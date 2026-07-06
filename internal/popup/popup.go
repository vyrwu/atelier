// Package popup gives every popup-spawning tool a single shape so they
// stay consistent and testable.
//
// Two lifecycles are supported:
//
//   - WorkspaceScoped — one popup session per parent tmux window. Backing
//     session name: "_atelier_<tool>_<parent_sid>_<parent_wid>". Dies when
//     the parent window dies.
//
//   - SessionGlobal — a single backing session shared across the entire
//     tmux server. Backing session name: "_atelier_<tool>". Used by k9s,
//     pgcli, pgcenter — tools where one running instance covers all work.
package popup

import (
	"fmt"
	"os"
	"strings"
)

// SessionPrefix is the common prefix for every atelier-managed popup
// session. `atelier popup cleanup` only touches sessions starting with this.
const SessionPrefix = "_atelier"

// Client is the minimal tmux surface popup helpers need.
//
// *tmuxhost.Client from the host satisfies this. Tests substitute a
// recorder. Extracted as an interface so the package can offer pure
// unit-testable helpers (ApplyStyle, ResolveParentContext) without
// requiring a real tmux server.
type Client interface {
	Run(args ...string) ([]byte, error)
	HasSession(name string) (bool, error)
	ShowGlobalOption(name string) (string, error)
	DisplayMessage(format string) (string, error)
	DisplayMessageAt(target, format string) (string, error)
	NewSessionWithCommand(name, shellCmd string) error
	KillSession(name string) error
	Attach(name string) error
}

// ApplyStyle stamps the canonical atelier popup-session options on
// `session`. Idempotent. Existed as a free function because five tools
// (claude/lazygit/k8s/pg/popupshell) had copy-pasted the exact same
// 8-line block. Changes to popup style (e.g., mouse off, new tmux 3.6
// defaults) now land here once instead of in every tool.
//
// Options stamped:
//   - key-table popup    : so the popup-table bindings (M-; / M-n / M-s)
//     fire inside the popup
//   - status off         : no statusline inside the popup
//   - prefix None        : disable normal tmux prefix; popup-table is
//   - prefix2 None         the only routing
//   - aggressive-resize on : popup contents resize with the popup window
//
// Returns the first non-nil error from set-option. Most tools ignore
// this (style is decorative); k8s/pg surface the error because their
// setup flow depends on the options actually taking effect before
// downstream commands run.
func ApplyStyle(h Client, session string) error {
	for _, opt := range [][2]string{
		{"key-table", "popup"},
		{"status", "off"},
		{"prefix", "None"},
		{"prefix2", "None"},
	} {
		if _, err := h.Run("set-option", "-s", "-t", session, opt[0], opt[1]); err != nil {
			return fmt.Errorf("apply popup style %s=%s: %w", opt[0], opt[1], err)
		}
	}
	if _, err := h.Run("set-option", "-g", "-t", session, "aggressive-resize", "on"); err != nil {
		return fmt.Errorf("apply popup style aggressive-resize=on: %w", err)
	}
	return nil
}

// ParentContext is the popup's parent (workspace) coordinates — the
// tmux session/window the popup belongs to and the cwd it should open
// in. Resolved by ResolveParentContext from binding env vars + atelier
// globals + tmux's current state, in that priority order.
type ParentContext struct {
	SessionID string // e.g. "$1" — tmux session id, prefix restored
	WindowID  string // e.g. "@2" — tmux window id, prefix restored
	Cwd       string // pane_current_path of the outer pane; may be empty
}

// ResolveParentContext resolves the parent (workspace) session, window
// and cwd for a popup that's about to open. Tools used to copy-paste
// this resolver three times (claude, lazygit, popupshell) with subtle
// drift — when we added the @atelier_outer_client global, every copy
// needed to be updated in lockstep. Single source of truth here.
//
// Resolution order:
//
//  1. Env vars TMUX_PARENT_SESSION_ID / TMUX_PARENT_WINDOW_ID set by
//     the binding's `display-popup -e` (most reliable).
//  2. atelier global options @atelier_outer_session / @atelier_outer_window
//     stamped by the M-; root binding.
//  3. tmux's current session/window — last-resort fallback (covers the
//     "tool invoked directly from a shell, not via binding" case).
//
// Cwd uses TMUX_PARENT_PANE_PWD env, falling back to pane_current_path
// of @atelier_outer_pane. Empty cwd is allowed.
//
// Returned IDs always have their tmux sigil ($/@) — the env vars often
// drop sigils, this restores them.
func ResolveParentContext(h Client) (ParentContext, error) {
	return resolveParentContext(h, os.Getenv)
}

// resolveParentContext is the test seam — accepts a getenv function so
// unit tests can supply per-case env values without t.Setenv.
func resolveParentContext(h Client, getenv func(string) string) (ParentContext, error) {
	ctx := ParentContext{
		SessionID: getenv("TMUX_PARENT_SESSION_ID"),
		WindowID:  getenv("TMUX_PARENT_WINDOW_ID"),
	}

	if ctx.SessionID == "" {
		ctx.SessionID, _ = h.ShowGlobalOption("@atelier_outer_session")
	}
	if ctx.WindowID == "" {
		ctx.WindowID, _ = h.ShowGlobalOption("@atelier_outer_window")
	}

	if ctx.SessionID == "" {
		if v, err := h.DisplayMessage("#{session_id}"); err == nil {
			ctx.SessionID = v
		}
	}
	if ctx.WindowID == "" {
		if v, err := h.DisplayMessage("#{window_id}"); err == nil {
			ctx.WindowID = v
		}
	}

	if ctx.SessionID == "" || ctx.WindowID == "" {
		return ctx, fmt.Errorf("popup: could not resolve parent session/window — set TMUX_PARENT_* env, or @atelier_outer_* globals, or invoke from inside tmux")
	}

	ctx.SessionID = ensureSigil(ctx.SessionID, "$")
	ctx.WindowID = ensureSigil(ctx.WindowID, "@")

	ctx.Cwd = getenv("TMUX_PARENT_PANE_PWD")
	if ctx.Cwd == "" {
		if outer, _ := h.ShowGlobalOption("@atelier_outer_pane"); outer != "" {
			ctx.Cwd, _ = h.DisplayMessageAt(outer, "#{pane_current_path}")
		}
	}

	return ctx, nil
}

func ensureSigil(s, sigil string) string {
	if s == "" || strings.HasPrefix(s, sigil) {
		return s
	}
	return sigil + s
}

// WorkspaceScoped describes a popup tool whose backing session lives as
// long as its parent tmux window.
type WorkspaceScoped struct {
	Tool        string // short tool name, e.g. "lazygit"
	DefaultCmd  string // shell command line run if creating the session
	Description string // shown in tool selector / help
}

// SessionName returns the canonical backing-session name for this tool
// and the given parent IDs.
func (w *WorkspaceScoped) SessionName(parentSessionID, parentWindowID string) string {
	return fmt.Sprintf("%s_%s_%s_%s", SessionPrefix, w.Tool,
		digits(parentSessionID), digits(parentWindowID))
}

// Ensure creates the backing session if absent. Idempotent. Does not attach.
// `startDir`, if non-empty, becomes the popup's initial cwd.
func (w *WorkspaceScoped) Ensure(h Client, parentSessionID, parentWindowID, startDir string) error {
	return w.EnsureWithCmd(h, parentSessionID, parentWindowID, startDir, "")
}

// EnsureWithCmd is like Ensure but uses the provided shell command instead of
// DefaultCmd. Used by tools that inject runtime context (e.g., claude
// passing the initial prompt from a per-window option).
//
// If cmd is "", DefaultCmd is used. If both are empty, $SHELL is used.
func (w *WorkspaceScoped) EnsureWithCmd(h Client, parentSessionID, parentWindowID, startDir, cmd string) error {
	name := w.SessionName(parentSessionID, parentWindowID)
	has, err := h.HasSession(name)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	actualCmd := cmd
	if actualCmd == "" {
		actualCmd = w.DefaultCmd
	}
	if actualCmd == "" {
		actualCmd = "$SHELL"
	}
	if startDir != "" {
		actualCmd = fmt.Sprintf("cd %q && %s", startDir, actualCmd)
	}
	return h.NewSessionWithCommand(name, actualCmd)
}

// EnsureAndAttach ensures the backing session, then exec()s into tmux
// attach-session — atelier replaces itself with the tmux client.
func (w *WorkspaceScoped) EnsureAndAttach(h Client, parentSessionID, parentWindowID, startDir string) error {
	return w.EnsureAndAttachWithCmd(h, parentSessionID, parentWindowID, startDir, "")
}

// EnsureAndAttachWithCmd is the Cmd-injected sibling of EnsureAndAttach.
func (w *WorkspaceScoped) EnsureAndAttachWithCmd(h Client, parentSessionID, parentWindowID, startDir, cmd string) error {
	if err := w.EnsureWithCmd(h, parentSessionID, parentWindowID, startDir, cmd); err != nil {
		return err
	}
	return h.Attach(w.SessionName(parentSessionID, parentWindowID))
}

// OpenWorkspaceScoped is the canonical "open a workspace-scoped popup
// tool" entrypoint: resolve parent context, ensure backing session
// (using spec.DefaultCmd), apply popup style, attach. Replaces the
// hand-rolled OpenCommand bodies in popupshell, lazygit, and claude.
//
// For tools whose launch command depends on per-window state (claude
// reading @claude_prompt), use OpenWorkspaceScopedWithCmd to inject a
// resolver function.
func OpenWorkspaceScoped(h Client, spec *WorkspaceScoped) error {
	return OpenWorkspaceScopedWithCmd(h, spec, nil)
}

// OpenWorkspaceScopedWithCmd is OpenWorkspaceScoped + a hook to derive
// the launch command from the resolved ParentContext. fn is invoked
// AFTER context resolution and BEFORE session creation, with the
// resolved ctx (so it can read per-window options on ctx.WindowID).
// Returning ("", nil) from fn falls back to spec.DefaultCmd; returning
// non-empty cmd overrides. Errors from fn short-circuit.
func OpenWorkspaceScopedWithCmd(h Client, spec *WorkspaceScoped, fn func(ctx ParentContext) (string, error)) error {
	ctx, err := ResolveParentContext(h)
	if err != nil {
		return err
	}
	cmd := ""
	if fn != nil {
		cmd, err = fn(ctx)
		if err != nil {
			return err
		}
	}
	if err := spec.EnsureWithCmd(h, ctx.SessionID, ctx.WindowID, ctx.Cwd, cmd); err != nil {
		return err
	}
	name := spec.SessionName(ctx.SessionID, ctx.WindowID)
	if err := ApplyStyle(h, name); err != nil {
		return err
	}
	return h.Attach(name)
}

// SessionGlobal describes a popup tool that has a single backing session
// shared across the tmux server (k9s, pgcli, pgcenter).
type SessionGlobal struct {
	Tool        string
	DefaultCmd  string
	Description string
}

func (s *SessionGlobal) SessionName() string {
	return fmt.Sprintf("%s_%s", SessionPrefix, s.Tool)
}

// Ensure creates the backing session if absent. Idempotent. Does not attach.
//
// On fresh creation, applies the canonical popup style (status off,
// popup key-table, prefix off, aggressive-resize on) to the new
// session — without this, SessionGlobal popups render the inner tmux
// statusline at the bottom (visible "extra tmux statusline" bug).
// k8s/pg used to call ApplyStyle themselves after their custom
// new-session paths; new SessionGlobal tools that go through Ensure
// get it for free here.
func (s *SessionGlobal) Ensure(h Client) error {
	name := s.SessionName()
	has, err := h.HasSession(name)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	cmd := s.DefaultCmd
	if cmd == "" {
		cmd = "$SHELL"
	}
	if err := h.NewSessionWithCommand(name, cmd); err != nil {
		return err
	}
	return ApplyStyle(h, name)
}

// EnsureAndAttach ensures the backing session, then exec()s into tmux attach.
func (s *SessionGlobal) EnsureAndAttach(h Client) error {
	if err := s.Ensure(h); err != nil {
		return err
	}
	return h.Attach(s.SessionName())
}

// Respawn kills the backing session and recreates it with the provided
// shell command. Used for context-switches (k8s switch, pg switch).
func (s *SessionGlobal) Respawn(h Client, shellCmd string) error {
	name := s.SessionName()
	has, err := h.HasSession(name)
	if err != nil {
		return err
	}
	if has {
		if err := h.KillSession(name); err != nil {
			return err
		}
	}
	cmd := shellCmd
	if cmd == "" {
		cmd = s.DefaultCmd
	}
	if cmd == "" {
		cmd = "$SHELL"
	}
	return h.NewSessionWithCommand(name, cmd)
}

func digits(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}
