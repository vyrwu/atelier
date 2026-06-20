package workspace

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestComputeDefaultBranch_FallsBackWhenSymrefMissing locks in the
// fallback behavior: when origin/HEAD is NOT set as a local symref
// (the case that hit us on the user's vyrwu/atelier repo — bg-pull
// warmup silently skipped it forever), we probe origin/main and
// origin/master via rev-parse.
//
// Without the fallback the user's freshness icon never appeared for
// repos that were cloned without --set-upstream-head or via tooling
// that didn't run `git remote set-head`.
func TestComputeDefaultBranch_FallsBackWhenSymrefMissing(t *testing.T) {
	root := t.TempDir()

	// Bare "remote" we can fetch from.
	remote := filepath.Join(root, "remote.git")
	mustRun(t, "git", "init", "--bare", "--initial-branch=main", remote)

	// Real working repo that pushes a single commit to the remote
	// on the main branch.
	src := filepath.Join(root, "src")
	mustRun(t, "git", "init", "--initial-branch=main", src)
	mustRun(t, "git", "-C", src, "commit", "--allow-empty", "-m", "init")
	mustRun(t, "git", "-C", src, "remote", "add", "origin", remote)
	mustRun(t, "git", "-C", src, "push", "-u", "origin", "main")

	// Clone fresh from the remote → origin/HEAD IS set by default on
	// clone, so we DELIBERATELY clear it to simulate the broken
	// state the user hit on their atelier repo.
	clone := filepath.Join(root, "clone")
	mustRun(t, "git", "clone", remote, clone)
	mustRun(t, "git", "-C", clone, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")

	// Sanity: symbolic-ref should now FAIL.
	if err := exec.Command("git", "-C", clone, "symbolic-ref", "--short",
		"refs/remotes/origin/HEAD").Run(); err == nil {
		t.Fatal("setup error: expected symbolic-ref to fail after delete")
	}

	got, err := computeDefaultBranch(clone)
	if err != nil {
		t.Fatalf("computeDefaultBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("default branch: got %q want %q", got, "main")
	}
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
