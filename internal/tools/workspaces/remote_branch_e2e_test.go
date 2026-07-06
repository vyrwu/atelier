//go:build e2e

package workspaces

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveWorktreeBase_RemoteHit checks that when origin has a branch
// matching the requested name, resolveWorktreeBase returns
// `origin/<name>` (not `origin/main`) so the new worktree tracks the
// real PR work instead of an empty branch off main.
//
// Regression: PR #3183 surfaced the bug — atelier always created
// `-b <name> origin/main`, so worktrees for existing PR branches were
// empty (zero diff vs. main) and `gh pr view` confusion ensued.
func TestResolveWorktreeBase_RemoteHit(t *testing.T) {
	tmp := t.TempDir()
	// Create the "remote" repo with an extra branch beyond main.
	remote := mkRepo(t, filepath.Join(tmp, "remote"))
	gitRun(t, remote, "checkout", "-b", "feature/already-exists")
	if err := os.WriteFile(filepath.Join(remote, "feature.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, remote, "add", "feature.txt")
	gitRun(t, remote, "commit", "-m", "feature work")
	gitRun(t, remote, "checkout", "main")

	// Local clone of the remote — this is the "main repo path" atelier sees.
	local := filepath.Join(tmp, "local")
	gitRun(t, "", "clone", remote, local)

	base, tracking := resolveWorktreeBase(local, "feature/already-exists", "main")
	if !tracking {
		t.Fatalf("expected tracking=true when origin has the branch, got false")
	}
	if base != "origin/feature/already-exists" {
		t.Errorf("base: got %q want origin/feature/already-exists", base)
	}
}

// TestResolveWorktreeBase_RemoteMiss checks the fallback path — when
// origin has no matching branch, the worktree bases off
// `origin/<defaultBranch>` as before.
func TestResolveWorktreeBase_RemoteMiss(t *testing.T) {
	tmp := t.TempDir()
	remote := mkRepo(t, filepath.Join(tmp, "remote"))
	local := filepath.Join(tmp, "local")
	gitRun(t, "", "clone", remote, local)

	base, tracking := resolveWorktreeBase(local, "feature/brand-new", "main")
	if tracking {
		t.Errorf("expected tracking=false when origin lacks the branch, got true")
	}
	if base != "origin/main" {
		t.Errorf("base: got %q want origin/main", base)
	}
}

// TestResolveWorktreeBase_WorktreeAddTracksOrigin chains the helper into
// the full `git worktree add -b <name> <base>` invocation atelier
// performs, then asserts the resulting branch is non-empty (i.e. has
// the remote work, not a fresh empty branch off main).
func TestResolveWorktreeBase_WorktreeAddTracksOrigin(t *testing.T) {
	tmp := t.TempDir()
	remote := mkRepo(t, filepath.Join(tmp, "remote"))
	gitRun(t, remote, "checkout", "-b", "feature/pr-branch")
	if err := os.WriteFile(filepath.Join(remote, "pr.txt"), []byte("pr work\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, remote, "add", "pr.txt")
	gitRun(t, remote, "commit", "-m", "pr work")
	gitRun(t, remote, "checkout", "main")

	local := filepath.Join(tmp, "local")
	gitRun(t, "", "clone", remote, local)

	base, _ := resolveWorktreeBase(local, "feature/pr-branch", "main")
	wt := filepath.Join(tmp, "wt")
	if err := runGit(local, "worktree", "add", wt, "-b", "feature/pr-branch", base); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	// The new worktree should contain pr.txt — proves the branch was
	// based on origin/feature/pr-branch, not origin/main.
	if _, err := os.Stat(filepath.Join(wt, "pr.txt")); err != nil {
		t.Errorf("worktree missing pr.txt — branch was based off main instead of remote: %v", err)
	}
}

func mkRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# r\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "README.md")
	gitRun(t, dir, "commit", "-m", "initial")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@e.com",
		"GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@e.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
