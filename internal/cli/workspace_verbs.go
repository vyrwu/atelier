package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/config"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// The intent-workspace CONTROL SURFACE (WS-5): CLI verbs the driver agent uses
// to act on its workspace — add worktrees, read context, register/list/close
// PRs. These are the contract; `atelier mcp serve` is a thin MCP wrapper over
// exactly these functions (see internal/cli/mcp.go). Kept kernel-owned so the
// capability is a stable contract, not tied to a specific agent's transport.

// worktreeSubcommand + prSubcommand are added to `atelier workspace` and a
// top-level `atelier pr` (see WorkspaceCommand / PRCommand wiring in main).

// resolvedWorkspace is the workspace the current invocation acts on.
type resolvedWorkspace struct {
	Session string
	ID      string
	Root    string
}

// resolveCurrentWorkspace finds the workspace the caller is in. The agent runs
// in a popup launched with TMUX_PARENT_* env pointing at the workspace window,
// so popup.ResolveParentContext resolves it; a plain workspace pane resolves to
// itself. Reads @workspace_id / @workspace_root off the resolved window.
func resolveCurrentWorkspace(h *tmuxhost.Client) (resolvedWorkspace, error) {
	ctx, err := popup.ResolveParentContext(h)
	if err != nil {
		return resolvedWorkspace{}, err
	}
	// @workspace_id / @workspace_root are SESSION-scoped; read them at window
	// scope via display-message (inheritance), NOT GetWindowOption (which uses
	// show-window-options and errors on a session option).
	session, _ := h.DisplayMessageAt(ctx.WindowID, "#{session_name}")
	id, _ := h.DisplayMessageAt(ctx.WindowID, "#{"+workspace.OptWorkspaceID+"}")
	root, _ := h.DisplayMessageAt(ctx.WindowID, "#{"+workspace.OptWorkspaceRoot+"}")
	session, id, root = strings.TrimSpace(session), strings.TrimSpace(id), strings.TrimSpace(root)
	if id == "" {
		return resolvedWorkspace{}, fmt.Errorf("not inside an atelier workspace (no @workspace_id)")
	}
	if root == "" {
		root = workspace.WorkspaceRootFor(id)
	}
	return resolvedWorkspace{Session: session, ID: id, Root: root}, nil
}

// verbCodeRoot resolves the code root repos are found under (mirrors the
// workspaces tool's resolution so `worktree add owner/repo` locates the repo).
func verbCodeRoot() string {
	if v := os.Getenv("ATELIER_CODE_ROOT"); v != "" {
		return v
	}
	var cfg struct {
		CodeRoot string `toml:"code_root"`
	}
	_ = config.LoadSection("workspaces", &cfg)
	if cfg.CodeRoot != "" {
		return config.ExpandPath(cfg.CodeRoot)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "code", "github")
}

func verbWorktreeRoot() string {
	if v := os.Getenv("ATELIER_WORKTREE_ROOT"); v != "" {
		return v
	}
	var cfg struct {
		WorktreeRoot string `toml:"worktree_root"`
	}
	_ = config.LoadSection("workspaces", &cfg)
	if cfg.WorktreeRoot != "" {
		return config.ExpandPath(cfg.WorktreeRoot)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "code", ".worktrees", "github")
}

// worktreeAddCmd: `atelier workspace worktree add <owner/repo> <branch>`.
func worktreeAddCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "add <owner/repo> <branch>",
		Short: "Add a repo+branch worktree to the current workspace (creates worktree + symlink)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			h := tmuxhost.New(socket)
			ws, err := resolveCurrentWorkspace(h)
			if err != nil {
				return err
			}
			repo, branch := args[0], args[1]
			repoPath := filepath.Join(verbCodeRoot(), repo)
			if _, err := os.Stat(repoPath); err != nil {
				return fmt.Errorf("repo %q not found at %s", repo, repoPath)
			}
			wtPath := filepath.Join(verbWorktreeRoot(), repo, branch)
			wt, err := workspace.AddWorktree(h, ws.Session, ws.Root, repoPath, repo, branch, wtPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", wt.Link)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// worktreeListCmd: `atelier workspace worktree list`.
func worktreeListCmd() *cobra.Command {
	var socket, format string
	c := &cobra.Command{
		Use:   "list",
		Short: "List the current workspace's worktrees",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			ws, err := resolveCurrentWorkspace(h)
			if err != nil {
				return err
			}
			wts := loadWorktrees(ws.Session)
			out := cmd.OutOrStdout()
			if format == "json" {
				return json.NewEncoder(out).Encode(wts)
			}
			for _, wt := range wts {
				fmt.Fprintf(out, "%s\t%s\t%s\n", wt.Repo, wt.Branch, wt.Link)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}

// worktreeCmd groups the worktree verbs under `atelier workspace worktree`.
func worktreeCmd() *cobra.Command {
	c := &cobra.Command{Use: "worktree", Short: "Manage the current workspace's worktrees"}
	c.AddCommand(worktreeAddCmd(), worktreeListCmd())
	return c
}

// workspaceContextCmd: `atelier workspace context` — the agent's read of its own
// workspace (intent + worktrees + PRs), for the WS-7 context feed.
func workspaceContextCmd() *cobra.Command {
	var socket, format string
	c := &cobra.Command{
		Use:   "context",
		Short: "Print the current workspace's intent, worktrees, and PRs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			ws, err := resolveCurrentWorkspace(h)
			if err != nil {
				return err
			}
			st, _ := statestore.Load()
			rec := &statestore.Workspace{SessionName: ws.Session}
			if st != nil {
				if r := st.FindWorkspace(ws.Session); r != nil {
					rec = r
				}
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				return json.NewEncoder(out).Encode(rec)
			}
			fmt.Fprintf(out, "Workspace: %s\n", coalesce(rec.Title, ws.ID))
			if rec.Intent != "" {
				fmt.Fprintf(out, "Intent: %s\n", rec.Intent)
			}
			fmt.Fprintf(out, "Root: %s\n", ws.Root)
			fmt.Fprintf(out, "Worktrees (%d):\n", len(rec.Worktrees))
			for _, wt := range rec.Worktrees {
				fmt.Fprintf(out, "  %s @ %s → %s\n", wt.Repo, wt.Branch, wt.Link)
			}
			fmt.Fprintf(out, "Pull requests (%d):\n", len(rec.PRs))
			for _, pr := range rec.PRs {
				fmt.Fprintf(out, "  %s #%d [%s] ci=%s review=%s — %s\n",
					pr.Repo, pr.Number, pr.State, pr.CI, pr.ReviewDecision, pr.Title)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}

// PRCommand is the top-level `atelier pr` — register/list/close for the current
// workspace's pull requests. The agent registers PRs it opens; the Changes view
// (M-c) surfaces + acts on them.
func PRCommand() *cobra.Command {
	c := &cobra.Command{Use: "pr", Short: "Register, list, and close the workspace's pull requests"}
	c.AddCommand(prRegisterCmd(), prListCmd(), prCloseCmd())
	return c
}

var prURLRe = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/(?:pull|pulls)/(\d+)`)

// prRegisterCmd: `atelier pr register <url>` — record a PR the agent opened so
// atelier tracks it in the Changes view even before the branch-match sweep sees it.
func prRegisterCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "register <pr-url>",
		Short: "Register a pull request with the current workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h := tmuxhost.New(socket)
			ws, err := resolveCurrentWorkspace(h)
			if err != nil {
				return err
			}
			repo, number, ok := parsePRURL(args[0])
			if !ok {
				return fmt.Errorf("could not parse PR URL %q (want github.com/owner/repo/pull/N)", args[0])
			}
			pr := statestore.PR{Number: number, Repo: repo, URL: args[0], State: string(integration.ForgeOpen), Registered: true}
			if err := statestore.UpdateWorkspace(ws.Session, func(w *statestore.Workspace) {
				for i := range w.PRs {
					if w.PRs[i].Repo == repo && w.PRs[i].Number == number {
						w.PRs[i].Registered = true
						w.PRs[i].URL = args[0]
						return
					}
				}
				w.PRs = append(w.PRs, pr)
			}); err != nil {
				return err
			}
			// Refresh from the forge so the fields fill in immediately.
			workspace.SpawnForgeRefresh()
			fmt.Fprintf(cmd.OutOrStdout(), "registered %s #%d\n", repo, number)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func prListCmd() *cobra.Command {
	var socket, format string
	c := &cobra.Command{
		Use:   "list",
		Short: "List the current workspace's pull requests",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			ws, err := resolveCurrentWorkspace(h)
			if err != nil {
				return err
			}
			prs := loadPRs(ws.Session)
			out := cmd.OutOrStdout()
			if format == "json" {
				return json.NewEncoder(out).Encode(prs)
			}
			for _, pr := range prs {
				fmt.Fprintf(out, "%s #%d [%s] %s\n", pr.Repo, pr.Number, pr.State, pr.Title)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}

func prCloseCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "close <number|url>",
		Short: "Close a pull request (requires [forge] allow_write)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h := tmuxhost.New(socket)
			ws, err := resolveCurrentWorkspace(h)
			if err != nil {
				return err
			}
			forge := integration.Active().Forge
			if forge == nil {
				return fmt.Errorf("no forge integration configured")
			}
			pr, ok := findWorkspacePR(ws.Session, args[0])
			if !ok {
				return fmt.Errorf("no such PR in this workspace: %q", args[0])
			}
			if err := forge.Close(pr); err != nil {
				return err
			}
			_ = statestore.UpdateWorkspace(ws.Session, func(w *statestore.Workspace) {
				for i := range w.PRs {
					if w.PRs[i].Repo == pr.Repo && w.PRs[i].Number == pr.Number {
						w.PRs[i].State = string(integration.ForgeClosed)
					}
				}
			})
			fmt.Fprintf(cmd.OutOrStdout(), "closed %s #%d\n", pr.Repo, pr.Number)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// --- shared helpers (also called by the MCP server) --------------------------

func loadWorktrees(session string) []statestore.Worktree {
	st, err := statestore.Load()
	if err != nil || st == nil {
		return nil
	}
	if r := st.FindWorkspace(session); r != nil {
		return r.Worktrees
	}
	return nil
}

func loadPRs(session string) []statestore.PR {
	st, err := statestore.Load()
	if err != nil || st == nil {
		return nil
	}
	if r := st.FindWorkspace(session); r != nil {
		return r.PRs
	}
	return nil
}

// findWorkspacePR resolves a PR by number or URL within the workspace.
func findWorkspacePR(session, ref string) (integration.PullRequest, bool) {
	prs := loadPRs(session)
	if n, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil {
		for _, pr := range prs {
			if pr.Number == n {
				return storePRToIntegration(pr), true
			}
		}
	}
	if repo, number, ok := parsePRURL(ref); ok {
		for _, pr := range prs {
			if pr.Repo == repo && pr.Number == number {
				return storePRToIntegration(pr), true
			}
		}
	}
	return integration.PullRequest{}, false
}

func storePRToIntegration(pr statestore.PR) integration.PullRequest {
	return integration.PullRequest{
		Number: pr.Number, Repo: pr.Repo, Title: pr.Title,
		State: integration.ForgeState(pr.State), URL: pr.URL, Branch: pr.Branch,
	}
}

// parsePRURL extracts (owner/repo, number) from a GitHub PR URL. Pure.
func parsePRURL(url string) (repo string, number int, ok bool) {
	m := prURLRe.FindStringSubmatch(url)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return "", 0, false
	}
	return m[1] + "/" + m[2], n, true
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
