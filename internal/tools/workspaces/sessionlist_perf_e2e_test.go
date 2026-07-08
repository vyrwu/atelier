//go:build e2e

// Perf regression test for the M-s session picker load path.
// BuildSessionList emits one row per window but the default branch is a
// per-repo fact — so DefaultBranch must be resolved once per distinct
// repo, not once per row. Before the memoization fix a repo session
// with many branch windows fanned out one `git symbolic-ref` spawn per
// window on the synchronous path before the picker opened, which is the
// "sandbox M-s slow load" this test guards against.
package workspaces_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/perf"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/tools/workspaces"
)

func TestBuildSessionList_MemoizesDefaultBranchPerRepo(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	// Deliberately skip SourceInit: it triggers `atelier state restore`,
	// which recreates the developer's real workspaces on the test socket
	// and makes the row/repo counts non-hermetic. BuildSessionList only
	// reads live tmux state, so the picker path needs no init config.
	_ = srv.Attach(t, "main")
	time.Sleep(200 * time.Millisecond)

	// Force the one-time `go build` of the tool binaries BEFORE HOME is
	// repointed at the temp dir — otherwise Go writes its (read-only)
	// module cache into t.TempDir and RemoveAll cleanup fails.
	_ = srv.BinDir()

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	// Create the repo session (stamps session-level @repo_path) with one
	// window, then add three more. @repo_path is inherited by every
	// window in the session, so BuildSessionList emits one row per
	// window all pointing at the same repo.
	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo", repoDir, "main", "feat-one"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, w := range []string{"feat-two", "feat-three", "feat-four"} {
		if _, err := srv.Client.Run("new-window", "-d", "-t", "vyrwu/demo",
			"-c", repoDir, "-n", w); err != nil {
			t.Fatalf("seed window %s: %v", w, err)
		}
	}

	before := perf.Calls("git")
	rows, err := workspaces.BuildSessionList(srv.Client)
	if err != nil {
		t.Fatalf("BuildSessionList: %v", err)
	}
	gitCalls := perf.Calls("git") - before

	repoRows := 0
	for _, r := range rows {
		if r.Session == "vyrwu/demo" {
			repoRows++
		}
	}
	if repoRows != 4 {
		t.Fatalf("expected 4 rows for vyrwu/demo, got %d (rows=%+v)", repoRows, rows)
	}

	// The fix: one distinct repo → DefaultBranch shells git exactly once
	// (the origin/HEAD symbolic-ref resolves on the first try), cached
	// for the other three windows. Without memoization this scales with
	// row count — 4 git spawns for these 4 windows.
	if gitCalls != 1 {
		t.Errorf("BuildSessionList made %d git calls for 1 repo across %d windows; "+
			"expected 1 (memoized per repo, not per row)", gitCalls, repoRows)
	}
}
