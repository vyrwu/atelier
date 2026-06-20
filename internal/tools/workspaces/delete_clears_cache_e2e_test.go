//go:build e2e

package workspaces_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestDeleteRow_ClearsCache locks in the contract that deleting a
// workspace via M-x in the session picker also clears the cached
// state. Without this, the next `atelier state restore` would
// resurrect what the user just nuked.
func TestDeleteRow_ClearsCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

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

	// Create a non-default workspace via the manual-name flow. This
	// auto-registers it in the cache (via workspace.RegisterCreatedWorkspace).
	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo", repoDir, "main", "feat-doomed"); err != nil {
		t.Fatalf("create wt: %v", err)
	}

	// Sanity: cache contains the workspace.
	before, _ := statestore.Load()
	if before == nil || before.FindWindow("vyrwu/demo", "feat-doomed") == nil {
		t.Fatalf("cache should contain feat-doomed before delete. State:\n%+v", before)
	}

	// User invokes the delete from the picker (M-x → Confirm? y/n →
	// y). `_delete-row` is the action invoked by the fzf bind.
	row := "vyrwu/demo\tfeat-doomed\t<display>"
	if _, err := srv.RunAtelier("tools", "workspaces", "_delete-row", row); err != nil {
		t.Fatalf("_delete-row: %v", err)
	}

	// Cache should no longer contain feat-doomed. Either the workspace
	// was emptied of windows (and so removed entirely), or the window
	// is gone from a still-present workspace.
	after, _ := statestore.Load()
	if after != nil && after.FindWindow("vyrwu/demo", "feat-doomed") != nil {
		t.Errorf("cache still contains deleted workspace. State:\n%+v", after)
	}
}

// TestDeleteRow_DefaultBranch_ClearsSessionFromCache covers the other
// delete branch: when the picked row is the session's default branch
// with no other windows, the WHOLE session gets killed — and the
// session must also drop from the cache. Seeded directly (no creator
// flow) to keep the test focused on the delete contract.
func TestDeleteRow_DefaultBranch_ClearsSessionFromCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("main")
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	// Seed: session in tmux + matching cache entry.
	if _, err := srv.Client.Run("new-session", "-d", "-s", "vyrwu/demo",
		"-c", repoDir, "-n", "main"); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := srv.Client.Run("set-option", "-t", "vyrwu/demo", "@repo_path", repoDir); err != nil {
		t.Fatalf("seed @repo_path: %v", err)
	}
	_ = statestore.UpdateWorkspace("vyrwu/demo", func(ws *statestore.Workspace) {
		ws.RepoPath = repoDir
		ws.Kind = "worktree"
	})
	_ = statestore.UpdateWindow("vyrwu/demo", "main", func(w *statestore.Window) {
		w.Cwd = repoDir
		w.Branch = "main"
	})

	// Delete the default-branch row → kills whole session.
	row := "vyrwu/demo\tmain\t<display>"
	if _, err := srv.RunAtelier("tools", "workspaces", "_delete-row", row); err != nil {
		t.Fatalf("_delete-row: %v", err)
	}

	after, _ := statestore.Load()
	if after == nil {
		return // empty cache is the desired terminal state
	}
	for _, ws := range after.Workspaces {
		if ws.SessionName == "vyrwu/demo" {
			t.Errorf("session 'vyrwu/demo' should be removed from cache, still present: %+v", ws)
		}
	}
	_ = strings.Contains // keep import touched
}
