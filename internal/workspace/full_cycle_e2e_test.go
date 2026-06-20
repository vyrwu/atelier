//go:build e2e

package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestFullCycle_KillServerThenRestoreReproducesWorkspaces is the
// load-bearing end-to-end test for the entire persistence story.
//
// Scenario:
//  1. Spin up a tmux server. Create a workspace, attention-flag a
//     window, write a recap. These mutations go through the write-
//     through chokepoints (SetRecap, SetAttention,
//     RegisterCreatedWorkspace) → cache file is up to date.
//  2. Kill the tmux server. All in-memory tmux state is gone — but
//     the cache file survives on disk.
//  3. Start a fresh tmux server.
//  4. Invoke workspace.Restore (which is what `atelier state restore`
//     fires from the init tmux config).
//  5. Assert: the workspace is back. The window is named correctly.
//     The recap + attention + claude session id are re-stamped. The
//     user perceives no loss.
//
// If this test fails, the persistence story is broken from the user's
// perspective even if every individual unit test passes.
func TestFullCycle_KillServerThenRestoreReproducesWorkspaces(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	wt := filepath.Join(t.TempDir(), "worktrees", "atelier", "feat-persist")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	// --- Phase 1: original tmux session ---
	srv1 := testtmux.New(t)
	srv1.NewSession("vyrwu/atelier")
	// Mark session atelier-managed so write-through scope check passes.
	if _, err := srv1.Client.Run("set-option", "-t", "vyrwu/atelier", "@repo_path", wt); err != nil {
		t.Fatalf("seed @repo_path: %v", err)
	}

	// Record the original window's @ID before we touch options.
	out, err := srv1.Client.Run("list-windows", "-t", "=vyrwu/atelier", "-F", "#{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	origWid := splitLinesForTest(string(out))[0]

	// Register through the canonical persistence chokepoints (the same
	// path workspaces.go uses in its creator flows).
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "vyrwu/atelier",
		RepoPath:   wt,
		Kind:       "worktree",
		WindowName: actualWindowName(t, srv1, origWid),
		Cwd:        wt,
		Branch:     "feat-persist",
	})
	if err := workspace.SetRecap(srv1.Client, origWid, "Designed persistence layer"); err != nil {
		t.Fatalf("SetRecap: %v", err)
	}
	if err := workspace.SetAttention(srv1.Client, origWid, true); err != nil {
		t.Fatalf("SetAttention: %v", err)
	}
	// Simulate a Claude task completion stamping the session id via
	// the generic metadata API (`ai.active_session_id` key — AI
	// plugin's namespace).
	if err := workspace.PersistWindowMetadata(srv1.Client, origWid,
		"ai.active_session_id", "uuid-abc-123"); err != nil {
		t.Fatalf("PersistWindowMetadata: %v", err)
	}

	// Sanity-check the cache was populated.
	pre, _ := statestore.Load()
	if pre == nil || pre.FindWindow("vyrwu/atelier", actualWindowName(t, srv1, origWid)) == nil {
		t.Fatalf("cache should have entry before kill. State:\n%+v", pre)
	}

	// --- Phase 2: tmux server dies ---
	srv1.Kill()

	// --- Phase 3: fresh tmux server ---
	srv2 := testtmux.New(t)
	// The fresh server has no sessions yet beyond what testtmux created.
	// CRUCIAL: the cache file from srv1 survives in XDG_CACHE_HOME
	// because we set it once at the top of the test.

	// --- Phase 4: restore ---
	if err := workspace.Restore(srv2.Client); err != nil {
		t.Fatalf("workspace.Restore: %v", err)
	}

	// --- Phase 5: assertions ---
	if has, _ := srv2.Client.HasSession("vyrwu/atelier"); !has {
		t.Fatal("session 'vyrwu/atelier' was not restored")
	}

	// Find the restored window's new @ID (different from origWid — tmux
	// reassigns IDs on every server start).
	out, _ = srv2.Client.Run("list-windows", "-t", "=vyrwu/atelier",
		"-F", "#{window_name}|#{window_id}")
	var newWid, newName string
	for _, line := range splitLinesForTest(string(out)) {
		if line == "" {
			continue
		}
		idx := -1
		for i, b := range []byte(line) {
			if b == '|' {
				idx = i
				break
			}
		}
		if idx > 0 {
			newName = line[:idx]
			newWid = line[idx+1:]
			break
		}
	}
	if newWid == "" {
		t.Fatalf("restored window not found. list-windows:\n%s", out)
	}
	// (We deliberately don't assert newWid != origWid — each testtmux
	// server is its own tmux process starting IDs from @0, so they
	// happen to match. The MEANINGFUL property is that options keyed
	// on window NAME made it across the restart, which we check next.)

	// Persisted options should be back, stamped by name (the persistent
	// identity that survives @ID reassignment).
	checks := map[string]string{
		"@needs_attention":      "1",
		"@attention_recap":      "Designed persistence layer",
		"@ai_active_session_id": "uuid-abc-123",
	}
	for opt, want := range checks {
		got, _ := srv2.Client.GetWindowOption(newWid, opt)
		if got != want {
			t.Errorf("restored window option %s: got %q want %q (window=%s name=%s)",
				opt, got, want, newWid, newName)
		}
	}
	// Recap timestamp is present (non-zero) — set by SetRecap before kill.
	if ts, _ := srv2.Client.GetWindowOption(newWid, "@attention_recap_ts"); ts == "" || ts == "0" {
		t.Errorf("@attention_recap_ts should be non-zero, got %q", ts)
	}
}

// actualWindowName resolves the tmux window name for a window @ID.
// testtmux gives newly-created sessions a default window whose name
// varies by shell — we read it back rather than assume.
func actualWindowName(t *testing.T, srv *testtmux.Server, wid string) string {
	t.Helper()
	name, err := srv.Client.DisplayMessageAt(wid, "#{window_name}")
	if err != nil {
		t.Fatalf("display-message #{window_name}: %v", err)
	}
	return name
}

func splitLinesForTest(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
