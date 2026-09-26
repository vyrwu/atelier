package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A conflicted rebase is detected as in progress — what create_pr relies on
// to leave it resolvable rather than abort it into an endless retry.
func TestRebaseInProgress(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base\n")
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-qm", "base")
	git(t, dir, "checkout", "-qb", "feat")
	write("feat\n")
	git(t, dir, "commit", "-qam", "feat")
	git(t, dir, "checkout", "-q", "main")
	write("main\n")
	git(t, dir, "commit", "-qam", "main")
	git(t, dir, "checkout", "-q", "feat")

	if rebaseInProgress(dir) {
		t.Fatal("reports a rebase before one started")
	}
	if _, err := runGit(dir, "rebase", "main"); err == nil {
		t.Fatal("expected the rebase to conflict")
	}
	if !rebaseInProgress(dir) {
		t.Error("a conflicted rebase was not detected as in progress")
	}
	git(t, dir, "rebase", "--abort")
	if rebaseInProgress(dir) {
		t.Error("still reports a rebase after it was aborted")
	}
}

// git must never stop to ask for credentials: under an agent the terminal is
// Claude's UI, and a prompt there hangs the tool call forever. A shell alias
// prints the environment git itself runs with.
func TestRunGitNeverPrompts(t *testing.T) {
	out, err := runGit(t.TempDir(), "-c", "alias.env=!printenv GIT_TERMINAL_PROMPT", "env")
	if err != nil {
		t.Fatalf("alias failed: %v (%s)", err, out)
	}
	if out != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q in git's environment, want 0", out)
	}
}
