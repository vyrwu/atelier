//go:build e2e

package workspace_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRestore_DriverStartsAtWorkspaceRoot locks the contract: after restore,
// the recreated driver window's pane is at the workspace ROOT persisted in the
// cache, NOT the cwd atelier was launched from.
//
// User-visible bug this guards against: launching `atelier` from `~/code/…`
// and resuming a workspace dumps you in `~/code/…` instead of the workspace's
// dedicated root. The whole point of "resume" is to land where you left off.
func TestRestore_DriverStartsAtWorkspaceRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ATELIER_WORKSPACE_ROOT", t.TempDir())

	// Two distinct real directories to disambiguate "launch cwd" from
	// "workspace root".
	root := t.TempDir()
	launchCwd := t.TempDir()
	t.Chdir(launchCwd) // simulate atelier being launched from a non-workspace dir

	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			Title:       "fake",
			Root:        root,
			Windows: []statestore.Window{
				{Name: "repo", Cwd: root},
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

	// #{pane_start_path} reports the directory passed to -c when the pane was
	// created. (#{pane_current_path} reflects the shell's CURRENT cwd, which
	// can drift if the shell init cd's somewhere.)
	out, err := srv.Client.Run("display-message", "-p",
		"-t", "fake/repo", "#{pane_start_path}")
	if err != nil {
		t.Fatalf("display-message pane_start_path: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != root {
		t.Errorf("pane_start_path = %q, want %q (launched from %q)",
			got, root, launchCwd)
	}
}
