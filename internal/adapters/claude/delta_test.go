package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

// TestWorktreeDelta grounds the recap in real code changes: uncommitted work in
// a git worktree must surface (by filename); a non-git or empty path yields no
// delta so the recap degrades to conversation-only.
func TestWorktreeDelta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}

	dir := t.TempDir()
	runGitTest(t, dir, "init", "-q")
	runGitTest(t, dir, "config", "user.email", "t@example.com")
	runGitTest(t, dir, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", ".")
	runGitTest(t, dir, "commit", "-q", "-m", "base")

	// Uncommitted edit → the delta names the changed file.
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := worktreeDelta(dir)
	if !strings.Contains(got, "app.go") {
		t.Fatalf("delta should mention the changed file; got %q", got)
	}

	if worktreeDelta("") != "" {
		t.Error("empty cwd should yield no delta")
	}
	if d := worktreeDelta(t.TempDir()); d != "" {
		t.Errorf("non-git dir should yield no delta; got %q", d)
	}
}

// TestWorktreeDelta_BranchVsBase covers the committed-branch path: three-dot
// `base...HEAD` must name the branch's OWN changes, and base resolution must
// fall back to a local `main` when origin/HEAD is unset.
func TestWorktreeDelta_BranchVsBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	dir := t.TempDir()
	runGitTest(t, dir, "init", "-q", "-b", "main")
	runGitTest(t, dir, "config", "user.email", "t@example.com")
	runGitTest(t, dir, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", ".")
	runGitTest(t, dir, "commit", "-q", "-m", "base")

	// A branch that adds its own file, committed (so it's branch-delta, not
	// uncommitted).
	runGitTest(t, dir, "checkout", "-q", "-b", "feat")
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n// feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", ".")
	runGitTest(t, dir, "commit", "-q", "-m", "feat work")

	// base resolves to local `main` (no origin here); three-dot names feature.go
	// and NOT base.go (which is shared with the base, not this branch's change).
	if got := branchBase(dir); got != "main" {
		t.Fatalf("branchBase = %q, want main", got)
	}
	got := worktreeDelta(dir)
	if !strings.Contains(got, "feature.go") {
		t.Fatalf("branch delta should name feature.go; got %q", got)
	}
	if strings.Contains(got, "base.go") {
		t.Errorf("branch delta must not include the shared base file; got %q", got)
	}
}

// TestWorktreeDelta_LongLineMemoryBound is the regression guard for the memory
// hole: a few very long lines (minified bundle / base64) pass the line-count
// gate, but the byte-bounded reader must keep the delta small instead of
// buffering megabytes. We assert the result stays bounded (not the full input).
func TestWorktreeDelta_LongLineMemoryBound(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	dir := t.TempDir()
	runGitTest(t, dir, "init", "-q", "-b", "main")
	runGitTest(t, dir, "config", "user.email", "t@example.com")
	runGitTest(t, dir, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "bundle.min.js"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", ".")
	runGitTest(t, dir, "commit", "-q", "-m", "base")

	runGitTest(t, dir, "checkout", "-q", "-b", "feat")
	// One enormous single line — few lines, huge bytes (the gate-evading case).
	huge := "var x=\"" + strings.Repeat("A", 5_000_000) + "\";\n"
	if err := os.WriteFile(filepath.Join(dir, "bundle.min.js"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", ".")
	runGitTest(t, dir, "commit", "-q", "-m", "minified")

	got := worktreeDelta(dir)
	if len([]rune(got)) > maxDeltaRunes+64 {
		t.Fatalf("delta must stay bounded despite a 5MB line; len=%d", len([]rune(got)))
	}
}

// TestChangedLines parses git's --shortstat summary; unparseable input is 0.
func TestChangedLines(t *testing.T) {
	cases := map[string]int{
		" 3 files changed, 12 insertions(+), 4 deletions(-)": 16,
		" 1 file changed, 5 insertions(+)":                   5,
		" 1 file changed, 2 deletions(-)":                    2,
		"":                                                   0,
		"garbage":                                            0,
	}
	for in, want := range cases {
		if got := changedLines(in); got != want {
			t.Errorf("changedLines(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestCapRunes bounds the delta without splitting a rune; short input passes
// through untouched.
func TestCapRunes(t *testing.T) {
	if got := capRunes("short", 100); got != "short" {
		t.Errorf("under cap should pass through; got %q", got)
	}
	got := capRunes(strings.Repeat("x", 50), 10)
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) || !strings.Contains(got, "truncated") {
		t.Errorf("over cap should truncate + mark; got %q", got)
	}
}
