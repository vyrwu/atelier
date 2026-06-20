//go:build e2e

package workspace_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRestore_WindowStartsAtCachedCwd locks the contract: after
// restore, the recreated window's pane is at the cwd persisted in
// the cache, NOT the cwd from which atelier was launched.
//
// User-visible bug this guards against: launching `atelier` from
// `~/code/atelier/` and resuming a `wawafertility/infrastructure`
// workspace dumps you in `~/code/atelier/` instead of the
// `wawafertility/infrastructure` checkout path. Frustrating —
// the whole point of "resume" is to land where you left off.
func TestRestore_WindowStartsAtCachedCwd(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Two distinct real directories to disambiguate "launch cwd"
	// from "workspace cwd".
	workspaceCwd := t.TempDir()
	launchCwd := t.TempDir()
	t.Chdir(launchCwd) // simulate atelier being launched from a non-workspace dir

	// Seed cache.
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    workspaceCwd,
			Kind:        "default-branch",
			Windows: []statestore.Window{
				{Name: "main", Cwd: workspaceCwd, Branch: "main"},
			},
		}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Fresh tmux server (simulating M-q + relaunch).
	srv := testtmux.New(t)
	srv.NewSession("seed")
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("workspace.Restore: %v", err)
	}

	// Read the pane's start directory. tmux's #{pane_start_path}
	// reports the directory passed to -c when the pane was created.
	// (#{pane_current_path} reflects the shell's CURRENT cwd, which
	// can drift if the shell init script cd's somewhere — we want
	// to assert what the SESSION was started with, not where the
	// shell ended up.)
	out, err := srv.Client.Run("display-message", "-p",
		"-t", "fake/repo:main", "#{pane_start_path}")
	if err != nil {
		t.Fatalf("display-message pane_start_path: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != workspaceCwd {
		t.Errorf("pane_start_path = %q, want %q (launched from %q)",
			got, workspaceCwd, launchCwd)
	}
}
