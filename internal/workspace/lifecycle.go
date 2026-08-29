package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// SpawnForgeRefresh fires `atelier tools workspaces _forge-refresh` detached.
// Returns immediately; the per-repo PR sweep + @forge_state stamping happen in
// the background.
//
// Lives in the workspace primitive because EVERY workspace-lifecycle event
// that lands the user on a workspace should refresh the forge badge, so the
// status-line forge icon populates without the user having to M-s into the
// picker first. The subcommand batches per repo, so a single spawn refreshes
// the whole set; its per-repo TTL keeps repeated calls from hammering the forge.
//
// No-op when no forge integration is configured, and in e2e test contexts (the
// detached `gh` query would make network calls and race t.TempDir cleanup).
// Best-effort: a spawn failure is logged, never surfaced.
func SpawnForgeRefresh() {
	if strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-") {
		debuglog.Logf("workspace.SpawnForgeRefresh: SKIP (test socket)")
		return
	}
	if integration.Active().Forge == nil {
		return // no forge adapter — slot is simply absent
	}
	self, err := os.Executable()
	if err != nil {
		self = "atelier"
	}
	cmd := exec.Command(self, "tools", "workspaces", "_forge-refresh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// Own process group so the detached child survives the invoking process
	// exiting (the tmux popup pty closing would otherwise SIGHUP our whole
	// process group, killing the child before it finishes the sweep).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		debuglog.LogErr("workspace.SpawnForgeRefresh start", err)
		return
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	debuglog.Logf("workspace.SpawnForgeRefresh: launched pid=%d self=%s", pid, self)
}

// SessionName normalizes an atelier session identifier (the workspace slug) to
// match how tmux actually stores it. tmux silently rewrites '.' and ':' to '_'
// in session names (they're target-syntax delimiters: `session:window.pane`).
// So a slug like "cloudnativedenmark.dk" is created as session
// "cloudnativedenmark_dk"; any later `-t` target built from the raw name
// resolves the '.dk' as a window/pane and fails.
//
// Callers that DERIVE a session name from a slug MUST run it through SessionName
// before using it as a tmux target OR a statestore key. Names that ORIGINATE
// from tmux (picker rows, list-windows) are already normalized; re-normalizing
// is a harmless no-op (SessionName is idempotent).
func SessionName(name string) string {
	return statestore.CanonicalSessionName(name)
}

// EnsureSession creates the workspace's tmux session if absent, at `root` (the
// workspace's dedicated directory). It stamps @workspace_id (the marker
// Listable keys on) and marks window 1 as the driver-agent window. `driverName`
// names window 1 (cosmetic — the picker renders @workspace_title, not the tmux
// window name).
//
// Returns created=true when it made a fresh session. When the session already
// exists it idempotently re-stamps @workspace_id (a launcher `new-session -A`
// can recreate a killed workspace as a bare shell with no metadata; healing it
// keeps the workspace visible in M-s).
func EnsureSession(h *tmuxhost.Client, session, root, driverName string) (created bool, err error) {
	if has, _ := h.HasSession(session); has {
		if _, e := h.Run("set-option", "-t", session, OptWorkspaceID, session); e != nil {
			debuglog.LogErr("workspace.EnsureSession restamp @workspace_id", e)
		}
		return false, nil
	}
	args := []string{"new-session", "-d", "-s", session}
	if root != "" {
		args = append(args, "-c", root)
	}
	if driverName != "" {
		args = append(args, "-n", driverName)
	}
	if _, err := h.Run(args...); err != nil {
		return false, fmt.Errorf("workspace.EnsureSession new-session %s: %w", session, err)
	}
	if _, err := h.Run("set-option", "-t", session, OptWorkspaceID, session); err != nil {
		debuglog.LogErr("workspace.EnsureSession set @workspace_id", err)
	}
	if wid, _ := h.DisplayMessageAt(session+":1", "#{window_id}"); strings.TrimSpace(wid) != "" {
		MarkDriver(h, strings.TrimSpace(wid))
	}
	return true, nil
}

// MarkDriver flags a window as the workspace's driver-agent window
// (@workspace_driver=1). The picker renders the driver's attention/recap and
// the `multiple_drivers` invariant counts these; inspection shells don't get it.
func MarkDriver(h *tmuxhost.Client, windowID string) {
	if _, err := h.Run("set-option", "-w", "-t", windowID, OptWorkspaceDriver, "1"); err != nil {
		debuglog.LogErr("workspace.MarkDriver", err)
	}
}

// LandOuter brings the outer (workspace) client onto a target session (and
// optionally a specific window within it).
//
// Target arguments are tmux target strings — either `=session` / `=session:name`
// form, or a raw `@<id>`. Either may be empty; the empty side is skipped.
//
// Reads @atelier_outer_client to target the right client by name; falls back to
// a bare switch-client if absent. Without -c outer, a bare switch-client from
// inside a popup pty would switch the POPUP-client (rendering the workspace
// inside the popup).
//
// Order matters: select-window FIRST sets the session's current window
// (select-window does NOT accept -c). switch-client -c <outer> -t =<session>
// THEN attaches the outer client to that session, which displays the window we
// just set. NEVER plain attach — that creates a parallel client.
func LandOuter(h *tmuxhost.Client, sessionTarget, windowTarget string) error {
	if windowTarget != "" {
		if _, err := h.Run("select-window", "-t", windowTarget); err != nil {
			return fmt.Errorf("workspace.LandOuter select-window %q: %w", windowTarget, err)
		}
	}
	if sessionTarget == "" {
		return nil
	}
	outerClient, _ := h.ShowGlobalOption("@atelier_outer_client")
	debuglog.Logf("workspace.LandOuter: session=%q window=%q outer=%q",
		sessionTarget, windowTarget, outerClient)
	args := []string{"switch-client"}
	if outerClient != "" {
		args = append(args, "-c", outerClient)
	}
	args = append(args, "-t", sessionTarget)
	if _, err := h.Run(args...); err != nil {
		return fmt.Errorf("workspace.LandOuter switch-client: %w", err)
	}
	// Re-stamp @atelier_outer_* against the workspace we just landed on, so a
	// popup opened between this switch and the next M-; / M-n / M-s reads fresh
	// globals rather than the previous workspace's pane id.
	restampOuterGlobals(h, sessionTarget, windowTarget)
	// Dismiss popup-clients for OTHER workspaces so a tool popup left open on
	// workspace A doesn't stay visually on top after landing on workspace B.
	detachStalePopups(h, sessionTarget, windowTarget)
	return nil
}

// restampOuterGlobals updates @atelier_outer_session / _window / _pane to point
// at the workspace LandOuter just switched to. Best-effort — the switch itself
// is the load-bearing action; stale globals are a soft regression the next
// M-; / M-n / M-s press would correct anyway.
func restampOuterGlobals(h *tmuxhost.Client, sessionTarget, windowTarget string) {
	if err := state.SetOuter(h, sessionTarget, windowTarget); err != nil {
		debuglog.LogErr("LandOuter restamp", err)
		return
	}
	debuglog.Logf("LandOuter restamp: session=%s window=%s", sessionTarget, windowTarget)
}

// detachStalePopups closes popup overlays whose backing session is scoped to a
// DIFFERENT (session_id, window_id) than the target the user just landed on.
// Popup-backing session names are `_atelier_<tool>_<sid_digits>_<wid_digits>` —
// we keep the suffix matching the target and detach everything else's client(s).
//
// Detaches are dispatched via `run-shell -b` (deferred) so we don't kill our
// own popup pty (one of these clients) mid-call: by the time the deferred
// command runs, we've returned and our popup has closed naturally.
func detachStalePopups(h *tmuxhost.Client, sessionTarget, windowTarget string) {
	keepSidWid := resolveSidWidDigits(h, sessionTarget, windowTarget)
	out, err := h.Run("list-clients", "-F", "#{client_session}|#{client_name}")
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		clientSession := parts[0]
		clientName := parts[1]
		if !shouldDetachPopupClient(clientSession, keepSidWid) {
			continue
		}
		debuglog.Logf("workspace.LandOuter: detaching stale popup client=%q session=%q",
			clientName, clientSession)
		_, _ = h.Run("run-shell", "-b", "tmux detach-client -t "+clientName)
	}
}

// shouldDetachPopupClient decides whether a client attached to a given session
// should be detached on a workspace switch. Pure — extracted for unit-testing
// without spinning up tmux.
//
// Rules:
//   - Only `_atelier_*` popup-backing sessions are candidates.
//   - When keepSidWid is non-empty AND the popup session ends with
//     `_<keepSidWid>`, the popup is scoped to the workspace we're landing on —
//     preserve it.
//   - All other atelier popups are stale (scoped elsewhere) → detached.
//   - Empty keepSidWid (failed resolution) → detach everything (safer to
//     over-clean than leave a popup of unknown scope).
func shouldDetachPopupClient(clientSession, keepSidWid string) bool {
	if !strings.HasPrefix(clientSession, "_atelier_") {
		return false
	}
	if keepSidWid == "" {
		return true
	}
	return !strings.HasSuffix(clientSession, "_"+keepSidWid)
}

// resolveSidWidDigits returns "<sidDigits>_<widDigits>" for the target
// session+window so detachStalePopups can compare against popup-backing session
// names. Returns "" if either lookup fails.
func resolveSidWidDigits(h *tmuxhost.Client, sessionTarget, windowTarget string) string {
	if sessionTarget == "" {
		return ""
	}
	sid, err := h.DisplayMessageAt(stripEqualsPrefix(sessionTarget), "#{session_id}")
	if err != nil || sid == "" {
		return ""
	}
	wt := windowTarget
	if wt == "" {
		wt = sessionTarget
	}
	wid, err := h.DisplayMessageAt(stripEqualsPrefix(wt), "#{window_id}")
	if err != nil || wid == "" {
		return ""
	}
	return state.Digits(sid) + "_" + state.Digits(wid)
}

func stripEqualsPrefix(s string) string {
	if strings.HasPrefix(s, "=") {
		return s[1:]
	}
	return s
}
