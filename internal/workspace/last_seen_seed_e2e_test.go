//go:build e2e

package workspace_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRegisterCreatedWorkspace_SeedsLastSeen locks in: a freshly
// created workspace gets `LastSeen=now` even if the user never
// switches away from it. Without this, M-q'ing immediately after
// creation leaves the workspace with last_seen=0, and the picker
// reads it as "no age info" on next launch — confusing.
//
// The user-visible bug: open a workspace, M-q immediately, relaunch
// atelier, the M-s picker shows the workspace with NO timestamp.
// Looks like persistence is broken even though it isn't.
func TestRegisterCreatedWorkspace_SeedsLastSeen(t *testing.T) {
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
	got := state.Workspaces[0].LastSeen
	if got < before || got > after {
		t.Errorf("LastSeen at creation = %d, expected in [%d, %d] (now-ish)",
			got, before, after)
	}
}

// TestRegisterCreatedWorkspace_PreservesExistingLastSeen: calling
// RegisterCreatedWorkspace AGAIN on an existing workspace (e.g. user
// re-opens the same default-branch via M-n empty-Enter) must NOT
// overwrite an existing LastSeen with `now`. The stamp-last-seen
// hook owns the actively-maintained value; creation only seeds.
func TestRegisterCreatedWorkspace_PreservesExistingLastSeen(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Seed with a workspace that has a known LastSeen from a prior
	// session-switch.
	const knownLastSeen int64 = 1700000000
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    "/tmp/whatever",
			Kind:        "default-branch",
			LastSeen:    knownLastSeen,
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
	if state.Workspaces[0].LastSeen != knownLastSeen {
		t.Errorf("LastSeen overwritten: got %d, want %d (creation should not clobber)",
			state.Workspaces[0].LastSeen, knownLastSeen)
	}
}
