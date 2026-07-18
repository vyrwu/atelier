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

// TestRestore_RestoresCreatedTs locks in the contract: a restored
// workspace re-stamps @created_ts on its window from the persisted
// CreatedTs, so the picker's Age sort survives a tmux restart instead of
// showing every restored workspace as brand-new.
func TestRestore_RestoresCreatedTs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	fakeRepo := t.TempDir()
	const createdTs int64 = 1700000000
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    fakeRepo,
			Kind:        "default-branch",
			Windows: []statestore.Window{
				{Name: "main", Cwd: fakeRepo, Branch: "main", CreatedTs: createdTs},
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

	out, err := srv.Client.Run("show-option", "-w", "-t", "fake/repo:main", "-v", workspace.OptCreatedTs)
	if err != nil {
		t.Fatalf("show-option %s: %v", workspace.OptCreatedTs, err)
	}
	got := strings.TrimSpace(string(out))
	want := strconv.FormatInt(createdTs, 10)
	if got != want {
		t.Errorf("%s on restored window = %q, want %q", workspace.OptCreatedTs, got, want)
	}
}

// TestRestore_NoCreatedTs_NoStamp: a cache entry with no CreatedTs must
// NOT stamp the option — a zero epoch would render as 1970 in the Age
// column, worse than blank.
func TestRestore_NoCreatedTs_NoStamp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	fakeRepo := t.TempDir()
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    fakeRepo,
			Kind:        "default-branch",
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

	out, _ := srv.Client.Run("show-option", "-w", "-t", "fake/repo:main", "-qv", workspace.OptCreatedTs)
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("%s on restored window = %q, want empty (no stamp)", workspace.OptCreatedTs, got)
	}
}
