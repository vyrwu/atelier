//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestSetRecap_WriteThroughToStatestore locks in FR-5.2: SetRecap
// stamps both the tmux window option AND the on-disk cache, so the
// recap survives `tmux kill-server`. This is the load-bearing
// persistence guarantee for the entire restore feature.
func TestSetRecap_WriteThroughToStatestore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("vyrwu/atelier")
	// Mark the session as atelier-managed so the write-through scope
	// check passes (write-through skips foreign sessions).
	if _, err := srv.Client.Run("set-option", "-t", "vyrwu/atelier", "@repo_path", "/fake/repo"); err != nil {
		t.Fatalf("seed @repo_path: %v", err)
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

	if err := workspace.SetRecap(srv.Client, wid, "wrote persistence layer"); err != nil {
		t.Fatalf("SetRecap: %v", err)
	}

	// Tmux side: the option is stamped.
	if v, _ := srv.Client.GetWindowOption(wid, "@attention_recap"); v != "wrote persistence layer" {
		t.Errorf("tmux @attention_recap not set: %q", v)
	}

	// Statestore side: the recap is in the cache, keyed by (session_name,
	// window_name) — the tmux-id-agnostic persistent identity.
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

// TestSetAttention_WriteThroughToStatestore locks in the parallel
// guarantee for the attention flag — without persistence, a Claude
// task that completed mid-restart leaves the user with no indication
// that the recap is from before they last looked.
func TestSetAttention_WriteThroughToStatestore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("repo-a")
	if _, err := srv.Client.Run("set-option", "-t", "repo-a", "@repo_path", "/fake/repo"); err != nil {
		t.Fatalf("seed @repo_path: %v", err)
	}
	out, _ := srv.Client.Run("list-windows", "-t", "=repo-a", "-F", "#{window_id}")
	wid := string(out)
	wid = wid[:len(wid)-1]

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
