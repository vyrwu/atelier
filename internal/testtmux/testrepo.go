//go:build e2e

package testtmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRepo creates a real git repository under code/github/<owner>/<repo>
// inside the given root, with a single commit on the named default
// branch. Returns the absolute path to the repo.
//
// This lets workspace tests run against actual `git worktree add` /
// `git fetch` paths without external network. The caller is expected to
// point ATELIER_CODE_ROOT at the parent code/github dir.
func TestRepo(t *testing.T, root, owner, repo, defaultBranch string) string {
	t.Helper()
	dir := filepath.Join(root, "code", "github", owner, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=atelier-test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=atelier-test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	run("init", "-b", defaultBranch)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	// Add a fake `origin` remote that points at ourselves so
	// `git fetch origin <branch>` works without a network. We use the
	// same repo as remote — fetch will just be a no-op against itself.
	run("config", "--local", "remote.origin.url", dir)
	run("config", "--local", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	run("fetch", "origin", defaultBranch)
	// Stamp the symbolic-ref so DefaultBranch() resolves correctly.
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+defaultBranch)
	// Wire upstream tracking on the default branch so `git pull
	// --rebase` inside runBgPull works without complaining about
	// missing tracking info. Without this, tests that create a
	// session whose cwd is the fixture repo hit the pull-rebase
	// branch of runBgPull and fail.
	run("branch", "--set-upstream-to=origin/"+defaultBranch, defaultBranch)
	return dir
}

// CodeRoot returns the parent dir to set as ATELIER_CODE_ROOT for tests
// using TestRepo. The picker walks `<CodeRoot>/<owner>/<repo>`, so the
// root passed to TestRepo gets `/code/github` appended internally.
func CodeRoot(root string) string {
	return filepath.Join(root, "code", "github")
}
