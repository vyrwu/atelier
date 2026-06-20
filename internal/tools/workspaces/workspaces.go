// Package workspaces is the atelier workspaces tool — the bash-exact port
// of tmux_session_picker, tmux_workspace_picker, tmux_workspace_name,
// tmux_workspace_prompt, tmux_workspace_build, tmux_workspace_auto_session,
// tmux_workspace_session_name, tmux_clone_workspace, tmux_delete_workspace,
// tmux_workspace_delete_prompt.
//
// All fzf invocations use the atelier shared palette (internal/fzfstyle)
// configured to match bash's exact accent colors per picker.
package workspaces

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"

	"github.com/vyrwu/atelier/internal/claudegen"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/initgen"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/spinner"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// ============================================================================
// SessionsCommand — port of tmux_session_picker
// ============================================================================

func SessionsCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "sessions",
		Short: "Pick an existing workspace session (bash-exact tmux_session_picker)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			rows, err := BuildSessionList(h)
			if err != nil {
				return err
			}
			// Empty state: still open the picker so the user sees a usable
			// dismissable surface (Esc cancels) and a hint to use M-n.
			// Erroring + pausing here causes the popup to stack on top of
			// itself when the user reflexively retries.
			lines := make([]string, 0, len(rows))
			for _, r := range rows {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", r.Session, r.Window, r.Display))
			}
			emptyHeader := ""
			if len(rows) == 0 {
				emptyHeader = "No workspaces yet — press M-n to create one, or Esc to dismiss"
			}

			// Bash uses red accent + extra `hl:red,hl+:red:bold` in --color.
			// fzfstyle.Args sets the standard palette; override with WithCustomColor
			// to add the red hl variants. Same prompt as bash: "栽 ".
			args := fzfstyle.Args("栽 ", "Select Workspace", "red",
				fzfstyle.WithCustomColor("prompt:red:bold,pointer:red,query:red,hl:red,hl+:red:bold,label:103,border:103,footer:103"),
				fzfstyle.WithDelimiter("\t"),
				fzfstyle.WithNth("3"),
				fzfstyle.WithBind("alt-x", "transform:"+dispatch.ToolCmd("workspaces", "_delete-prompt", "\"$FZF_PROMPT\"", "{}")),
				fzfstyle.WithBind("y", "transform:if [[ \"$FZF_PROMPT\" == Confirm* ]]; then echo \"execute-silent("+dispatch.ToolCmd("workspaces", "_delete-row", "{}")+")+reload("+dispatch.ToolCmd("workspaces", "_session-list")+")+change-prompt(栽 )\"; elif [[ \"$FZF_PROMPT\" == Cannot* ]]; then echo \"change-prompt(栽 )\"; else echo \"put(y)\"; fi"),
				fzfstyle.WithBind("n", "transform:if [[ \"$FZF_PROMPT\" == Confirm* || \"$FZF_PROMPT\" == Cannot* ]]; then echo \"change-prompt(栽 )\"; else echo \"put(n)\"; fi"),
				fzfstyle.WithBind("esc", "transform:if [[ \"$FZF_PROMPT\" == Confirm* || \"$FZF_PROMPT\" == Cannot* ]]; then echo \"change-prompt(栽 )\"; else echo \"abort\"; fi"),
				fzfstyle.WithBind("enter", "transform:if [[ \"$FZF_PROMPT\" == Confirm* ]]; then echo \"execute-silent("+dispatch.ToolCmd("workspaces", "_delete-row", "{}")+")+reload("+dispatch.ToolCmd("workspaces", "_session-list")+")+change-prompt(栽 )\"; elif [[ \"$FZF_PROMPT\" == Cannot* ]]; then echo \"change-prompt(栽 )\"; else echo \"accept\"; fi"),
				fzfstyle.WithBind("alt-s", "abort"),
				fzfstyle.WithBind("alt-n", "become("+dispatch.ToolCmd("workspaces", "pick")+")"),
				fzfstyle.WithBind("alt-u", "become("+dispatch.ToolCmd("workspaces", "clone")+")"),
				fzfstyle.WithFooter("M-x · delete  |  M-n · creator  |  M-u · clone url"),
			)
			if emptyHeader != "" {
				args = append(args, "--header="+emptyHeader)
			}

			debuglog.Logf("workspaces.sessions: opening picker (%d rows)", len(lines))
			picked, err := fzf.Pick(lines, args...)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					debuglog.Logf("workspaces.sessions: cancelled")
					return err
				}
				return err
			}
			// fzf with --ansi strips escape codes from the returned line.
			// Parse the first two tab-separated fields directly — those
			// fields are plain text (Session\tWindow) by construction.
			// Empty picked happens when fzf accepts on an empty list
			// (e.g. after a delete+reload bind emptied the rows) — treat
			// as a cancel.
			if picked == "" {
				debuglog.Logf("workspaces.sessions: empty pick — propagating cancel")
				return fzf.ErrCancelled
			}
			debuglog.Logf("workspaces.sessions: picked %q", picked)
			fields := strings.SplitN(picked, "\t", 3)
			if len(fields) < 2 {
				return fmt.Errorf("could not parse picked entry: %q", picked)
			}
			row := SessionRow{Session: fields[0], Window: fields[1]}

			// Same UX as bash:
			//  - on default branch of a repo session → run pull-default
			//  - if a claude popup backs the target window, defer-spawn it
			// Async pull (FR-7.1): capture repo info here, spawn the
			// `_bg-pull` subcommand AFTER LandOuter so the user is on
			// the workspace before any git work begins.
			bgRepoPath, _ := getSessionRepoPath(h, row.Session)
			var bgDefaultBranch string
			if bgRepoPath != "" {
				bgDefaultBranch = DefaultBranch(bgRepoPath)
			}

			// Deferred Claude popup open. Two trigger conditions:
			//
			//  1. Backing popup session ALREADY exists (claude was
			//     running, user is returning) → reopen the popup so it
			//     attaches to the live session.
			//
			//  2. Backing popup session does NOT exist BUT the window
			//     has @claude_active_session_id stamped (from a prior
			//     atelier run, persisted via statestore) → spawn a
			//     fresh popup which atelier-claude will launch with
			//     `--resume <id>`. This is the FR-5.2 auto-resume on
			//     workspace entry: tmux died, atelier restored the
			//     workspace, user M-s'es back in, Claude picks up where
			//     it left off.
			targetSid, _ := h.DisplayMessageAt(row.Session, "#{session_id}")
			targetWid, _ := h.DisplayMessageAt(row.Session+":"+row.Window, "#{window_id}")
			if targetSid != "" && targetWid != "" {
				claudeSession := claudePopupSessionName(targetSid, targetWid)
				hasPopup, _ := h.HasSession(claudeSession)
				// TODO(plugins-refactor): this cross-tool peek into the
				// AI plugin's namespace (`ai.active_session_id`) is the
				// last remaining hardcoded plugin dependency in the
				// foundational workspaces flow. When task #75 lands, the
				// AI plugin should register a "workspace-selected" hook
				// instead — workspaces fires the event, plugin decides
				// whether to spawn the popup.
				resumeID, _ := h.GetWindowOption(targetWid,
					statestore.MetadataKeyToOptionName("ai.active_session_id"))
				shouldSpawn := hasPopup || resumeID != ""
				if shouldSpawn {
					sidNum := strings.TrimPrefix(targetSid, "$")
					widNum := strings.TrimPrefix(targetWid, "@")
					// Use the canonical atelier popup style — same
					// geometry/border/accent as every other "full"
					// popup. Change those at initgen.PopupOptions
					// and this picks it up.
					popupOpts := initgen.PopupOptions(manifest.StyleFull, "Claude Code", false)
					popupCmd := fmt.Sprintf(
						`sleep 0.2 && tmux display-popup %s -e TMUX_PARENT_SESSION_ID=%s -e TMUX_PARENT_WINDOW_ID=%s -E '%s'`,
						popupOpts, sidNum, widNum,
						dispatch.ToolCmd("claude", "open"))
					_, _ = h.Run("run-shell", "-b", popupCmd)
				}
			}

			if err := workspace.LandOuter(h, "="+row.Session, "="+row.Session+":"+row.Window); err != nil {
				return err
			}
			// Spawn _bg-pull AFTER landing the user. Detached;
			// returns immediately. Failure (or no repo) is silent.
			if bgRepoPath != "" && bgDefaultBranch != "" && targetWid != "" {
				workspace.SpawnBgPull(bgRepoPath, bgDefaultBranch, targetWid)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// ============================================================================
// PickCommand — port of tmux_workspace_picker (Step 1)
// ============================================================================

func PickCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "pick",
		Short: "Pick or create a workspace (bash-exact tmux_workspace_picker)",
		RunE: func(_ *cobra.Command, _ []string) error {
			base := workspaceCodeRoot()
			if _, err := os.Stat(base); err != nil {
				return fmt.Errorf("no %s", base)
			}

			repos := []string{}
			err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(base, p)
				depth := strings.Count(rel, string(os.PathSeparator)) + 1
				if rel == "." {
					return nil
				}
				if !d.IsDir() {
					return nil
				}
				if depth == 2 {
					repos = append(repos, rel)
					return filepath.SkipDir
				}
				if depth >= 2 {
					return filepath.SkipDir
				}
				return nil
			})
			if err != nil {
				return err
			}
			sort.Strings(repos)

			lines := make([]string, 0, len(repos))
			for _, r := range repos {
				// Display: cyan owner/repo
				parts := strings.SplitN(r, "/", 2)
				if len(parts) == 2 {
					lines = append(lines, fmt.Sprintf("%s\t\033[36m%s/%s\033[0m", r, parts[0], parts[1]))
				} else {
					lines = append(lines, fmt.Sprintf("%s\t\033[36m%s\033[0m", r, r))
				}
			}

			footerRepo := "M-a · auto mode  |  M-s · selector  |  M-u · clone url"
			footerAuto := "M-a · repo mode  |  M-s · selector  |  M-u · clone url"

			args := fzfstyle.Args("製 ", "New Workspace", "green",
				fzfstyle.WithCustomColor("prompt:green:bold,pointer:green,query:green,hl:green,hl+:green:bold,label:103,border:103,footer:103"),
				fzfstyle.WithDelimiter("\t"),
				fzfstyle.WithNth("2"),
				fzfstyle.WithBind("alt-n", "abort"),
				fzfstyle.WithBind("alt-s", "become("+dispatch.ToolCmd("workspaces", "sessions")+")"),
				fzfstyle.WithBind("alt-u", "become("+dispatch.ToolCmd("workspaces", "clone")+")"),
				fzfstyle.WithBind("alt-a", fmt.Sprintf(
					`transform:if [[ "$FZF_PROMPT" == '製 ' ]]; then echo 'change-prompt(製? )+disable-search+change-footer(%s)'; else echo 'change-prompt(製 )+enable-search+change-footer(%s)'; fi`,
					footerAuto, footerRepo)),
				fzfstyle.WithBind("enter", fmt.Sprintf(`transform:if [[ "$FZF_PROMPT" == "製? " ]]; then echo "become(%s {q})"; else echo "accept"; fi`, dispatch.ToolCmd("workspaces", "_auto-session"))),
				fzfstyle.WithFooter(footerRepo),
			)
			debuglog.Logf("workspaces.pick: opening repo picker (%d repos)", len(lines))
			picked, err := fzf.Pick(lines, args...)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					debuglog.Logf("workspaces.pick: cancelled")
					return err
				}
				return err
			}
			repo, cancelled := interpretPickedRepo(picked)
			if cancelled {
				debuglog.Logf("workspaces.pick: empty pick (become chain ended) — propagating cancel")
				return fzf.ErrCancelled
			}
			repoPath := filepath.Join(base, repo)
			defaultBranch := DefaultBranch(repoPath)
			debuglog.Logf("workspaces.pick: picked repo=%q → name flow", repo)

			_ = socket
			return runWorkspaceName(repo, repoPath, defaultBranch, "")
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// ============================================================================
// CloneCommand — port of tmux_clone_workspace
// ============================================================================

var cloneURLRe = regexp.MustCompile(`^(https://github\.com/|git@github\.com:)([^/]+)/([^/[:space:]]+)/?$`)

func CloneCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "clone",
		Short: "Prompt for a GitHub URL and clone (bash-exact tmux_clone_workspace)",
		RunE: func(_ *cobra.Command, _ []string) error {
			base := workspaceCodeRoot()
			query := ""
			errMsg := ""
			for {
				header := "paste a github URL → clone + open default branch"
				if errMsg != "" {
					header = errMsg
				}
				args := fzfstyle.Args("複 ", "Clone Repo", "yellow",
					fzfstyle.WithCustomColor("prompt:yellow:bold,pointer:yellow,query:yellow,hl:yellow,hl+:yellow:bold,label:103,border:103,header:yellow,footer:103"),
					fzfstyle.WithNoClear(),
					fzfstyle.WithPrintQuery(),
					fzfstyle.WithExpect("enter"),
					fzfstyle.WithBind("alt-s", "become("+dispatch.ToolCmd("workspaces", "sessions")+")"),
					fzfstyle.WithBind("alt-n", "become("+dispatch.ToolCmd("workspaces", "pick")+")"),
					fzfstyle.WithHeader(header),
					fzfstyle.WithFooter("M-s · selector  |  M-n · creator"),
					fzfstyle.WithQuery(query),
				)
				res, err := fzf.PickWithExpect(nil, []string{"enter"}, dropPrompts(args)...)
				if err != nil {
					if errors.Is(err, fzf.ErrCancelled) {
						return err
					}
					return err
				}
				// fzf become() short-circuit: see TestCreator_PromptFlow_*
				// and the inline comment in runWorkspaceName.
				if res.Key == "" && res.Query == "" && res.Selection == "" {
					debuglog.Logf("CloneCommand: fzf returned empty (likely become()) — exit silently")
					return nil
				}
				url := strings.TrimSpace(res.Query)
				if url == "" {
					errMsg = "✗ enter a github URL"
					continue
				}
				m := cloneURLRe.FindStringSubmatch(url)
				if m == nil {
					errMsg = "✗ unrecognized URL — expected https://github.com/o/r or git@github.com:o/r"
					query = url
					continue
				}
				owner := m[2]
				repo := strings.TrimSuffix(strings.TrimSuffix(m[3], "/"), ".git")
				target := filepath.Join(base, owner, repo)
				session := owner + "/" + repo

				if _, err := os.Stat(target); err != nil {
					_ = os.MkdirAll(filepath.Dir(target), 0o755)
					err := spinner.NewBox(fmt.Sprintf("Cloning %s/%s...", owner, repo)).Run(func() error {
						return runGit("", "clone", url, target)
					})
					if err != nil {
						errMsg = fmt.Sprintf("✗ clone failed for %s/%s", owner, repo)
						query = url
						continue
					}
				}

				defaultBranch := DefaultBranch(target)
				h := tmuxhost.New("")
				// Canonical "open default branch" sequence — ensures
				// session, ensures default-branch window, lands outer
				// client, fires bg-pull, registers in cache. One
				// primitive, no inline reimplementation.
				return workspace.OpenDefaultBranch(h, session, target, defaultBranch,
					ensureDefaultBranchWindow)
			}
		},
	}
	return c
}

// ============================================================================
// DeleteCommand + helpers for the fzf bind transforms
// ============================================================================

func DeleteCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "delete",
		Short: "Delete the current worktree + cascade popup cleanup",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			pathOut, err := h.DisplayMessage("#{pane_current_path}")
			if err != nil {
				return err
			}
			worktreeRoot := workspaceWorktreeRoot()
			if !strings.HasPrefix(pathOut, worktreeRoot) {
				return fmt.Errorf("current path %q is not under %q; refusing to delete", pathOut, worktreeRoot)
			}
			repoSlug, _ := splitWorktreePath(pathOut, worktreeRoot)
			repoPath := filepath.Join(workspaceCodeRoot(), repoSlug)
			if err := removeWorktree(repoPath, pathOut); err != nil {
				return fmt.Errorf("git worktree remove: %w", err)
			}
			if _, err := h.Run("kill-window"); err != nil {
				return err
			}
			return hostpopup.CleanupOrphanedPopups(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// _delete-prompt and _delete-row are internal subcommands used by the
// session-picker fzf binds. They mirror tmux_workspace_delete_prompt and
// tmux_delete_workspace.
func DeletePromptCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_delete-prompt",
		Short:  "internal: fzf ctrl-x action for the session picker",
		Hidden: true,
		Args:   cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := args[0]
			line := args[1]
			out := cmd.OutOrStdout()
			switch {
			case strings.HasPrefix(prompt, "Confirm"):
				return nil
			case strings.HasPrefix(prompt, "Cannot"):
				fmt.Fprintln(out, "change-prompt(栽 )")
				return nil
			}
			fields := strings.SplitN(line, "\t", 3)
			if len(fields) < 2 {
				return nil
			}
			session, window := fields[0], fields[1]
			h := tmuxhost.New("")
			repoPath, _ := getSessionRepoPath(h, session)
			defaultBranch := ""
			if repoPath != "" {
				defaultBranch = DefaultBranch(repoPath)
			}
			if repoPath != "" && window == defaultBranch {
				countOut, _ := h.Run("list-windows", "-t", "="+session, "-F", "#W")
				count := 0
				for _, l := range strings.Split(strings.TrimSpace(string(countOut)), "\n") {
					if l != "" {
						count++
					}
				}
				if count > 1 {
					fmt.Fprintln(out, "change-prompt(Cannot delete — close attached workspaces first. )")
					return nil
				}
				fmt.Fprintln(out, "change-prompt(Confirm? y/n: )")
				return nil
			}
			fmt.Fprintln(out, "change-prompt(Confirm? y/n: )")
			return nil
		},
	}
}

func DeleteRowCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_delete-row",
		Short:  "internal: delete a single workspace row (called from session picker)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			line := args[0]
			fields := strings.SplitN(line, "\t", 3)
			if len(fields) < 2 {
				return nil
			}
			session, window := fields[0], fields[1]
			h := tmuxhost.New("")
			repoPath, _ := getSessionRepoPath(h, session)
			defaultBranch := ""
			if repoPath != "" {
				defaultBranch = DefaultBranch(repoPath)
			}

			// If we're deleting a non-default window: just remove its worktree + kill window.
			if repoPath != "" && window != defaultBranch {
				wtPath := filepath.Join(workspaceWorktreeRoot(), session, window)
				_ = removeWorktree(repoPath, wtPath)
				_, _ = h.Run("kill-window", "-t", "="+session+":"+window)
				// Mirror the deletion to the persisted cache so restore
				// doesn't resurrect the workspace the user just nuked.
				// RemoveWindow auto-removes the workspace when its last
				// window goes — so both branches converge correctly.
				_ = statestore.RemoveWindow(session, window)
				return hostpopup.CleanupOrphanedPopups(h)
			}
			// Default branch with no other windows: kill whole session.
			_, _ = h.Run("kill-session", "-t", "="+session)
			_ = statestore.RemoveSession(session)
			return hostpopup.CleanupOrphanedPopups(h)
		},
	}
}

// SessionListCommand emits the workspace selector lines (for fzf --reload).
func SessionListCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_session-list",
		Short:  "internal: emit session-picker lines (for fzf --reload)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New("")
			rows, err := BuildSessionList(h)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", r.Session, r.Window, r.Display)
			}
			return nil
		},
	}
}

// AutoSessionCommand: port of tmux_workspace_auto_session
func AutoSessionCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_auto-session",
		Short:  "internal: multi-repo session creator (Ctrl-A in repo picker dispatches here)",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			initialPrompt := ""
			if len(args) > 0 {
				initialPrompt = args[0]
			}
			return runAutoSession(initialPrompt)
		},
	}
}

// CreateCommand kept for back-compat (non-interactive create from --repo/--branch).
func CreateCommand() *cobra.Command {
	var repo, branch, socket string
	c := &cobra.Command{
		Use:   "create",
		Short: "Create a workspace non-interactively (--repo --branch)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if repo == "" || branch == "" {
				return fmt.Errorf("--repo and --branch are required")
			}
			repoPath := filepath.Join(workspaceCodeRoot(), repo)
			defaultBranch := DefaultBranch(repoPath)
			return runWorkspaceName(repo, repoPath, defaultBranch, branch)
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "repo (owner/repo)")
	c.Flags().StringVar(&branch, "branch", "", "branch / worktree name")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func ListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List worktrees under WorktreeRoot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := workspaceWorktreeRoot()
			wts, err := listWorktrees(root)
			if err != nil {
				return err
			}
			for _, w := range wts {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", w.repo, w.branch)
			}
			return nil
		},
	}
}

// ============================================================================
// Internal: tmux_workspace_name port
// ============================================================================

// runWorkspaceName is the bash tmux_workspace_name port. Loops until the
// user picks/confirms a name or cancels. Pre-fills query on error retries.
func runWorkspaceName(repo, repoPath, defaultBranch, initialName string) error {
	session := repo

	ensureSession := func() (created bool, err error) {
		return workspace.EnsureSession(tmuxhost.New(""), session, repoPath, defaultBranch)
	}

	query := initialName
	errMsg := ""
	name := initialName

	for {
		if name == "" {
			header := fmt.Sprintf("branch name → new worktree · empty → open %s", defaultBranch)
			if errMsg != "" {
				header = errMsg
			}
			args := fzfstyle.Args("製 ", "Workspace in "+repo, "green",
				fzfstyle.WithCustomColor("prompt:green:bold,pointer:green,query:green,hl:green,hl+:green:bold,label:103,border:103,header:red,footer:103"),
				fzfstyle.WithNoClear(),
				fzfstyle.WithPrintQuery(),
				fzfstyle.WithExpect("enter"),
				fzfstyle.WithBind("alt-n", "abort"),
				fzfstyle.WithBind("alt-s", "become("+dispatch.ToolCmd("workspaces", "sessions")+")"),
				fzfstyle.WithBind("alt-u", "become("+dispatch.ToolCmd("workspaces", "clone")+")"),
				fzfstyle.WithBind("alt-a", fmt.Sprintf("become(%s %q %q %q {q})", dispatch.ToolCmd("workspaces", "_prompt"),
					repo, repoPath, defaultBranch)),
				fzfstyle.WithHeader(header),
				fzfstyle.WithFooter("M-a · auto mode  |  M-s · selector  |  M-u · clone url"),
				fzfstyle.WithQuery(query),
			)
			res, err := fzf.PickWithExpect(nil, []string{"enter"}, dropPrompts(args)...)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return err
				}
				return err
			}
			// When the user hits Ctrl-A / Ctrl-S / Ctrl-U, fzf execs into
			// the bound command via `become()` — replacing fzf entirely.
			// That replacement's exit code becomes fzf's, and fzf produces
			// NO output: no expect-key, no query, no selection. We must
			// NOT confuse that with "user pressed Enter on empty query"
			// (which sets Key="enter") — otherwise the empty-Enter
			// default-branch path runs on top of the workspace the
			// become()-spawned process just created and selected.
			// Bash dodges this with `[[ -z "$result" ]] && exit 0`.
			if res.Key == "" && res.Query == "" && res.Selection == "" {
				debuglog.Logf("runWorkspaceName: fzf returned empty (likely become() replaced it) — exit silently")
				return nil
			}
			name = strings.TrimSpace(res.Query)

			if name == "" {
				// Canonical "open default branch" sequence: ensure
				// session + default-branch window + LandOuter +
				// bg-pull + cache registration. Single primitive.
				return workspace.OpenDefaultBranch(
					tmuxhost.New(""), session, repoPath, defaultBranch,
					ensureDefaultBranchWindow)
			}
		}

		// If a window with this name already exists in the session, jump to it.
		h := tmuxhost.New("")
		if has, _ := h.HasSession(session); has {
			// list-windows with #{window_id}\t#W so we can target the
			// existing window by its @ID instead of by name (which
			// silently fails when name contains `/`).
			out, _ := h.Run("list-windows", "-t", "="+session, "-F", "#{window_id}\t#W")
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				parts := strings.SplitN(line, "\t", 2)
				if len(parts) != 2 {
					continue
				}
				if parts[1] == name {
					return workspace.LandOuter(h, "="+session, parts[0])
				}
			}
		}

		// If a branch with this name exists but isn't a worktree, error.
		if branchExists(repoPath, name) {
			wtPath := worktreePathForBranch(repoPath, name)
			if wtPath != "" {
				created, err := ensureSession()
				if err != nil {
					return err
				}
				h := tmuxhost.New("")
				spec := workspace.WorktreeWindowSpec{
					Session:    session,
					WtPath:     wtPath,
					WindowName: name,
				}
				if created {
					spec.KillDefaultBranch = defaultBranch
				}
				newWid, err := workspace.CreateWorktreeWindow(h, spec)
				if err != nil {
					return err
				}
				return workspace.LandOuter(h, "="+session, newWid)
			}
			errMsg = fmt.Sprintf("✗ branch '%s' exists but isn't a worktree — pick another name", name)
			query = name
			name = ""
			continue
		}

		// Build the worktree.
		wtPath := filepath.Join(workspaceWorktreeRoot(), repo, name)
		_ = os.MkdirAll(filepath.Dir(wtPath), 0o755)
		var newWid string
		sp := spinner.NewBox(fmt.Sprintf("Building workspace '%s'...", name))
		err := sp.Run(func() error {
			sp.SetStatus(fmt.Sprintf("Fetching origin/%s...", defaultBranch))
			if err := runGit(repoPath, "fetch", "origin", defaultBranch); err != nil {
				return err
			}
			sp.SetStatus("Building worktree...")
			if err := runGit(repoPath, "worktree", "add", wtPath, "-b", name, "origin/"+defaultBranch); err != nil {
				return err
			}
			sp.SetStatus("Stamping tmux options...")
			created, err := ensureSession()
			if err != nil {
				return err
			}
			spec := workspace.WorktreeWindowSpec{
				Session:    session,
				WtPath:     wtPath,
				WindowName: name,
			}
			if created {
				spec.KillDefaultBranch = defaultBranch
			}
			newWid, err = workspace.CreateWorktreeWindow(h, spec)
			return err
		})
		if err != nil {
			errMsg = fmt.Sprintf("✗ workspace '%s' build failed — try another name", name)
			query = name
			name = ""
			continue
		}
		return workspace.LandOuter(h, "="+session, newWid)
	}
}

// ============================================================================
// Internal: tmux_workspace_prompt port (auto-mode Claude-named branch)
// ============================================================================

func PromptCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_prompt",
		Short:  "internal: auto-mode Claude-named branch flow (Ctrl-A in manual name)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			repo := args[0]
			repoPath := args[1]
			defaultBranch := args[2]
			initialPrompt := ""
			if len(args) > 3 {
				initialPrompt = args[3]
			}
			return runWorkspacePrompt(repo, repoPath, defaultBranch, initialPrompt)
		},
	}
}

func runWorkspacePrompt(repo, repoPath, defaultBranch, initialPrompt string) error {
	session := repo
	ensureSession := func() (created bool, err error) {
		return workspace.EnsureSession(tmuxhost.New(""), session, repoPath, defaultBranch)
	}

	query := initialPrompt
	errMsg := ""
	prompt := initialPrompt
	for {
		if prompt == "" {
			header := fmt.Sprintf("task description → auto branch · empty → open %s", defaultBranch)
			if errMsg != "" {
				header = errMsg
			}
			args := fzfstyle.Args("製? ", "Workspace in "+repo, "green",
				fzfstyle.WithCustomColor("prompt:green:bold,pointer:green,query:green,hl:green,hl+:green:bold,label:103,border:103,header:red,footer:103"),
				fzfstyle.WithNoClear(),
				fzfstyle.WithPrintQuery(),
				fzfstyle.WithExpect("enter"),
				fzfstyle.WithBind("alt-n", "abort"),
				fzfstyle.WithBind("alt-a", fmt.Sprintf("become(%s %q %q %q {q})", dispatch.ToolCmd("workspaces", "_name"),
					repo, repoPath, defaultBranch)),
				fzfstyle.WithBind("alt-s", "become("+dispatch.ToolCmd("workspaces", "sessions")+")"),
				fzfstyle.WithBind("alt-u", "become("+dispatch.ToolCmd("workspaces", "clone")+")"),
				fzfstyle.WithHeader(header),
				fzfstyle.WithFooter("M-a · manual name  |  M-s · selector  |  M-u · clone url"),
				fzfstyle.WithQuery(query),
			)
			res, err := fzf.PickWithExpect(nil, []string{"enter"}, dropPrompts(args)...)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return err
				}
				return err
			}
			// fzf become() short-circuit: see TestCreator_PromptFlow_*
			// and the inline comment in runWorkspaceName.
			if res.Key == "" && res.Query == "" && res.Selection == "" {
				debuglog.Logf("runWorkspacePrompt: fzf returned empty (likely become()) — exit silently")
				return nil
			}
			prompt = strings.TrimSpace(res.Query)
			if prompt == "" {
				// Empty → open default branch (canonical primitive).
				return workspace.OpenDefaultBranch(
					tmuxhost.New(""), session, repoPath, defaultBranch,
					ensureDefaultBranchWindow)
			}
		}

		var name, wtPath, newWid string
		h := tmuxhost.New("")
		sp := spinner.NewBox("Building workspace...")
		err := sp.Run(func() error {
			n, w, e := buildClaudeNamedWorkspace(sp, prompt, repo, repoPath, defaultBranch)
			name, wtPath = n, w
			if e != nil {
				return e
			}
			// Stamping stage: ensureSession + new-window + set-option.
			// Kept inside the spinner so the FR-2.1 four-stage sequence
			// renders cleanly. Visible client moves (select-window /
			// switch-client) happen AFTER the spinner closes so the
			// user isn't shown a transient view.
			sp.SetStatus("Stamping tmux options...")
			created, err := ensureSession()
			if err != nil {
				return err
			}
			spec := workspace.WorktreeWindowSpec{
				Session:    session,
				WtPath:     wtPath,
				WindowName: name,
				Kind:       "worktree",
				// TODO(plugins-refactor): the prompt + workspace-kind
				// metadata writes are AI-plugin concerns — workspaces is
				// hardcoding the AI namespace. When task #75 lands, the
				// AI plugin should contribute these via a "before-create"
				// hook instead of workspaces knowing about ai.* keys.
				Metadata: map[string]string{
					"ai.prompt":         prompt,
					"ai.workspace_kind": "worktree",
				},
			}
			if created {
				spec.KillDefaultBranch = defaultBranch
			}
			newWid, err = workspace.CreateWorktreeWindow(h, spec)
			return err
		})
		if err != nil {
			errMsg = fmt.Sprintf("✗ build failed: %v — refine prompt", err)
			// Surface via tmux display-message too so the user sees it
			// even if the popup tears down before the re-prompt header
			// renders fully.
			_, _ = tmuxhost.New("").Run("display-message", errMsg)
			query = prompt
			prompt = ""
			continue
		}

		if err := workspace.LandOuter(h, "="+session, newWid); err != nil {
			return err
		}
		// Log post-state so we can see where the client actually landed.
		if v, err := h.DisplayMessage("#{client_name}|#{client_session}|#{window_id}|#{window_name}"); err == nil {
			debuglog.Logf("runWorkspacePrompt: post-switch state=%q", v)
		}

		newSid, _ := h.DisplayMessageAt(newWid, "#{session_id}")
		sidNum := strings.TrimPrefix(strings.TrimSpace(newSid), "$")
		widNum := strings.TrimPrefix(newWid, "@")

		// Defer the claude popup — we're still inside the creator
		// popup, and tmux only allows one popup per client at a time.
		// Pass explicit -e env so atelier-claude open computes the
		// backing-session name `_atelier_claude_<sid>_<wid>` against
		// the new window, not the stale outer-chain globals.
		// `-c <outer-client>` ensures display-popup opens on the
		// user's real client (matters when more than one is attached).
		outerClient, _ := h.ShowGlobalOption("@atelier_outer_client")
		clientArg := ""
		if outerClient != "" {
			clientArg = fmt.Sprintf(" -c '%s'", outerClient)
		}
		popupCmd := fmt.Sprintf(
			`sleep 0.15 && tmux display-popup%s -b rounded -S "fg=colour103" -T "#[align=centre] Claude Code " -w100%% -h99%% -y S -e TMUX_PARENT_SESSION_ID=%s -e TMUX_PARENT_WINDOW_ID=%s -E '"+dispatch.ToolCmd("claude", "open")+"'`,
			clientArg, sidNum, widNum)
		_, _ = h.Run("run-shell", "-b", popupCmd)
		return nil
	}
}

// NameCommand is the alias used from _prompt's Ctrl-A.
func NameCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_name",
		Short:  "internal: manual-name flow",
		Hidden: true,
		Args:   cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			repo := args[0]
			repoPath := args[1]
			defaultBranch := args[2]
			initial := ""
			if len(args) > 3 {
				initial = args[3]
			}
			return runWorkspaceName(repo, repoPath, defaultBranch, initial)
		},
	}
}

// ============================================================================
// Internal: tmux_workspace_build port
// ============================================================================

var conventionalBranchRe = regexp.MustCompile(`^(feat|fix|chore|refactor|docs|test|perf|style)/[a-z0-9-]+$`)

const branchNamingSysPrompt = `You generate git branch names following conventional commits.
Output exactly ONE line in the format <type>/<short-kebab-description>.
Allowed types: feat, fix, chore, refactor, docs, test, perf, style.
Description must be 2-5 words in kebab-case, all lowercase, only [a-z0-9-].
Output ONLY the branch name on a single line. No quotes. No explanation. No code blocks.`

// stageReporter is the minimal interface buildClaudeNamedWorkspace needs
// from spinner.BoxSpinner. Defined here so tests can substitute a no-op.
type stageReporter interface {
	SetStatus(label string)
}

type noopReporter struct{}

func (noopReporter) SetStatus(string) {}

func buildClaudeNamedWorkspace(sp stageReporter, prompt, repo, repoPath, defaultBranch string) (name, wtPath string, err error) {
	if sp == nil {
		sp = noopReporter{}
	}
	sp.SetStatus("Inferring branch name...")
	gen := claudegen.New()
	gen.Model = "sonnet"
	raw, err := gen.RunWithSystemPrompt(branchNamingSysPrompt, prompt)
	if err != nil {
		return "", "", err
	}
	name = strings.ToLower(strings.TrimSpace(strings.SplitN(strings.TrimRight(raw, "\r\n"), "\n", 2)[0]))
	if !conventionalBranchRe.MatchString(name) {
		return name, "", fmt.Errorf("invalid name: %q", name)
	}
	if branchExists(repoPath, name) {
		return name, "", fmt.Errorf("branch %q already exists", name)
	}
	wtPath = filepath.Join(workspaceWorktreeRoot(), repo, name)
	_ = os.MkdirAll(filepath.Dir(wtPath), 0o755)
	sp.SetStatus(fmt.Sprintf("Fetching origin/%s...", defaultBranch))
	if err := runGit(repoPath, "fetch", "origin", defaultBranch); err != nil {
		return name, "", fmt.Errorf("fetch: %w", err)
	}
	sp.SetStatus("Building worktree...")
	if err := runGit(repoPath, "worktree", "add", wtPath, "-b", name, "origin/"+defaultBranch); err != nil {
		return name, "", fmt.Errorf("worktree add: %w", err)
	}
	return name, wtPath, nil
}

// ============================================================================
// Internal: tmux_workspace_auto_session port
// ============================================================================

var autoSessionNameRe = regexp.MustCompile(`^auto/[a-z0-9-]+$`)

const sessionNamingSysPrompt = `You generate short tmux session names for multi-repo work.
Output exactly ONE line in the format auto/<short-kebab-description>.
Description must be 2-5 words in kebab-case, all lowercase, only [a-z0-9-].
Output ONLY the session name on a single line. No quotes. No explanation. No code blocks.`

func runAutoSession(initialPrompt string) error {
	base := workspaceMultiRepoRoot()
	_ = os.MkdirAll(base, 0o755)

	query := initialPrompt
	errMsg := ""
	prompt := initialPrompt
	for {
		if prompt == "" {
			header := "describe the multi-repo task → claude will name the session"
			if errMsg != "" {
				header = errMsg
			}
			args := fzfstyle.Args("製? ", "New Workspace", "green",
				fzfstyle.WithCustomColor("prompt:green:bold,pointer:green,query:green,hl:green,hl+:green:bold,label:103,border:103,header:red,footer:103"),
				fzfstyle.WithNoClear(),
				fzfstyle.WithPrintQuery(),
				fzfstyle.WithExpect("enter"),
				fzfstyle.WithBind("alt-n", "abort"),
				fzfstyle.WithBind("alt-a", "become("+dispatch.ToolCmd("workspaces", "pick")+")"),
				fzfstyle.WithBind("alt-s", "become("+dispatch.ToolCmd("workspaces", "sessions")+")"),
				fzfstyle.WithHeader(header),
				fzfstyle.WithFooter("M-a · pick repo  |  M-s · selector"),
				fzfstyle.WithQuery(query),
			)
			res, err := fzf.PickWithExpect(nil, []string{"enter"}, dropPrompts(args)...)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return err
				}
				return err
			}
			// fzf become() short-circuit: see TestCreator_PromptFlow_*
			// and the inline comment in runWorkspaceName.
			if res.Key == "" && res.Query == "" && res.Selection == "" {
				debuglog.Logf("runAutoSession: fzf returned empty (likely become()) — exit silently")
				return nil
			}
			prompt = strings.TrimSpace(res.Query)
			if prompt == "" {
				return nil
			}
		}

		var name string
		var alreadyExists bool
		h := tmuxhost.New("")
		sp := spinner.NewBox("Building workspace...")
		err := sp.Run(func() error {
			sp.SetStatus("Asking Claude for a session name...")
			gen := claudegen.New()
			gen.Model = "sonnet"
			raw, e := gen.RunWithSystemPrompt(sessionNamingSysPrompt, prompt)
			if e != nil {
				return e
			}
			name = strings.ToLower(strings.TrimSpace(strings.SplitN(strings.TrimRight(raw, "\r\n"), "\n", 2)[0]))
			if !autoSessionNameRe.MatchString(name) {
				return fmt.Errorf("invalid name: %q", name)
			}
			if has, _ := h.HasSession(name); has {
				alreadyExists = true
				return nil
			}
			sp.SetStatus("Stamping tmux options...")
			if _, err := h.Run("new-session", "-d", "-s", name, "-c", base); err != nil {
				return err
			}
			// TODO(plugins-refactor): same as the worktree creator path —
			// AI plugin metadata stamping (`ai.prompt`, `ai.workspace_kind`)
			// is hardcoded here. Moves to a plugin-contributed
			// before-create hook in task #75.
			_, _ = h.Run("set-option", "-w", "-t", "="+name+":1",
				statestore.MetadataKeyToOptionName("ai.prompt"), prompt)
			_, _ = h.Run("set-option", "-w", "-t", "="+name+":1",
				statestore.MetadataKeyToOptionName("ai.workspace_kind"), "multi-repo")
			// Default window 1 of an auto-session is unnamed (`bash` /
			// `zsh`) — register it under its tmux-default name "1" so
			// statestore restore can find it back. The persistent
			// identity is (session_name, window_name).
			defaultWinName, _ := h.DisplayMessageAt("="+name+":1", "#{window_name}")
			defaultWinName = strings.TrimSpace(defaultWinName)
			if defaultWinName == "" {
				defaultWinName = "1"
			}
			workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
				Session:    name,
				Kind:       "multi-repo",
				WindowName: defaultWinName,
				Cwd:        base,
				Metadata: map[string]string{
					"ai.prompt":         prompt,
					"ai.workspace_kind": "multi-repo",
				},
			})
			return nil
		})
		if err != nil {
			errMsg = fmt.Sprintf("✗ %v — retry", err)
			query = prompt
			prompt = ""
			continue
		}

		if alreadyExists {
			return workspace.LandOuter(h, "="+name, "="+name+":1")
		}
		if err := workspace.LandOuter(h, "="+name, "="+name+":1"); err != nil {
			return err
		}

		// Canonical atelier full-popup style + canonical dispatch
		// string. No hand-rolled display-popup args.
		popupCmd := fmt.Sprintf("sleep 0.15 && tmux display-popup %s -E '%s'",
			initgen.PopupOptions(manifest.StyleFull, "Claude Code", false),
			dispatch.ToolCmd("claude", "open"))
		_, _ = h.Run("run-shell", "-b", popupCmd)
		return nil
	}
}

// ============================================================================
// Helpers
// ============================================================================

func workspaceCodeRoot() string {
	home, _ := os.UserHomeDir()
	if v := os.Getenv("ATELIER_CODE_ROOT"); v != "" {
		return v
	}
	return filepath.Join(home, "code", "github")
}

func workspaceWorktreeRoot() string {
	home, _ := os.UserHomeDir()
	if v := os.Getenv("ATELIER_WORKTREE_ROOT"); v != "" {
		return v
	}
	return filepath.Join(home, "code", ".worktrees", "github")
}

func workspaceMultiRepoRoot() string {
	home, _ := os.UserHomeDir()
	if v := os.Getenv("ATELIER_MULTI_REPO_ROOT"); v != "" {
		return v
	}
	return filepath.Join(home, "code")
}

func claudePopupSessionName(sid, wid string) string {
	return fmt.Sprintf("_atelier_claude_%s_%s",
		strings.TrimPrefix(sid, "$"), strings.TrimPrefix(wid, "@"))
}

func splitWorktreePath(p, root string) (repoSlug, branch string) {
	rel := strings.TrimPrefix(p, root)
	rel = strings.TrimPrefix(rel, "/")
	parts := strings.Split(rel, "/")
	if len(parts) >= 3 {
		return parts[0] + "/" + parts[1], parts[2]
	}
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return rel, ""
}

type wt struct {
	repo, branch, path string
}

func listWorktrees(root string) ([]wt, error) {
	var out []wt
	owners, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		ownerPath := filepath.Join(root, owner.Name())
		children, err := os.ReadDir(ownerPath)
		if err != nil {
			continue
		}
		hasNested := false
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			nestedPath := filepath.Join(ownerPath, child.Name())
			nested, err := os.ReadDir(nestedPath)
			if err != nil {
				continue
			}
			for _, br := range nested {
				if !br.IsDir() {
					continue
				}
				out = append(out, wt{owner.Name() + "/" + child.Name(), br.Name(), filepath.Join(nestedPath, br.Name())})
				hasNested = true
			}
		}
		if hasNested {
			continue
		}
		for _, br := range children {
			if !br.IsDir() {
				continue
			}
			out = append(out, wt{owner.Name(), br.Name(), filepath.Join(ownerPath, br.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].repo != out[j].repo {
			return out[i].repo < out[j].repo
		}
		return out[i].branch < out[j].branch
	})
	return out, nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func runGitQuiet(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func branchExists(repoPath, branch string) bool {
	return runGitQuiet(repoPath, "show-ref", "--verify", "refs/heads/"+branch) != ""
}

func worktreePathForBranch(repoPath, branch string) string {
	out := runGitQuiet(repoPath, "worktree", "list", "--porcelain")
	var curPath string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			curPath = strings.TrimPrefix(line, "worktree ")
		}
		if line == "branch refs/heads/"+branch {
			return curPath
		}
	}
	return ""
}

func removeWorktree(repoPath, wtPath string) error {
	return runGit(repoPath, "worktree", "remove", "--force", wtPath)
}

func pullDefault(repoPath, defaultBranch string) error {
	current := runGitQuiet(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if current == defaultBranch {
		return runGit(repoPath, "pull", "--rebase")
	}
	return runGit(repoPath, "fetch", "origin", defaultBranch)
}

// ensureDefaultBranchWindow makes sure the given session has a window
// named after the default branch. If the window already exists, no-op.
// If absent (e.g. session was created with only worktree windows, or the
// default-branch window was deleted), a new window is created at repoPath
// with that name. select-window can then safely target =session:branch.
func ensureDefaultBranchWindow(h *tmuxhost.Client, session, repoPath, defaultBranch string) error {
	out, err := h.Run("list-windows", "-t", "="+session, "-F", "#W")
	if err != nil {
		return err
	}
	for _, w := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if w == defaultBranch {
			return nil
		}
	}
	last := lastWindowIndex(session)
	next := last + 1
	_, err = h.Run("new-window", "-t", fmt.Sprintf("%s:%d", session, next),
		"-c", repoPath, "-n", defaultBranch)
	return err
}

func lastWindowIndex(session string) int {
	h := tmuxhost.New("")
	out, err := h.Run("list-windows", "-t", "="+session, "-F", "#I")
	if err != nil {
		return 0
	}
	max := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		n := 0
		_, _ = fmt.Sscanf(line, "%d", &n)
		if n > max {
			max = n
		}
	}
	return max
}

func getSessionRepoPath(h *tmuxhost.Client, session string) (string, error) {
	out, _ := h.Run("show-option", "-t", session, "-qv", workspace.OptRepoPath)
	return strings.TrimSpace(string(out)), nil
}

// dropPrompts removes fzfstyle's --prompt= arg when we want to use a
// caller-supplied prompt via WithCustomColor or WithQuery. fzf accepts
// repeated flags but the last wins; this is a defensive cleanup so the
// output is canonical.
func dropPrompts(args []string) []string {
	// Currently fzfstyle.Args only emits one --prompt=; just return.
	return args
}

// ============================================================================
// Time-based unique session fallback (unused vestige; keep for compat)
// ============================================================================

var _ = time.Now
var _ = url.Parse

// switchOuterTo was the old in-tool implementation of outer-client
// landing. Lifted to internal/workspace.LandOuter (workspace primitive
// owns workspace-lifecycle tmux operations per DESIGN.md). All call
// sites use workspace.LandOuter directly now.

// interpretPickedRepo extracts the repo name from fzf's picked line.
//
// Empty / whitespace-only picked = fzf become() chain that terminated
// upstream with no output. PickCommand previously fell through to
// runWorkspaceName("", ...) in that case, opening the name picker on
// an empty repo (the "Repo selected popup that needs another M-n to
// close" bug). This helper makes the cancel signal explicit so callers
// can return fzf.ErrCancelled instead of proceeding.
func interpretPickedRepo(picked string) (repo string, cancelled bool) {
	if strings.TrimSpace(picked) == "" {
		return "", true
	}
	return strings.SplitN(picked, "\t", 2)[0], false
}
