package workspace_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRegisterCreatedWorkspace_SeedsCreatedTs: a freshly registered
// workspace window gets CreatedTs=now when the caller supplied none, so
// the picker's Age column has a value immediately (even before the first
// restart re-stamps it from the cache).
func TestRegisterCreatedWorkspace_SeedsCreatedTs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	before := time.Now().Unix()
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "fake/repo",
		RepoPath:   "/tmp/fake",
		Kind:       "worktree",
		WindowName: "feat/x",
		Cwd:        "/tmp/fake/feat/x",
		Branch:     "feat/x",
	})
	after := time.Now().Unix()

	state, err := statestore.Load()
	if err != nil || state == nil {
		t.Fatalf("load: %v %v", state, err)
	}
	w := state.FindWindow("fake/repo", "feat/x")
	if w == nil {
		t.Fatalf("window not registered: %+v", state.Workspaces)
	}
	if w.CreatedTs < before || w.CreatedTs > after {
		t.Errorf("CreatedTs = %d, want in [%d, %d]", w.CreatedTs, before, after)
	}
}

// TestRegisterCreatedWorkspace_PreservesExistingCreatedTs: re-registering
// an existing window must NOT reset its age — CreatedTs is stamped once at
// creation so the Age sort is a true "how old" signal, not last-touched.
func TestRegisterCreatedWorkspace_PreservesExistingCreatedTs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const known int64 = 1700000000
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    "/tmp/fake",
			Kind:        "worktree",
			Windows:     []statestore.Window{{Name: "feat/x", CreatedTs: known}},
		}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "fake/repo",
		RepoPath:   "/tmp/fake",
		Kind:       "worktree",
		WindowName: "feat/x",
		Cwd:        "/tmp/fake/feat/x",
		Branch:     "feat/x",
		CreatedTs:  time.Now().Unix(),
	})

	state, _ := statestore.Load()
	w := state.FindWindow("fake/repo", "feat/x")
	if w == nil || w.CreatedTs != known {
		t.Errorf("CreatedTs overwritten: got %+v, want %d (creation must not clobber)", w, known)
	}
}
