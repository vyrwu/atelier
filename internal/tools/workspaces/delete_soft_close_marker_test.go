package workspaces

import (
	"os"
	"strings"
	"testing"
)

// TestDeleteRow_MarksSoftCloseBeforeKill guards the ordering invariant
// behind a real bug: when you M-x the workspace you're currently on and
// it's the sole window in its session, kill-window destroys the session
// and tears down the pane/client running this very _delete-row process.
// Any statement AFTER the kill (the soft-close marker, statestore prune,
// popup cleanup) then never runs — so M-r's "closed X ago" badge silently
// vanished for self-deletes. The marker (and RemoveWindow) must therefore
// be issued BEFORE kill-window.
//
// Source-order guard rather than behavioral: the teardown that triggers
// the bug is a tmux-pane death of the test's own process, which an
// in-process test can't reproduce (nothing kills the test), so a
// behavioral test would pass even with the buggy order.
func TestDeleteRow_MarksSoftCloseBeforeKill(t *testing.T) {
	src, err := os.ReadFile("workspaces.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	s := string(src)
	start := strings.Index(s, "func DeleteRowCommand")
	if start < 0 {
		t.Fatal("DeleteRowCommand not found")
	}
	body := s[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	marker := strings.Index(body, "touchSoftClosedMarker(")
	remove := strings.Index(body, "statestore.RemoveWindow(")
	kill := strings.Index(body, `"kill-window"`)
	if marker < 0 || kill < 0 || remove < 0 {
		t.Fatalf("expected marker(%d), RemoveWindow(%d) and kill-window(%d) all present", marker, remove, kill)
	}
	if marker > kill {
		t.Error("touchSoftClosedMarker must run BEFORE kill-window (kill can terminate this process on a self-delete)")
	}
	if remove > kill {
		t.Error("statestore.RemoveWindow must run BEFORE kill-window (same teardown risk)")
	}
}

// TestSpawnClaudeResume_ConsultsResumableState guards the on-disk fallback:
// when the window has no live agent popup, spawnClaudeResume must consult
// the active AI adapter's HasResumableState (which checks the tracked
// session id AND the worktree's on-disk transcript) before giving up —
// otherwise the first recover after a delete (which prunes the tracked id)
// opens no agent at all. The transcript check now lives behind the port in
// the adapter; the kernel asks HasResumableState.
func TestSpawnClaudeResume_ConsultsResumableState(t *testing.T) {
	src, err := os.ReadFile("workspaces.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	s := string(src)
	start := strings.Index(s, "func spawnClaudeResume")
	if start < 0 {
		t.Fatal("spawnClaudeResume not found")
	}
	body := s[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "HasResumableState(") {
		t.Error("spawnClaudeResume must consult ai.HasResumableState (tracked id + on-disk transcript) before bailing on an untracked window")
	}
}
