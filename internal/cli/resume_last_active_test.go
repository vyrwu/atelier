package cli

import (
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

// TestResolveLaunchSession_FallbackWhenNoCache locks in: first-ever
// atelier launch (no cache file) returns the caller's fallback.
// Without this, the bundled launcher would receive empty string
// and tmux's new-session would auto-name the session "0" — the
// "weird auto-naming" bug we already burned an iteration on.
func TestResolveLaunchSession_FallbackWhenNoCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if got := resolveLaunchSession("default"); got != "default" {
		t.Errorf("no-cache fallback: got %q, want %q", got, "default")
	}
}

// TestResolveLaunchSession_ReturnsLastActive: the load-bearing case.
// When the cache has a LastActiveSession set (the
// client-session-changed hook wrote it during a prior session),
// resolveLaunchSession returns that name. The launcher then
// attaches there instead of "default" — the user resumes work.
func TestResolveLaunchSession_ReturnsLastActive(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := statestore.SetLastActiveSession("vyrwu/atelier"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := resolveLaunchSession("default"); got != "vyrwu/atelier" {
		t.Errorf("got %q, want %q", got, "vyrwu/atelier")
	}
}

// TestResolveLaunchSession_FallbackOnEmptyLastActive: when the
// cache exists but LastActiveSession was explicitly cleared (e.g.
// the user only ever sat on "default" — see stamp-last-active's
// filter that skips bootstrap sessions), fall back to "default".
func TestResolveLaunchSession_FallbackOnEmptyLastActive(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Save a state with NO last-active set. Some other workspace
	// state may be cached but the resume target is empty.
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "some/other",
			RepoPath:    "/tmp/whatever",
			Kind:        "default-branch",
		}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := resolveLaunchSession("default"); got != "default" {
		t.Errorf("empty-last-active fallback: got %q, want %q", got, "default")
	}
}

// TestSetLastActiveSession_RoundTrip locks the contract on the
// statestore primitive that the stamp-last-active subcommand calls.
// Reading back what we just wrote should return the same value.
// Captures any future serialization regression (e.g. forgetting
// the JSON tag, breaking the field order on save).
func TestSetLastActiveSession_RoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := statestore.SetLastActiveSession("foo/bar"); err != nil {
		t.Fatalf("set: %v", err)
	}
	state, err := statestore.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state == nil {
		t.Fatal("state is nil after write")
	}
	if state.LastActiveSession != "foo/bar" {
		t.Errorf("LastActiveSession = %q, want %q", state.LastActiveSession, "foo/bar")
	}
}
