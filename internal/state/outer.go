package state

import (
	"errors"
	"strings"
)

// Errors returned by SetOuter when it refuses a bad outer target.
var (
	// ErrOuterNotWorkspace means the target resolved to the launcher or a
	// popup session — stamping either as the outer workspace is the "lands
	// in a weird default shell" bug.
	ErrOuterNotWorkspace = errors.New("state: outer target is launcher/popup, refusing to stamp")
	// ErrOuterStale means the target session/window no longer exists.
	ErrOuterStale = errors.New("state: outer target does not exist")
)

// SetOuter stamps @atelier_outer_pane/session/window for the workspace at
// (sessionTarget, windowTarget), REFUSING to point them at the launcher, a
// popup, or a dead target. This is the write-time guard the old blind restamp
// lacked: cheaper to refuse the bad write than to detect and repair it later.
//
// Targets may carry a tmux "=" exact-match prefix (as LandOuter passes); it is
// stripped for the display-message reads that don't accept it. Does NOT touch
// @atelier_outer_client — that hint is stamped only by the M-; root binding.
func SetOuter(h Host, sessionTarget, windowTarget string) error {
	sessionRef := stripEquals(sessionTarget)
	windowRef := windowTarget
	if windowRef == "" {
		windowRef = sessionRef
	} else {
		windowRef = stripEquals(windowRef)
	}

	name, err := h.DisplayMessageAt(sessionRef, "#{session_name}")
	if err != nil || strings.TrimSpace(name) == "" {
		return ErrOuterStale
	}
	// Refuse the launcher and every atelier-internal backing session (any
	// leading-underscore name) — the same non-outer set clientKind excludes.
	if n := strings.TrimSpace(name); n == LauncherSessionName || strings.HasPrefix(n, "_") {
		return ErrOuterNotWorkspace
	}

	sid, err := h.DisplayMessageAt(sessionRef, "#{session_id}")
	if err != nil || strings.TrimSpace(sid) == "" {
		return ErrOuterStale
	}
	wid, err := h.DisplayMessageAt(windowRef, "#{window_id}")
	if err != nil || strings.TrimSpace(wid) == "" {
		return ErrOuterStale
	}
	pid, err := h.DisplayMessageAt(windowRef, "#{pane_id}")
	if err != nil || strings.TrimSpace(pid) == "" {
		return ErrOuterStale
	}

	if err := h.SetGlobalOption(OptOuterSession, strings.TrimSpace(sid)); err != nil {
		return err
	}
	if err := h.SetGlobalOption(OptOuterWindow, strings.TrimSpace(wid)); err != nil {
		return err
	}
	return h.SetGlobalOption(OptOuterPane, strings.TrimSpace(pid))
}

// hintReader is the minimal tmux surface OuterHint needs — a subset of both
// *tmuxhost.Client and the popup package's Client interface, so both the
// per-command Capture and the popup parent-resolver can share one validated
// read without pulling in the full Host/Topology machinery.
type hintReader interface {
	ShowGlobalOption(name string) (string, error)
	DisplayMessageAt(target, format string) (string, error)
}

// OuterHint reads the stored @atelier_outer_* globals and validates them: it
// returns the (session, window, pane) ids only when the stored session still
// resolves to a live WORKSPACE. When the pointer is missing, stale, or points
// at the launcher/a popup (the raw M-; binding stamps whatever session it
// fired from — a hint, not authority), ok is false so the caller re-derives
// from its current context instead of trusting a poisoned pointer.
//
// This is the read-side half of the outer-pointer contract: SetOuter guards
// the write, OuterHint validates the read. Cheap — two tmux calls, no full
// topology — so it fits the popup-open / tool-open paths that call it.
func OuterHint(h hintReader) (session, window, pane string, ok bool) {
	session, _ = h.ShowGlobalOption(OptOuterSession)
	session = strings.TrimSpace(session)
	if session == "" {
		return "", "", "", false
	}
	name, err := h.DisplayMessageAt(session, "#{session_name}")
	if err != nil || strings.TrimSpace(name) == "" {
		return "", "", "", false // stale: session id no longer resolves
	}
	if ClassifySession(strings.TrimSpace(name)) != KindWorkspace {
		return "", "", "", false // launcher or popup — never a valid outer
	}
	window, _ = h.ShowGlobalOption(OptOuterWindow)
	pane, _ = h.ShowGlobalOption(OptOuterPane)
	return session, strings.TrimSpace(window), strings.TrimSpace(pane), true
}

// ResolveOuter returns the validated outer pointer. If the stored globals
// point at a live workspace session+window it returns them with
// corrected=false. Otherwise it self-corrects to the first attached workspace
// client's session/window (corrected=true); with no workspace client attached
// the corrected pointer is empty (the chain should be cleared).
func ResolveOuter(t *Topology) (Outer, bool) {
	if t.outerValid() {
		return t.OuterPtr, false
	}
	for _, c := range t.OuterClients() {
		return Outer{Session: c.SessionID, Window: c.WindowID, Client: c.Name}, true
	}
	return Outer{}, true
}

// outerValid reports whether the stored outer pointer points at a live
// workspace session and (if set) a live window.
func (t *Topology) outerValid() bool {
	if t.OuterPtr.Session == "" {
		return false
	}
	s, ok := t.SessionByID(t.OuterPtr.Session)
	if !ok || s.Kind != KindWorkspace {
		return false
	}
	if t.OuterPtr.Window != "" && !t.HasWindow(t.OuterPtr.Window) {
		return false
	}
	return true
}

func stripEquals(s string) string {
	if strings.HasPrefix(s, "=") {
		return s[1:]
	}
	return s
}
