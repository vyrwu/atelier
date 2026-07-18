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

// TestRestore_RestoresPerWindowCreatedAt locks in the per-window fix: a
// session with multiple worktree windows re-stamps @workspace_created_ts
// on EVERY window from its own CreatedAt, falling back to the session-level
// Workspace.CreatedAt for windows that predate per-window created_at.
//
// Regression target: the sandbox (all-restore) surfaced multi-worktree
// sessions showing an age on only the first window — the additional
// windows came back blank because only Workspace.CreatedAt was re-stamped.
func TestRestore_RestoresPerWindowCreatedAt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	fakeRepo := t.TempDir()
	const sessionTs int64 = 1700000000
	const winTs int64 = 1700009999
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    fakeRepo,
			Kind:        "worktree",
			CreatedAt:   sessionTs,
			Windows: []statestore.Window{
				// no per-window CreatedAt → must fall back to sessionTs
				{Name: "main", Cwd: fakeRepo, Branch: "main"},
				// own CreatedAt → must win over sessionTs
				{Name: "feat/x", Cwd: fakeRepo, Branch: "feat/x", CreatedAt: winTs},
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

	// Resolve window @IDs (reading by slash-containing name is ambiguous).
	lw, err := srv.Client.Run("list-windows", "-t", "=fake/repo", "-F", "#{window_name}|#{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	idByName := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(lw)), "\n") {
		name, id, _ := strings.Cut(line, "|")
		idByName[name] = id
	}

	for _, tc := range []struct {
		win  string
		want int64
	}{
		{"main", sessionTs}, // fell back to session-level
		{"feat/x", winTs},   // used its own
	} {
		id := idByName[tc.win]
		if id == "" {
			t.Fatalf("window %q not restored", tc.win)
		}
		out, err := srv.Client.Run("show-options", "-w", "-t", id, "-v",
			workspace.OptWorkspaceCreatedTs)
		if err != nil {
			t.Fatalf("show-options %s: %v", tc.win, err)
		}
		got := strings.TrimSpace(string(out))
		if want := strconv.FormatInt(tc.want, 10); got != want {
			t.Errorf("%s @workspace_created_ts = %q, want %q", tc.win, got, want)
		}
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
