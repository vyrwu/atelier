package workspaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/initgen"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/repoindex"
	"github.com/vyrwu/atelier/internal/spinner"
	"github.com/vyrwu/atelier/internal/textprompt"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// ============================================================================
// M-n — intent-first workspace creation
// ============================================================================
//
// "Instead of picking repos, you just enter the task at hand. Atelier does the
// rest." The user types WHAT they're doing; atelier names the workspace (title
// + slug + optional tag), AI-selects which repos in the code index the intent
// touches, materializes a worktree per repo symlinked into the workspace root,
// and opens the driver agent there with the intent as its first prompt.

// NewCommand opens the intent prompt (M-n). With no AI integration it still
// works — the slug/title fall back to a deterministic slugify of the intent and
// no repos are pre-selected (the agent adds them via the control surface).
func NewCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "new",
		Short: "Create a workspace from an intent (M-n)",
		RunE: func(_ *cobra.Command, args []string) error {
			intent := strings.TrimSpace(strings.Join(args, " "))
			if intent == "" {
				var cancelled bool
				intent, cancelled = promptIntent()
				if cancelled {
					return fzf.ErrCancelled
				}
				if intent == "" {
					return fzf.ErrCancelled
				}
			}
			// Test hook: e2e tests need synchronous create so assertions don't
			// race the deferred popup. Otherwise run inline with a spinner box
			// (the M-n popup hosts it — same pattern as the old auto flow).
			return runCreate(tmuxhost.New(socket), intent)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// promptIntent shows the single free-text INPUT field ("what are we doing
// today?") — a rectangular prompt box, not a fuzzy picker: this is text entry,
// not selection. Returns (intent, cancelled); Esc / Ctrl-C cancel.
func promptIntent() (string, bool) {
	intent, err := textprompt.Read(textprompt.Options{
		Title:  "What are we doing today?",
		Prompt: "> ",
		Accent: "35", // green
		Footer: "Enter · create   Esc · cancel   ⌃W · del word   ⌃U · clear",
	})
	if err != nil {
		return "", true
	}
	return strings.TrimSpace(intent), false
}

// runCreate is the full intent→workspace build. Runs inside a spinner box.
func runCreate(h *tmuxhost.Client, intent string) error {
	var (
		title, slug, tag string
		repos            []repoindex.Repo
		newSid, newWid   string
	)
	sp := spinner.NewBox("Creating workspace...")
	err := sp.Run(func() error {
		sp.SetStatus("Naming the workspace...")
		plan := planWorkspace(h, intent)
		title, slug, tag, repos = plan.title, plan.slug, plan.tag, plan.repos

		session := workspace.SessionName(slug)
		// Dedup: an existing workspace with this slug → land on it.
		if has, _ := h.HasSession(session); has {
			sid, _ := h.DisplayMessageAt(session, "#{session_id}")
			wid, _ := h.DisplayMessageAt(session+":1", "#{window_id}")
			newSid, newWid = strings.TrimSpace(sid), strings.TrimSpace(wid)
			return errWorkspaceExists
		}

		root := workspace.WorkspaceRootFor(slug)
		if err := workspace.EnsureWorkspaceRoot(root); err != nil {
			return err
		}
		if _, err := workspace.EnsureSession(h, session, root, workspaceWindowName(slug)); err != nil {
			return err
		}
		if err := workspace.StampWorkspaceIdentity(h, session, session, title, intent, root); err != nil {
			return err
		}
		if tag != "" {
			_ = workspace.SetTag(h, session, tag)
		}
		// Materialize a worktree per AI-selected repo, symlinked into the root.
		for _, r := range repos {
			sp.SetStatus(fmt.Sprintf("Adding %s...", r.Slug))
			branch := slug
			wtPath := filepath.Join(workspaceWorktreeRoot(), r.Slug, branch)
			if _, err := workspace.AddWorktree(h, session, root, r.Path, r.Slug, branch, wtPath); err != nil {
				debuglog.LogErr("workspaces.new: AddWorktree "+r.Slug, err)
			}
		}
		sid, _ := h.DisplayMessageAt(session, "#{session_id}")
		wid, _ := h.DisplayMessageAt(session+":1", "#{window_id}")
		newSid, newWid = strings.TrimSpace(sid), strings.TrimSpace(wid)
		// Queue the intent as the driver agent's first prompt.
		if ai := integration.Active().AI; ai != nil && newWid != "" {
			_ = ai.SetPrompt(h, newWid, intent, "")
		}
		return nil
	})
	session := workspace.SessionName(slug)
	if err != nil && err != errWorkspaceExists {
		_, _ = h.Run("display-message", fmt.Sprintf("✗ workspace create failed: %v", err))
		return err
	}

	// Queue the agent popup BEFORE LandOuter (LandOuter's detachStalePopups
	// would otherwise SIGHUP a popup we queue after). Skipped on test sockets.
	if err != errWorkspaceExists && newSid != "" && newWid != "" {
		queueAgentOpen(h, newSid, newWid)
	}
	return workspace.LandOuter(h, "="+session, "="+session+":1")
}

// queueAgentOpen defers opening the driver agent popup on the outer client so
// it fires after LandOuter has switched the user onto the new workspace.
func queueAgentOpen(h *tmuxhost.Client, sid, wid string) {
	if agentAutoOpenSkipped() || integration.Active().AI == nil {
		return
	}
	sidNum := strings.TrimPrefix(sid, "$")
	widNum := strings.TrimPrefix(wid, "@")
	outerClient, _ := h.ShowGlobalOption("@atelier_outer_client")
	clientArg := ""
	if outerClient != "" {
		clientArg = fmt.Sprintf(" -c '%s'", outerClient)
	}
	popupCmd := fmt.Sprintf(
		"sleep 0.15 && tmux display-popup%s %s -e TMUX_PARENT_SESSION_ID=%s -e TMUX_PARENT_WINDOW_ID=%s -E '%s'",
		clientArg, initgen.PopupOptions(manifest.StyleFull, "Claude Code", false),
		sidNum, widNum, dispatch.CoreCmd("ai", "open"))
	_, _ = h.Run("run-shell", "-b", popupCmd)
}

// errWorkspaceExists signals runCreate that the slug maps to a live workspace;
// the caller lands on it rather than rebuilding.
var errWorkspaceExists = fmt.Errorf("workspace already exists")

// workspacePlan is the resolved naming + repo selection for an intent.
type workspacePlan struct {
	title, slug, tag string
	repos            []repoindex.Repo
}

// planWorkspace derives the title, slug, tag, and repo set for an intent. Uses
// the active AI integration when present (feeding it the repo index so it can
// name the repos the intent touches); falls back to a deterministic slugify +
// no repos when no AI is configured.
func planWorkspace(h *tmuxhost.Client, intent string) workspacePlan {
	index, _ := repoindex.Scan(workspaceCodeRoot())
	ai := integration.Active().AI
	if ai == nil {
		title, slug := fallbackNaming(intent)
		return workspacePlan{title: title, slug: slug}
	}
	autoTag := workspaceAutoTagEnabled()
	var existingTags []string
	if autoTag {
		existingTags = collectTags(h)
	}
	raw, err := ai.GenerateName(context.Background(), workspaceNamingSysPrompt,
		composeCreationIntent(intent, index, existingTags))
	if err != nil {
		debuglog.LogErr("workspaces.new: GenerateName", err)
		title, slug := fallbackNaming(intent)
		return workspacePlan{title: title, slug: slug}
	}
	title, slug, tag, repoNames := parseWorkspacePlan(raw)
	if slug == "" {
		title, slug = fallbackNaming(intent)
	}
	if !autoTag {
		tag = ""
	}
	matched, unmatched := repoindex.Match(index, repoNames)
	if len(unmatched) > 0 {
		debuglog.Logf("workspaces.new: AI proposed unknown repos %v (ignored)", unmatched)
	}
	return workspacePlan{title: title, slug: slug, tag: tag, repos: matched}
}

// workspaceWindowName is the tmux window name for the driver window (cosmetic —
// the picker renders the title). The last slug segment.
func workspaceWindowName(slug string) string {
	if i := strings.LastIndexByte(slug, '/'); i >= 0 {
		slug = slug[i+1:]
	}
	if slug == "" {
		return "agent"
	}
	return slug
}

// workspaceAutoTagEnabled reports whether creation should ask the AI for a
// grouping tag. ATELIER_AUTO_TAG overrides; else [workspaces] auto_tag (true).
func workspaceAutoTagEnabled() bool {
	if v := os.Getenv("ATELIER_AUTO_TAG"); v != "" {
		return v != "0" && !strings.EqualFold(v, "false")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return true
	}
	return cfg.AutoTag
}

// BuildCommand is retained for tests / non-interactive creation:
// `atelier tools workspaces _build --intent="..."` runs the build synchronously.
func BuildCommand() *cobra.Command {
	var intent, socket string
	c := &cobra.Command{
		Use:    "_build",
		Short:  "internal: synchronous workspace build from an intent (tests)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(intent) == "" {
				return fmt.Errorf("_build: --intent required")
			}
			return runCreate(tmuxhost.New(socket), intent)
		},
	}
	c.Flags().StringVar(&intent, "intent", "", "workspace intent text")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// NameCommand is a hidden non-interactive naming probe used by tests to check
// the plan parser end-to-end against the active AI adapter.
func NameCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_name <intent>",
		Short:  "internal: print the derived workspace plan (title/slug/tag/repos)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan := planWorkspace(tmuxhost.New(socket), strings.Join(args, " "))
			names := make([]string, 0, len(plan.repos))
			for _, r := range plan.repos {
				names = append(names, r.Slug)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", plan.title, plan.slug, plan.tag, strings.Join(names, ","))
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
