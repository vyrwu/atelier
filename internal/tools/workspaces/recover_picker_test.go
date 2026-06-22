package workspaces

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecoverPickerRows covers the M-r "Recover Workspace" row shape: every
// worktree under WorktreeRoot becomes a tab-separated row of
// `<repo>\t<branch>\t<display>` where the display column is what fzf
// renders. Catches regressions in field order (the picker's bind
// transforms split on \t and assume `repo` is column 1, `branch`
// column 2).
func TestRecoverPickerRows(t *testing.T) {
	tmp := t.TempDir()
	mkWorktree := func(parts ...string) {
		dir := filepath.Join(tmp, filepath.Join(parts...))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %v: %v", parts, err)
		}
		// A real worktree has a `.git` FILE at its root. listWorktrees
		// uses that as the "this dir is a worktree" signal.
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /dev/null\n"), 0o644); err != nil {
			t.Fatalf("touch .git in %v: %v", parts, err)
		}
	}
	// Layout (github-style root + one flat repo):
	//   owner1/repoA/feat/add-foo
	//   owner1/repoA/main
	//   owner2/standalone-branch
	mkWorktree("owner1", "repoA", "feat", "add-foo")
	mkWorktree("owner1", "repoA", "main")
	mkWorktree("owner2", "standalone-branch")

	t.Setenv("ATELIER_WORKTREE_ROOT", tmp)

	rows, err := recoverPickerRows()
	if err != nil {
		t.Fatalf("recoverPickerRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(rows), rows)
	}

	wantPrefixes := []string{
		"owner1/repoA\tfeat/add-foo\t",
		"owner1/repoA\tmain\t",
		"owner2\tstandalone-branch\t",
	}
	for i, want := range wantPrefixes {
		if !strings.HasPrefix(rows[i], want) {
			t.Errorf("row %d:\n  got:  %q\n  want prefix: %q", i, rows[i], want)
		}
		// Display column (after the second tab) must contain BOTH repo
		// and branch — fzf --nth=3 searches only this field, so missing
		// either breaks search.
		fields := strings.SplitN(rows[i], "\t", 3)
		if len(fields) != 3 {
			t.Fatalf("row %d has %d fields, want 3: %q", i, len(fields), rows[i])
		}
		display := fields[2]
		if !strings.Contains(display, fields[0]) || !strings.Contains(display, fields[1]) {
			t.Errorf("row %d display missing repo/branch: display=%q repo=%q branch=%q",
				i, display, fields[0], fields[1])
		}
	}
}

// TestRemoveWorktree_MissingMainRepo covers the recover-picker orphan
// path: a worktree directory exists on disk but the main repo at
// repoPath is gone (user `rm -rf`'d ~/code/github/<repo>/). git
// worktree remove --force fails with "chdir <repoPath>: no such file
// or directory". removeWorktree must fall back to direct removal so
// the picker entry actually disappears after delete + reload.
func TestRemoveWorktree_MissingMainRepo(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "wt-orphan")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /nowhere\n"), 0o644); err != nil {
		t.Fatalf("touch .git: %v", err)
	}
	missingRepo := filepath.Join(tmp, "main-repo-that-does-not-exist")

	if err := removeWorktree(missingRepo, wt); err != nil {
		t.Fatalf("removeWorktree fallback should succeed when main repo missing: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still present after removeWorktree: stat err=%v", err)
	}
}

// TestRecoverPickerRows_EmptyRoot returns ([], nil) when WorktreeRoot
// doesn't exist — the picker handles empty by showing an inline header
// instead of erroring (mirrors the sessions picker's empty UX).
func TestRecoverPickerRows_EmptyRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ATELIER_WORKTREE_ROOT", filepath.Join(tmp, "does-not-exist"))

	rows, err := recoverPickerRows()
	if err != nil {
		t.Fatalf("expected nil err on missing root, got %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty rows, got %v", rows)
	}
}
