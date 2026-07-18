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

// TestRestore_RestoresCreatedAtTimestamp locks in the contract: when a
// workspace is restored from cache, its @workspace_created_ts tmux option
// gets re-stamped from the persisted CreatedAt field. Without this, the
// M-s picker's age column reads empty for every restored workspace,
// making them all look brand-new.
//
// Regression target: the user reported "M-q + relaunch shows
// workspaces but no time" — the workspace name + repo_path were
// restored but the timestamp wasn't.
func TestRestore_RestoresCreatedAtTimestamp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Seed the cache with a workspace that has CreatedAt set.
	fakeRepo := t.TempDir()
	const createdAtTs int64 = 1700000000
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    fakeRepo,
			Kind:        "default-branch",
			CreatedAt:   createdAtTs,
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

	// Verify @workspace_created_ts was stamped on the restored window.
	out, err := srv.Client.Run("show-options", "-w", "-t", "fake/repo:main", "-v",
		workspace.OptWorkspaceCreatedTs)
	if err != nil {
		t.Fatalf("show-options @workspace_created_ts: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want := strconv.FormatInt(createdAtTs, 10)
	if got != want {
		t.Errorf("@workspace_created_ts on restored window = %q, want %q", got, want)
	}
}

// TestRestore_NoCreatedAt_NoStamp: when the cache entry has no
// CreatedAt value (a workspace was created but the value is zero),
// Restore should NOT stamp the option. A zero-valued epoch
// would be interpreted as "1970-01-01" by the picker — worse than
// empty (which the picker correctly renders as blank).
func TestRestore_NoCreatedAt_NoStamp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	fakeRepo := t.TempDir()
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    fakeRepo,
			Kind:        "default-branch",
			// no CreatedAt
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

	// Use show-options -qv (q = quiet) so unset options return empty
	// instead of erroring with "invalid option: @workspace_created_ts".
	out, _ := srv.Client.Run("show-options", "-w", "-t", "fake/repo:main", "-qv",
		workspace.OptWorkspaceCreatedTs)
	got := strings.TrimSpace(string(out))
	if got != "" {
		t.Errorf("@workspace_created_ts on restored window = %q, want empty (no stamp)", got)
	}
}
