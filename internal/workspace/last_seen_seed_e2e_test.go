//go:build e2e

package workspace_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRegisterCreatedWorkspace_SeedsCreatedAt locks in: a freshly
// created workspace gets `CreatedAt=now` even if the user never
// switches away from it. Without this, M-q'ing immediately after
// creation leaves the workspace with created_at=0, and the picker
// reads it as "no age info" on next launch — confusing.
//
// The user-visible bug: open a workspace, M-q immediately, relaunch
// atelier, the M-s picker shows the workspace with NO timestamp.
// Looks like persistence is broken even though it isn't.
func TestRegisterCreatedWorkspace_SeedsCreatedAt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	before := time.Now().Unix()
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "fake/repo",
		RepoPath:   "/tmp/whatever",
		Kind:       "default-branch",
		WindowName: "main",
		Cwd:        "/tmp/whatever",
		Branch:     "main",
	})
	after := time.Now().Unix()

	state, err := statestore.Load()
	if err != nil || state == nil {
		t.Fatalf("Load: %v %v", state, err)
	}
	if len(state.Workspaces) != 1 {
		t.Fatalf("workspaces = %d, want 1", len(state.Workspaces))
	}
	got := state.Workspaces[0].CreatedAt
	if got < before || got > after {
		t.Errorf("CreatedAt at creation = %d, expected in [%d, %d] (now-ish)",
			got, before, after)
	}
}

// TestRegisterCreatedWorkspace_PreservesExistingCreatedAt: calling
// RegisterCreatedWorkspace AGAIN on an existing workspace (e.g. user
// re-opens the same default-branch via M-n empty-Enter) must NOT
// overwrite an existing CreatedAt with `now`. Creation only seeds
// the timestamp once.
func TestRegisterCreatedWorkspace_PreservesExistingCreatedAt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Seed with a workspace that has a known CreatedAt from initial creation.
	const knownCreatedAt int64 = 1700000000
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    "/tmp/whatever",
			Kind:        "default-branch",
			CreatedAt:   knownCreatedAt,
		}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Re-register (simulates user opening the same workspace again).
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "fake/repo",
		RepoPath:   "/tmp/whatever",
		Kind:       "default-branch",
		WindowName: "main",
		Cwd:        "/tmp/whatever",
	})

	state, _ := statestore.Load()
	if state == nil || len(state.Workspaces) == 0 {
		t.Fatal("no workspaces post-register")
	}
	if state.Workspaces[0].CreatedAt != knownCreatedAt {
		t.Errorf("CreatedAt overwritten: got %d, want %d (creation should not clobber)",
			state.Workspaces[0].CreatedAt, knownCreatedAt)
	}
}
