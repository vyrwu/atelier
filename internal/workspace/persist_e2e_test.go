//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestSetRecap_WriteThroughToStatestore locks in FR-5.2: SetRecap stamps both
// the tmux window option AND the on-disk cache, so the recap survives `tmux
// kill-server`. This is the load-bearing persistence guarantee for restore.
func TestSetRecap_WriteThroughToStatestore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("vyrwu/atelier")
	// Mark the session atelier-managed (stamp @workspace_id) so the
	// write-through scope check passes (it skips foreign sessions).
	if _, err := srv.Client.Run("set-option", "-t", "vyrwu/atelier",
		workspace.OptWorkspaceID, "vyrwu/atelier"); err != nil {
		t.Fatalf("seed @workspace_id: %v", err)
	}
	out, err := srv.Client.Run("list-windows", "-t", "=vyrwu/atelier", "-F", "#{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	wid := string(out)
	wid = wid[:len(wid)-1] // strip trailing newline
	if wid == "" {
		t.Fatal("list-windows returned empty window id")
	}
	// Register the workspace first (real flows register at creation), giving
	// the record identity so it survives the statestore's atelier-managed
	// Save filter — a window mutation alone carries no identity.
	wname, _ := srv.Client.DisplayMessageAt(wid, "#{window_name}")
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session: "vyrwu/atelier", Title: "atelier", Root: "/tmp/atelier",
		WindowName: wname, Cwd: "/tmp/atelier",
	})

	if err := workspace.SetRecap(srv.Client, wid, "wrote persistence layer"); err != nil {
		t.Fatalf("SetRecap: %v", err)
	}

	// Tmux side: the option is stamped.
	if v, _ := srv.Client.GetWindowOption(wid, "@attention_recap"); v != "wrote persistence layer" {
		t.Errorf("tmux @attention_recap not set: %q", v)
	}

	// Statestore side: keyed by (session_name, window_name).
	s, err := statestore.Load()
	if err != nil {
		t.Fatalf("statestore.Load: %v", err)
	}
	if s == nil {
		t.Fatal("statestore empty after SetRecap")
	}
	actualName, _ := srv.Client.DisplayMessageAt(wid, "#{window_name}")
	w := s.FindWindow("vyrwu/atelier", actualName)
	if w == nil {
		t.Fatalf("window record missing from cache; tmux window name=%q. State:\n%+v",
			actualName, s)
	}
	if w.Recap != "wrote persistence layer" {
		t.Errorf("cache recap = %q, want %q", w.Recap, "wrote persistence layer")
	}
	if w.RecapTs == 0 {
		t.Errorf("cache RecapTs should be non-zero, got %d", w.RecapTs)
	}
}

// TestSetAttention_WriteThroughToStatestore locks in the parallel guarantee for
// the attention flag — without persistence, a Claude task that completed
// mid-restart leaves the user with no indication the recap is stale.
func TestSetAttention_WriteThroughToStatestore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("repo-a")
	if _, err := srv.Client.Run("set-option", "-t", "repo-a",
		workspace.OptWorkspaceID, "repo-a"); err != nil {
		t.Fatalf("seed @workspace_id: %v", err)
	}
	out, _ := srv.Client.Run("list-windows", "-t", "=repo-a", "-F", "#{window_id}")
	wid := string(out)
	wid = wid[:len(wid)-1]
	// Register first so the record has identity and survives the Save filter.
	wname, _ := srv.Client.DisplayMessageAt(wid, "#{window_name}")
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session: "repo-a", Title: "repo-a", Root: "/tmp/repo-a",
		WindowName: wname, Cwd: "/tmp/repo-a",
	})

	if err := workspace.SetAttention(srv.Client, wid, true); err != nil {
		t.Fatalf("SetAttention: %v", err)
	}

	s, _ := statestore.Load()
	if s == nil {
		t.Fatal("statestore empty after SetAttention")
	}
	actualName, _ := srv.Client.DisplayMessageAt(wid, "#{window_name}")
	w := s.FindWindow("repo-a", actualName)
	if w == nil || !w.Attention {
		t.Errorf("attention not persisted: %+v", s)
	}

	// Toggle off → should clear the cached flag.
	if err := workspace.SetAttention(srv.Client, wid, false); err != nil {
		t.Fatalf("SetAttention off: %v", err)
	}
	s, _ = statestore.Load()
	w = s.FindWindow("repo-a", actualName)
	if w != nil && w.Attention {
		t.Errorf("attention not cleared: %+v", w)
	}
}
