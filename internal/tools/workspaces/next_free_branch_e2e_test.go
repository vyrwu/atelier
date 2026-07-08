//go:build e2e

package workspaces

import "testing"

// TestNextFreeBranch covers the chore/wip disambiguation: each vague
// prompt names to chore/wip, so unrelated tasks collide — nextFreeBranch
// must hand out chore/wip, chore/wip-2, chore/wip-3, … so each gets its
// own worktree instead of reusing an unrelated wip branch.
func TestNextFreeBranch(t *testing.T) {
	repo := mkRepo(t, t.TempDir())

	if got := nextFreeBranch(repo, "chore/wip"); got != "chore/wip" {
		t.Fatalf("no existing branch: got %q, want chore/wip", got)
	}

	gitRun(t, repo, "branch", "chore/wip")
	if got := nextFreeBranch(repo, "chore/wip"); got != "chore/wip-2" {
		t.Fatalf("wip exists: got %q, want chore/wip-2", got)
	}

	gitRun(t, repo, "branch", "chore/wip-2")
	if got := nextFreeBranch(repo, "chore/wip"); got != "chore/wip-3" {
		t.Fatalf("wip,-2 exist: got %q, want chore/wip-3", got)
	}
}
