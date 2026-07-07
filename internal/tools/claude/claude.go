// Package claude is atelier's per-window Claude Code popup — bash-exact
// port of show_claude_popup + claude_start + tmux_notify_attention +
// tmux_generate_recap.
//
// Per-window state read from the outer (workspace) tmux window:
//
//   - @claude_prompt — passed to claude as the initial prompt (one-shot)
//   - @claude_workspace_kind — "worktree" (default) | "multi-repo"
//     When "multi-repo", DefaultMultiRepoSystemPrompt is appended via
//     --append-system-prompt.
//
// Both options are CLEARED on first read so the prompt can't be replayed
// when the popup session is recreated on the same parent window.
//
// notify-attention is the integration point for Claude's Stop hook:
//
//   - If the user is currently viewing the popup, @needs_attention is NOT
//     set (they're already looking).
//   - Recap is always refreshed in the background, reading the transcript
//     path from the hook's stdin JSON payload.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/claudegen"
	"github.com/vyrwu/atelier/internal/claudesettings"
	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// Metadata keys + their derived tmux window option names. The
// claude tool owns its `ai.*` metadata namespace; core never
// inspects these keys. Each `Meta*` constant is the canonical
// metadata key; each `Opt*` constant is its tmux option name
// (`@ai_<field>` via statestore.MetadataKeyToOptionName).
//
// Persistence flow:
//
//  1. The tool stamps the option on a window via `tmux set-option @ai_*`.
//  2. The tool mirrors it into statestore via PersistWindowMetadata
//     with the same key — survives tmux server restarts.
//  3. Restore re-stamps the option from Metadata on rehydrate.
const (
	MetaPrompt          = "ai.prompt"
	MetaWorkspaceKind   = "ai.workspace_kind"
	MetaActiveSessionID = "ai.active_session_id"

	WorkspaceKindMultiRepo = "multi-repo"
	WorkspaceKindWorktree  = "worktree"
)

// Tmux option names — derived once at package load via the canonical
// dots→underscores translation. Plugin code uses these for
// set/get/unset-window-option calls.
var (
	OptPrompt          = statestore.MetadataKeyToOptionName(MetaPrompt)
	OptWorkspaceKind   = statestore.MetadataKeyToOptionName(MetaWorkspaceKind)
	OptActiveSessionID = statestore.MetadataKeyToOptionName(MetaActiveSessionID)
)

var Spec = &popup.WorkspaceScoped{
	Tool:        "claude",
	DefaultCmd:  "claude",
	Description: "Per-window Claude Code popup (bash-exact)",
}

// OpenCommand: show_claude_popup port.
//
// Reads parent session/window via popup.ResolveParentContext (env →
// atelier globals → current pane). Picks up @claude_prompt +
// @claude_workspace_kind from the parent window, clears them
// (one-shot), builds the launch command per claude_start, then delegates
// to popup.OpenWorkspaceScopedWithCmd for ensure + style + attach.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the Claude Code popup (bash-exact show_claude_popup port)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			return popup.OpenWorkspaceScopedWithCmd(h, Spec, func(ctx popup.ParentContext) (string, error) {
				debuglog.Logf("claude.Open: parentSession=%q parentWindow=%q (from env? %v)",
					ctx.SessionID, ctx.WindowID,
					os.Getenv("TMUX_PARENT_SESSION_ID") != "")

				prompt, _ := h.GetWindowOption(ctx.WindowID, OptPrompt)
				kind, _ := h.GetWindowOption(ctx.WindowID, OptWorkspaceKind)
				// Resume signal — set by notify-attention every time a
				// Claude task completes. Durable (NOT one-shot like
				// @claude_prompt) so it survives close+reopen and tmux
				// restart.
				resumeID, _ := h.GetWindowOption(ctx.WindowID, OptActiveSessionID)
				// Validate: if the transcript file was deleted (user
				// nuked ~/.claude/projects/, Claude reinstall, etc.),
				// drop the stale id so we start fresh instead of
				// asking Claude to --resume a missing session (which
				// would error out and leave the popup in a bad state).
				if resumeID != "" && !transcriptExists(resumeID) {
					debuglog.Logf("claude.Open: stale @claude_active_session_id=%q (transcript missing) — clearing", resumeID)
					_ = h.UnsetWindowOption(ctx.WindowID, OptActiveSessionID)
					_ = workspace.PersistWindowMetadata(h, ctx.WindowID, MetaActiveSessionID, "")
					resumeID = ""
				}

				// One-shot: clear the prompt + kind options so they
				// can't be replayed. @claude_active_session_id stays —
				// it's the persistent "we have a conversation here"
				// pointer.
				_ = h.UnsetWindowOption(ctx.WindowID, OptPrompt)
				_ = h.UnsetWindowOption(ctx.WindowID, OptWorkspaceKind)

				cfg, _ := LoadConfig()

				// Ensure atelier's Claude settings file exists and pass it
				// via `--settings`. Claude CLI layers our settings on top of
				// the user's `~/.config/claude/settings.json`, so user hooks
				// (agent-deck etc.) still fire alongside atelier's Stop hook
				// (which routes to `atelier tools claude notify-attention`).
				settingsPath, settingsErr := claudesettings.Ensure()
				if settingsErr != nil {
					debuglog.LogErr("claudesettings.Ensure", settingsErr)
				}
				return buildClaudeStartCmd(prompt, kind, cfg.MultiRepoSystemPrompt, settingsPath, resumeID), nil
			})
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func SetPromptCommand() *cobra.Command {
	var (
		windowID, prompt, kind, socket string
	)
	c := &cobra.Command{
		Use:   "set-prompt",
		Short: "Set the initial prompt for the next Claude popup open on a window",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			if windowID == "" {
				s, err := state.Capture(h)
				if err != nil {
					return err
				}
				windowID = s.OuterWindow
			}
			if windowID == "" {
				return fmt.Errorf("--window required (no outer window in state)")
			}
			if prompt == "" {
				if err := h.UnsetWindowOption(windowID, OptPrompt); err != nil {
					return err
				}
			} else if err := h.SetWindowOption(windowID, OptPrompt, prompt); err != nil {
				return err
			}
			if kind != "" {
				if err := h.SetWindowOption(windowID, OptWorkspaceKind, kind); err != nil {
					return err
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&windowID, "window", "", "tmux window id (default: outer window from state)")
	c.Flags().StringVar(&prompt, "prompt", "", "initial prompt (empty clears it)")
	c.Flags().StringVar(&kind, "kind", "", "workspace kind hint (worktree | multi-repo)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func RecapCommand() *cobra.Command {
	var (
		windowID, socket, project string
	)
	c := &cobra.Command{
		Use:   "recap",
		Short: "Ask Claude (haiku) to summarize the latest transcript into @attention_recap on the window",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			if windowID == "" {
				s, err := state.Capture(h)
				if err != nil {
					return err
				}
				windowID = s.OuterWindow
			}
			if windowID == "" {
				return fmt.Errorf("--window required")
			}
			if project == "" {
				if w, err := workspace.Info(h, ""); err == nil {
					project = w.Cwd
				}
			}
			recap, err := LatestRecap(project)
			if err != nil {
				return err
			}
			if recap == "" {
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), recap)
			return workspace.SetRecap(h, windowID, recap)
		},
	}
	c.Flags().StringVar(&windowID, "window", "", "tmux window id (default: outer window)")
	c.Flags().StringVar(&project, "project", "", "claude project root (default: workspace cwd)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// NotifyAttentionCommand: tmux_notify_attention port.
//
// On the Claude Stop hook:
//   - Resolve target outer window (from _atelier_claude_*/_claudepop_*
//     session name if inside a popup; else current window).
//   - Skip @needs_attention if the popup session has attached clients
//     (user is already viewing).
//   - Always background-refresh @attention_recap from stdin payload's
//     transcript_path.
func NotifyAttentionCommand() *cobra.Command {
	var (
		windowID, socket string
	)
	c := &cobra.Command{
		Use:   "notify-attention",
		Short: "Flag the outer window as needing attention; refresh @attention_recap (Claude Stop hook entry)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)

			session, _ := h.DisplayMessage("#{session_name}")
			attached := "0"
			target := windowID

			switch {
			case strings.HasPrefix(session, "_atelier_claude_") || strings.HasPrefix(session, "_claudepop_"):
				attached, _ = h.DisplayMessageAt(session, "#{session_attached}")
				rest := strings.TrimPrefix(strings.TrimPrefix(session, "_atelier_claude_"), "_claudepop_")
				if i := strings.LastIndex(rest, "_"); i >= 0 {
					target = "@" + rest[i+1:]
				}
			default:
				if target == "" {
					if v := os.Getenv("TMUX_PARENT_WINDOW"); v != "" {
						target = v
					} else if v := os.Getenv("TMUX_PARENT_WINDOW_ID"); v != "" {
						target = v
					} else if v, _ := h.ShowGlobalOption("@atelier_outer_window"); v != "" {
						// Fallback to atelier-tracked outer window; set
						// by the M-; root binding before popup spawn.
						target = v
					} else if v, err := h.DisplayMessage("#{window_id}"); err == nil {
						target = v
					}
				}
			}
			if target == "" {
				return fmt.Errorf("notify-attention: could not resolve target window")
			}
			target = ensurePrefix(target, "@")
			debuglog.Logf("notify-attention: session=%q attached=%q target=%q", session, attached, target)

			if attached == "" || attached == "0" {
				_ = workspace.SetAttention(h, target, true)
			}

			// Read hook payload from stdin (may be empty).
			payload, _ := io.ReadAll(os.Stdin)
			if len(payload) == 0 {
				return nil
			}

			// Record the active Claude session ID per window so we can
			// later `claude --resume <id>` after a tmux server restart.
			// The hook payload's `transcript_path` looks like
			// ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl —
			// the filename stem IS the session ID.
			var probe struct {
				TranscriptPath string `json:"transcript_path"`
			}
			if err := json.Unmarshal(payload, &probe); err == nil && probe.TranscriptPath != "" {
				if sid := claudeSessionIDFromPath(probe.TranscriptPath); sid != "" {
					_ = h.SetWindowOption(target, OptActiveSessionID, sid)
					_ = workspace.PersistWindowMetadata(h, target, MetaActiveSessionID, sid)
				}
			}
			// Spawn a detached background process to do the recap so the hook
			// returns instantly (bash uses `(... ) &`).
			//
			// We re-invoke OURSELVES (atelier-claude) directly — passing
			// `tools claude _recap-from-hook` would only work if `self` were
			// the main `atelier` dispatcher; os.Executable() returns this
			// binary's path (atelier-claude), so we must use atelier-claude's
			// OWN subcommand name.
			self, err := os.Executable()
			if err != nil {
				self = "atelier-claude"
			}
			cmd := exec.Command(self, "_recap-from-hook", "--window", target)
			cmd.Stdin = strings.NewReader(string(payload))
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Start(); err != nil {
				return err
			}
			// Release without waiting.
			_ = cmd.Process.Release()
			return nil
		},
	}
	c.Flags().StringVar(&windowID, "window", "", "tmux window id (default: parent of popup or current)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// RecapFromHookCommand: tmux_generate_recap port. Reads the Claude hook
// JSON payload from stdin, extracts the transcript_path, asks Claude
// (haiku) for the ≤75-char recap line, and stores it as @attention_recap.
func RecapFromHookCommand() *cobra.Command {
	var (
		windowID, socket string
	)
	c := &cobra.Command{
		Use:    "_recap-from-hook",
		Short:  "internal: generate @attention_recap from a Claude hook payload on stdin",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if windowID == "" {
				return fmt.Errorf("--window required")
			}
			payload, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			var hook struct {
				TranscriptPath string `json:"transcript_path"`
			}
			var context string
			if err := json.Unmarshal(payload, &hook); err == nil && hook.TranscriptPath != "" {
				if data, err := os.ReadFile(hook.TranscriptPath); err == nil {
					context = tailNLines(string(data), 100)
				}
			}
			if context == "" {
				context = string(payload)
			}
			if strings.TrimSpace(context) == "" {
				return nil
			}
			cfg, _ := LoadConfig()
			gen := claudegen.New()
			if cfg.RecapModel != "" {
				gen.Model = cfg.RecapModel
			}
			out, err := gen.RunWithSystemPrompt(cfg.RecapSystemPrompt, context)
			if err != nil {
				return err
			}
			recap := truncateLine(out, RecapMaxRunes)
			if recap == "" {
				return nil
			}
			h := tmuxhost.New(socket)
			return workspace.SetRecap(h, windowID, recap)
		},
	}
	c.Flags().StringVar(&windowID, "window", "", "target tmux window id")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// buildClaudeStartCmd assembles the claude command line as claude_start does:
//
//	resume          : claude --settings <atelier.json> --resume <session-id>
//	multi-repo + prompt: claude --settings <atelier.json> --append-system-prompt <sys> <prompt>
//	worktree   + prompt: claude --settings <atelier.json> <prompt>
//	no prompt          : claude --settings <atelier.json>
//
// `--settings` is added only when settingsPath is non-empty (i.e.
// claudesettings.Ensure succeeded). Claude CLI layers atelier's settings
// on top of the user's, so the Stop hook routing to
// `atelier tools claude notify-attention` fires alongside any user hooks.
//
// Resume precedence: if `resumeSessionID` is non-empty and `prompt` is
// empty, append `--resume <id>` so Claude picks up the previous
// conversation. An explicit prompt always overrides resume — the user
// asked for a fresh start.
func buildClaudeStartCmd(prompt, kind, multiRepoSys, settingsPath, resumeSessionID string) string {
	settings := ""
	if settingsPath != "" {
		settings = "--settings " + shellQuote(settingsPath) + " "
	}
	if prompt == "" {
		if resumeSessionID != "" {
			return "claude " + settings + "--resume " + shellQuote(resumeSessionID)
		}
		return "claude " + settings
	}
	if kind == WorkspaceKindMultiRepo {
		return fmt.Sprintf("claude %s--append-system-prompt %s %s",
			settings, shellQuote(multiRepoSys), shellQuote(prompt))
	}
	return fmt.Sprintf("claude %s%s", settings, shellQuote(prompt))
}

// claudeSessionIDFromPath extracts the Claude session UUID from a
// transcript path like ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl.
// Returns "" if the filename doesn't look like a session transcript.
func claudeSessionIDFromPath(transcriptPath string) string {
	base := filepath.Base(transcriptPath)
	const suffix = ".jsonl"
	if !strings.HasSuffix(base, suffix) {
		return ""
	}
	return strings.TrimSuffix(base, suffix)
}

// transcriptExists returns true if Claude has a saved transcript file
// for the given session id. Claude stores transcripts at
// ~/.claude/projects/<encoded-cwd>/<sessionID>.jsonl — we don't know
// the encoded cwd at this point, so we glob over all projects.
//
// Used by OpenCommand to detect a STALE @claude_active_session_id
// (transcript file deleted by the user — `rm -rf ~/.claude/projects/X`,
// `claude` reinstall, etc.). Without this check, --resume on a missing
// id makes Claude CLI error out and the popup shows a failure instead
// of falling back to a fresh start.
func transcriptExists(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude/projects/*", sessionID+".jsonl"))
	if err != nil {
		return false
	}
	return len(matches) > 0
}

func shellQuote(s string) string {
	// Use single quotes, escape embedded single quotes the POSIX way.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func ensurePrefix(s, prefix string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, prefix) {
		return s
	}
	return prefix + s
}

func tailNLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// truncateLine returns a single-line, length-bounded recap. It strips
// surrounding whitespace, drops everything after the first newline, and
// hard-caps at max runes. When truncation actually happens, the last
// rune is replaced with `…` so the picker shows a visible "this got
// cut" marker rather than silently presenting a half-thought.
//
// Defensive layer behind DefaultRecapSystemPrompt's advisory — Claude
// occasionally ignores the length cap, especially with sonnet. The cap
// matches RecapMaxRunes (single source of truth).
func truncateLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// Strip surrounding straight/curly quotes the model sometimes emits
	// despite the prompt.
	s = strings.Trim(s, `"'“”‘’`)
	// Drop a leading "Recap:"-style label if the model added one.
	for _, prefix := range []string{"Recap:", "Summary:", "recap:", "summary:"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	// Reserve last rune for ellipsis.
	return string(runes[:max-1]) + "…"
}

// LatestRecap finds the most-recent Claude transcript for the given project
// directory and asks Claude to summarize it. Returns "" if no transcript.
func LatestRecap(projectDir string) (string, error) {
	transcript, err := findLatestTranscript(projectDir)
	if err != nil || transcript == "" {
		return "", err
	}
	cfg, _ := LoadConfig()
	gen := claudegen.New()
	if cfg.RecapModel != "" {
		gen.Model = cfg.RecapModel
	}
	if data, err := os.ReadFile(transcript); err == nil {
		ctx := tailNLines(string(data), 100)
		out, err := gen.RunWithSystemPrompt(cfg.RecapSystemPrompt, ctx)
		if err != nil {
			return "", err
		}
		return truncateLine(out, RecapMaxRunes), nil
	}
	return gen.RecapFromTranscript(transcript)
}

func findLatestTranscript(projectDir string) (string, error) {
	if projectDir == "" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	slug := strings.ReplaceAll(strings.TrimPrefix(projectDir, "/"), "/", "-")
	dir := filepath.Join(home, ".claude", "projects", slug)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var latest os.DirEntry
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if latest == nil {
			latest = e
			continue
		}
		li, _ := latest.Info()
		ei, _ := e.Info()
		if ei.ModTime().After(li.ModTime()) {
			latest = e
		}
	}
	if latest == nil {
		return "", nil
	}
	return filepath.Join(dir, latest.Name()), nil
}
