//go:build e2e

package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRestore_SkipsSoftClosedWindows guards the accumulation bug behind the
// "so many workspaces / exit cycles through all of them" complaint. Closed
// branches stay in the cache for M-r recover, but restore MUST NOT resurrect
// them as live windows — otherwise a repo session fills with branches you
// already closed.
func TestRestore_SkipsSoftClosedWindows(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()

	openWt := filepath.Join(root, "open")
	closedWt := filepath.Join(root, "closed")
	for _, d := range []string{openWt, closedWt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Mark the closed worktree soft-closed (what the delete flow drops).
	if err := os.WriteFile(filepath.Join(closedWt, ".atelier-soft-closed"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "vyrwu/atelier",
			RepoPath:    "/repo/vyrwu/atelier",
			Kind:        "worktree",
			Windows: []statestore.Window{
				{Name: "open-branch", Cwd: openWt},
				{Name: "closed-branch", Cwd: closedWt},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	srv := testtmux.New(t)
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	out, _ := srv.Client.Run("list-windows", "-t", "=vyrwu/atelier", "-F", "#{window_name}")
	windows := string(out)
	if !strings.Contains(windows, "open-branch") {
		t.Errorf("open branch must be restored; windows:\n%s", windows)
	}
	if strings.Contains(windows, "closed-branch") {
		t.Errorf("soft-closed branch must NOT be restored (accumulation bug); windows:\n%s", windows)
	}
}
