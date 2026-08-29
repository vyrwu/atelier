package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// InternalCommand groups host services that external tools can call back
// into when they're written in a language other than Go (or just don't
// want to import atelier's libraries). Go-written tools can import
// internal/popup and internal/state directly for in-process speed.
func InternalCommand() *cobra.Command {
	c := &cobra.Command{
		Use:    "internal",
		Short:  "Internal host services for external tools",
		Hidden: true,
	}
	c.AddCommand(internalEnsureWorkspacePopupCmd())
	c.AddCommand(internalEnsureGlobalPopupCmd())
	c.AddCommand(internalAttachCmd())
	c.AddCommand(internalRespawnCmd())
	c.AddCommand(internalStampStatuslineCmd())
	c.AddCommand(internalStampLastActiveCmd())
	c.AddCommand(internalClipboardCopyCmd())
	c.AddCommand(internalWelcomeCmd())
	return c
}

// welcomeDebounce is how long after a welcome-popup open we suppress another,
// so a burst of client-attached events (reattach) can't stack M-n popups.
const welcomeDebounce = 5

// internalWelcomeCmd is the launcher default screen (WS-3): when the bundled
// runtime has NO workspaces yet, open the M-n intent creator instead of leaving
// the user on a bare launcher shell. Fired from the generated config's
// client-attached hook; a no-op the moment any workspace exists, so it stops
// after the first one is created.
func internalWelcomeCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "welcome",
		Short:  "internal: open the M-n creator when no workspaces exist yet",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			if workspaceCount(h) > 0 {
				return nil // there are workspaces — the launcher resumes one
			}
			// Debounce so a reattach burst doesn't stack popups.
			if ts, _ := h.ShowGlobalOption("@atelier_welcome_ts"); strings.TrimSpace(ts) != "" {
				if last, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64); err == nil {
					if time.Now().Unix()-last < welcomeDebounce {
						return nil
					}
				}
			}
			_ = h.SetGlobalOption("@atelier_welcome_ts", strconv.FormatInt(time.Now().Unix(), 10))
			// Compact, centered, titled floating box framing the M-n intent
			// input. Mirrors the M-n binding's manifest.StyleInput geometry
			// (-b rounded -S fg=colour103 -T "New Workspace" -w60% -h30%, no
			// -y anchor → centered) so the welcome path and the keybind path
			// look identical.
			_, err := h.Run("display-popup", "-b", "rounded", "-S", "fg=colour103",
				"-T", "#[align=centre] New Workspace ", "-w60%", "-h30%",
				"-E", "atelier tools workspaces new")
			return err
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// workspaceCount returns how many atelier-managed workspaces (sessions carrying
// a @workspace_id, excluding popup sessions) currently exist. Reads from
// list-SESSIONS via workspace.ListableSessions — @workspace_id is session-scoped
// and reading it per-window is version-fragile.
func workspaceCount(h *tmuxhost.Client) int {
	n := 0
	for session := range workspace.ListableSessions(h) {
		if !strings.HasPrefix(session, "_") {
			n++
		}
	}
	return n
}

// internalClipboardCopyCmd is the copy-mode-vi yank target. tmux's
// `copy-pipe-and-cancel` invokes this with the selection on stdin;
// we forward it to the first available system-clipboard tool.
//
// Why a Go subcommand instead of inlining `pbcopy` / `wl-copy` in
// the tmux binding: portable detection. Atelier targets Darwin +
// Linux; the right binary depends on the user's session
// (pbcopy on macOS, wl-copy under Wayland, xclip/xsel under X11).
// Inlining a shell `command -v ... && ...` chain in the tmux config
// would be quoting hell and would break for users without
// /bin/sh-compatible defaults. One Go subcommand is cleaner.
//
// Order on Linux: wl-copy (Wayland — the modern default) → xclip
// (X11 most common) → xsel (X11 fallback). On macOS: pbcopy
// (always present). If nothing's found, we silently no-op —
// breaking the user's copy-mode yank with a confusing error is
// worse than a missing clipboard handoff (the OSC 52 set-clipboard
// path still works for many terminals).
func internalClipboardCopyCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "clipboard-copy",
		Short:  "Pipe stdin to the system clipboard (copy-mode-vi yank target)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			tool, args, ok := pickClipboardTool()
			if !ok {
				debuglog.Logf("clipboard-copy: no clipboard tool found on PATH (tried pbcopy / wl-copy / xclip / xsel)")
				// Drain stdin so the upstream pipe doesn't block on a
				// PIPE-write into a closed reader. tmux's copy-pipe
				// finishes either way; the user just gets no clipboard.
				_, _ = io.Copy(io.Discard, os.Stdin)
				return nil
			}
			cmd := exec.Command(tool, args...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stderr // any stderr from the tool surfaces to tmux's run-shell log
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				debuglog.LogErr("clipboard-copy: "+tool, err)
				return nil // best-effort, never break the yank
			}
			return nil
		},
	}
}

// pickClipboardTool returns the first available clipboard utility
// for the current OS + session, in priority order. The boolean is
// false when nothing's available — callers should no-op silently
// rather than error.
func pickClipboardTool() (tool string, args []string, ok bool) {
	if runtime.GOOS == "darwin" {
		if p, err := exec.LookPath("pbcopy"); err == nil {
			return p, nil, true
		}
		return "", nil, false
	}
	// Linux (and *BSD): try Wayland-first, then X11.
	if p, err := exec.LookPath("wl-copy"); err == nil {
		return p, nil, true
	}
	if p, err := exec.LookPath("xclip"); err == nil {
		return p, []string{"-selection", "clipboard"}, true
	}
	if p, err := exec.LookPath("xsel"); err == nil {
		return p, []string{"--clipboard", "--input"}, true
	}
	return "", nil, false
}

// internalStampLastActiveCmd records the named session as the user's
// most recently focused workspace. Wired into the
// `client-session-changed` hook with `#{session_name}` — the
// session the client just SWITCHED TO.
//
// Read by the bundled launcher on next startup to resume work:
// instead of attaching to bare "default", atelier resolves
// last-active from the cache and attaches there. If last-active
// doesn't exist (first launch) or its session can't be restored,
// the launcher falls back to "default" gracefully.
//
// Skips the special "default" session — that's the bundled
// launcher's bootstrap shell, not a "workspace" worth resuming to.
// Without this filter, every M-; popup landing the user back on
// default would overwrite last-active.
func internalStampLastActiveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "stamp-last-active <session-name>",
		Short: "Record session as last-active (client-session-changed hook entry)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			session := strings.TrimSpace(args[0])
			if session == "" || session == "default" {
				return nil
			}
			// Skip popup-backing sessions — those land the client
			// briefly while a tool is open, then return; persisting
			// them as last-active would resume the popup on next
			// launch, not the actual workspace.
			if strings.HasPrefix(session, "_") {
				return nil
			}
			return statestore.SetLastActiveSession(session)
		},
	}
	return c
}

// internalStampStatuslineCmd idempotently injects atelier's
// status-line segments (attention rollup + forge PR badge) into the
// user's window-status formats. Fired once at init time via run-shell.
//
// Why this exists: the prior approach used `set -ag window-status-...`
// (append) every init. tmux's set-ag accumulates, so each re-source of
// atelier's init added another copy of the format. Within minutes of
// dev iteration the format had 18+ copies of `attention --count` and
// the layout drifted (atelier additions interleaved with the user's
// theme). This command STRIPS prior atelier injections from the
// current format value, then re-appends a single canonical pair —
// idempotent across any number of init runs.
//
// We identify atelier injections by the literal `atelier status ` text
// (and a wrapping leading space). The user's theme content is
// preserved verbatim.
func internalStampStatuslineCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "stamp-statusline",
		Short: "Idempotently inject atelier's status-line segments (attention, forge)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			return stampStatusline(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// Status emitter names — single source of truth for the public
// embedding API surface. Changing these names requires migrating
// every user's tmux.conf, so they're effectively frozen.
//
// Referenced by:
//   - atelierStatuslineRe (regex stripping prior injections)
//   - attentionSegment (canonical segment builder)
//   - status.go (cobra subcommand `Use` fields)
//
// Adding/renaming an emitter here also updates atelierStatuslineRe (which
// strips prior injections) since the regex is built from this const.
const (
	attentionEmitter = "attention"
)

// atelierStatuslineRe matches atelier's `#(...)` injection in a
// window-status-format value, optionally preceded by whitespace, so we can
// strip prior copies before re-adding. Built from the emitter const so
// renaming the emitter touches one location.
var atelierStatuslineRe = regexp.MustCompile(
	`\s*#\(atelier status ` + attentionEmitter + `[^)]*\)`)

// attentionSegment is the canonical atelier addition — the global ⏺N
// attention rollup. Built from the emitter const so the regex above and the
// cobra subcommand `Use` field can't drift apart.
//
// In the bundled theme the rollup lives in status-left; this segment is the
// PLUGIN-mode delivery, injected non-destructively into the user's existing
// window-status-current-format (after #W) so plugin users still get the
// rollup without atelier owning their whole bar.
func attentionSegment() string {
	return `#(atelier status ` + attentionEmitter + ` count)`
}

func stampStatusline(h *tmuxhost.Client) error {
	// Only the current format gets the segment — inactive windows render
	// nothing (window-status-format is empty), so the bar reflects only
	// the focused workspace. The ⏺N rollup is global and covers background
	// windows that need the user.
	for _, opt := range []struct {
		name     string
		segments []string
	}{
		{
			name:     "window-status-current-format",
			segments: []string{attentionSegment()},
		},
	} {
		curBytes, err := h.Run("show-options", "-gv", opt.name)
		current := strings.TrimRight(string(curBytes), "\n")
		if err != nil {
			current = ""
		}
		cleaned := atelierStatuslineRe.ReplaceAllString(current, "")
		want := injectAfterWindowName(cleaned, strings.Join(opt.segments, ""))
		debuglog.Logf("stamp-statusline %s\n  before: %q\n  cleaned: %q\n  after:   %q",
			opt.name, current, cleaned, want)
		if _, err := h.Run("set-option", "-g", opt.name, want); err != nil {
			return fmt.Errorf("set-option %s: %w", opt.name, err)
		}
	}
	return nil
}

// injectAfterWindowName inserts atelier's segments AFTER the window
// name placeholder (#W) AND the powerline color-transition block that
// typically follows it. The transition is the `#[fg=X]#[bg=Y]` pair
// that draws the powerline arrow exiting the window-name segment;
// injecting BEFORE it would land the icon inside the colored box,
// before the arrow head, which looks broken.
//
// Heuristic: consume `#W`, then trailing whitespace, then any number
// of `#[...]` color directives. Whatever's left (user helpers, other
// formats) gets pushed after our injection.
//
// If the format has no `#W` placeholder, RETURN THE FORMAT UNCHANGED.
// The prior behavior was to append the segment, but that produces a
// free-floating icon — for every inactive window — when the user's
// window-status-format is empty or doesn't include the window name.
// Skipping injection lets the user / atelier theme fix the
// format (add #W) and re-source; doctor flags the no-anchor case.
//
// Examples (atelier segments simplified to <X>):
//
//	`#[bg=blue] #W #[fg=blue]#[bg=red]#(stuff)`
//	→ `#[bg=blue] #W #[fg=blue]#[bg=red]<X>#(stuff)`
//
//	`#W #(stuff)` → `#W <X>#(stuff)`
//	`#I: only`   → `#I: only`      (no #W → no inject)
//	``           → ``              (no #W → no inject)
func injectAfterWindowName(format, injection string) string {
	if injection == "" {
		return format
	}
	loc := injectAnchorRe.FindStringIndex(format)
	if loc == nil {
		return format
	}
	return format[:loc[1]] + injection + format[loc[1]:]
}

// injectAnchorRe matches `#W` followed by anything that's "between
// segments" in a typical status-bar format: whitespace, `#[...]`
// color directives, and Powerline glyphs (U+E0A0–U+E0FF — the
// Private Use Area range Nerd Fonts use for arrows/separators).
// The atelier segments are inserted at the END of the match, which
// is BEFORE the next content like `#(...)` or `#{...}` placeholders.
//
// Without consuming the powerline glyph, atelier's icon lands BEFORE
// the arrow exit — inside the colored window-name box rather than in
// the next segment where the user actually expects it.
var injectAnchorRe = regexp.MustCompile(
	`#W(?:\s|#\[[^\]]*\]|[\x{e0a0}-\x{e0ff}])*`)

func internalEnsureWorkspacePopupCmd() *cobra.Command {
	var (
		tool, cmdLine, sessionID, windowID, cwd, socket string
	)
	c := &cobra.Command{
		Use:   "ensure-workspace-popup",
		Short: "Ensure a workspace-scoped backing popup session exists",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			if sessionID == "" || windowID == "" {
				s, err := state.Capture(h)
				if err != nil {
					return err
				}
				sessionID, windowID = s.OuterSession, s.OuterWindow
			}
			spec := &popup.WorkspaceScoped{Tool: tool, DefaultCmd: cmdLine}
			if err := spec.Ensure(h, sessionID, windowID, cwd); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), spec.SessionName(sessionID, windowID))
			return nil
		},
	}
	c.Flags().StringVar(&tool, "tool", "", "tool name (becomes session-name suffix)")
	c.Flags().StringVar(&cmdLine, "cmd", "$SHELL", "shell command run inside the popup session")
	c.Flags().StringVar(&sessionID, "session", "", "parent session id (default: from state)")
	c.Flags().StringVar(&windowID, "window", "", "parent window id (default: from state)")
	c.Flags().StringVar(&cwd, "cwd", "", "initial working directory")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	_ = c.MarkFlagRequired("tool")
	return c
}

func internalEnsureGlobalPopupCmd() *cobra.Command {
	var (
		tool, cmdLine, socket string
	)
	c := &cobra.Command{
		Use:   "ensure-global-popup",
		Short: "Ensure a session-global backing popup session exists",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			spec := &popup.SessionGlobal{Tool: tool, DefaultCmd: cmdLine}
			if err := spec.Ensure(h); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), spec.SessionName())
			return nil
		},
	}
	c.Flags().StringVar(&tool, "tool", "", "tool name (becomes session-name suffix)")
	c.Flags().StringVar(&cmdLine, "cmd", "$SHELL", "shell command run inside the popup session")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	_ = c.MarkFlagRequired("tool")
	return c
}

func internalAttachCmd() *cobra.Command {
	var (
		session, socket string
	)
	c := &cobra.Command{
		Use:   "attach",
		Short: "exec() tmux attach-session -t <session>",
		RunE: func(_ *cobra.Command, _ []string) error {
			return tmuxhost.New(socket).Attach(session)
		},
	}
	c.Flags().StringVar(&session, "session", "", "session name to attach to")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	_ = c.MarkFlagRequired("session")
	return c
}

func internalRespawnCmd() *cobra.Command {
	var (
		tool, cmdLine, socket string
	)
	c := &cobra.Command{
		Use:   "respawn-global-popup",
		Short: "Kill + recreate a session-global backing popup (context switches)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			spec := &popup.SessionGlobal{Tool: tool}
			return spec.Respawn(h, cmdLine)
		},
	}
	c.Flags().StringVar(&tool, "tool", "", "tool name")
	c.Flags().StringVar(&cmdLine, "cmd", "", "shell command to run in the new session")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	_ = c.MarkFlagRequired("tool")
	_ = c.MarkFlagRequired("cmd")
	return c
}
