package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupRepo creates a bare repo + a clone with one commit on `main`.
// Returns the absolute path to the working clone.
func setupRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}

	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	work := filepath.Join(base, "work")

	mustGit(t, base, "init", "--bare", "--initial-branch=main", bare)
	mustGit(t, base, "clone", bare, work)
	mustGit(t, work, "config", "user.email", "test@example.com")
	mustGit(t, work, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit(t, work, "checkout", "-b", "main")
	mustGit(t, work, "add", "README")
	mustGit(t, work, "commit", "-m", "init")
	mustGit(t, work, "push", "-u", "origin", "main")
	mustGit(t, work, "remote", "set-head", "origin", "main")
	return work
}

func TestDefaultBranch_FromOriginHEAD(t *testing.T) {
	work := setupRepo(t)
	got, err := DefaultBranch(work)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "main" {
		t.Fatalf("DefaultBranch: got %q want main", got)
	}
}

func TestDefaultBranch_FallbackToMain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main", dir)
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "init")

	// No origin/HEAD — should fall back to detecting main locally.
	got, err := DefaultBranch(dir)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "main" {
		t.Fatalf("DefaultBranch: got %q want main", got)
	}
}

func TestPullDefault_OnDefaultBranch_RebasesPull(t *testing.T) {
	work := setupRepo(t)
	// On main, pull-default should not error (no remote changes; nothing to do).
	if err := PullDefault(work); err != nil {
		t.Fatalf("PullDefault on main: %v", err)
	}
}

func TestPullDefault_OnFeatureBranch_FetchesOnly(t *testing.T) {
	work := setupRepo(t)
	mustGit(t, work, "checkout", "-b", "feature")

	// On a feature branch, PullDefault should fetch origin main (no rebase).
	if err := PullDefault(work); err != nil {
		t.Fatalf("PullDefault on feature: %v", err)
	}
	// Verify we're still on the feature branch (not switched to main).
	out := mustGitOutput(t, work, "rev-parse", "--abbrev-ref", "HEAD")
	if out != "feature" {
		t.Fatalf("expected to stay on feature, got %q", out)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, errBuf.String())
	}
}

func mustGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, errBuf.String())
	}
	return string(bytes.TrimSpace(out.Bytes()))
}
