//go:build e2e

// Internal e2e coverage for reuseExistingWorkspace — the graceful
// "branch/window already exists" path shared by the manual-name and
// Claude-named build flows. Regression guard for the bug where the auto
// (Claude-named) creator dumped the user out of the picker with a raw
// "branch already exists" error instead of landing them on the existing
// workspace.
package workspaces

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestReuseExistingWorkspace_JumpsToExistingWindow: when a window with the
// requested name already exists, reuse returns handled=true pointing at it
// and creates no duplicate window.
func TestReuseExistingWorkspace_JumpsToExistingWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	_ = srv.Attach(t, "main")
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo", repoDir, "main", "feat-reuse"); err != nil {
		t.Fatalf("create: %v", err)
	}

	wt, wid, handled, err := reuseExistingWorkspace(srv.Client, "vyrwu/demo", repoDir, "feat-reuse", "main")
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for existing window")
	}
	if wid == "" {
		t.Error("expected non-empty window id")
	}
	if wt == "" {
		t.Error("expected non-empty worktree path")
	}
	if wins := srv.WindowsIn("vyrwu/demo"); len(wins) != 1 {
		t.Errorf("expected 1 window (no duplicate), got %d: %v", len(wins), wins)
	}
}

// TestReuseExistingWorkspace_UnknownName_NotHandled: a name that maps to no
// window and no branch is not reusable — the caller falls through to the
// normal build path.
func TestReuseExistingWorkspace_UnknownName_NotHandled(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	_ = srv.Attach(t, "main")
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	_, _, handled, err := reuseExistingWorkspace(srv.Client, "vyrwu/demo", repoDir, "feat-nonexistent", "main")
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if handled {
		t.Fatal("expected handled=false for unknown name")
	}
}
