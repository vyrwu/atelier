//go:build e2e

package workspace_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRestore_RestoresLastSeenTimestamp locks in the contract: when a
// workspace is restored from cache, its @last_seen tmux option gets
// re-stamped from the persisted LastSeen field. Without this, the M-s
// picker's "last used" column reads empty for every restored
// workspace, making them all look brand-new.
//
// Regression target: the user reported "M-q + relaunch shows
// workspaces but no time" — the workspace name + repo_path were
// restored but the LastSeen wasn't.
func TestRestore_RestoresLastSeenTimestamp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Seed the cache with a workspace that has LastSeen set.
	fakeRepo := t.TempDir()
	const lastSeenTs int64 = 1700000000
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    fakeRepo,
			Kind:        "default-branch",
			LastSeen:    lastSeenTs,
			Windows: []statestore.Window{
				{Name: "main", Cwd: fakeRepo, Branch: "main"},
			},
		}},
	}); err != nil {
		t.Fatalf("statestore.Save: %v", err)
	}

	// Restore against a fresh tmux server.
	srv := testtmux.New(t)
	srv.NewSession("seed")
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("workspace.Restore: %v", err)
	}

	// Verify @last_seen was stamped on the restored session.
	out, err := srv.Client.Run("show-option", "-t", "fake/repo", "-v", "@last_seen")
	if err != nil {
		t.Fatalf("show-option @last_seen: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want := strconv.FormatInt(lastSeenTs, 10)
	if got != want {
		t.Errorf("@last_seen on restored session = %q, want %q", got, want)
	}
}

// TestRestore_NoLastSeen_NoStamp: when the cache entry has no
// LastSeen value (a workspace was created but never switched away
// from), Restore should NOT stamp the option. A zero-valued epoch
// would be interpreted as "1970-01-01" by the picker — worse than
// empty (which the picker correctly renders as blank).
func TestRestore_NoLastSeen_NoStamp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	fakeRepo := t.TempDir()
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    fakeRepo,
			Kind:        "default-branch",
			// no LastSeen
			Windows: []statestore.Window{
				{Name: "main", Cwd: fakeRepo, Branch: "main"},
			},
		}},
	}); err != nil {
		t.Fatalf("statestore.Save: %v", err)
	}

	srv := testtmux.New(t)
	srv.NewSession("seed")
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("workspace.Restore: %v", err)
	}

	// Use show-option -qv (q = quiet) so unset options return empty
	// instead of erroring with "invalid option: @last_seen".
	out, _ := srv.Client.Run("show-option", "-t", "fake/repo", "-qv", "@last_seen")
	got := strings.TrimSpace(string(out))
	if got != "" {
		t.Errorf("@last_seen on restored session = %q, want empty (no stamp)", got)
	}
}
