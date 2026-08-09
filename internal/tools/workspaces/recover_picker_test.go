package workspaces

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecoverPickerRows covers the M-r "Recover Workspace" row shape: every
// worktree under WorktreeRoot becomes a recoverItem carrying repo + branch
// (the switch identity) and a filterable value covering both. Catches
// regressions in field mapping (repo vs branch) and in the search scope.
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

	items, err := recoverListItems()
	if err != nil {
		t.Fatalf("recoverListItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(items), items)
	}

	want := []struct{ repo, branch string }{
		{"owner1/repoA", "feat/add-foo"},
		{"owner1/repoA", "main"},
		{"owner2", "standalone-branch"},
	}
	for i, w := range want {
		it, ok := items[i].(recoverItem)
		if !ok {
			t.Fatalf("item %d is not a recoverItem: %T", i, items[i])
		}
		if it.repo != w.repo || it.branch != w.branch {
			t.Errorf("item %d: got (%q,%q), want (%q,%q)", i, it.repo, it.branch, w.repo, w.branch)
		}
		// The filter value must cover BOTH repo and branch — the picker
		// searches this field, so missing either breaks search.
		fv := it.FilterValue()
		if !strings.Contains(fv, w.repo) || !strings.Contains(fv, w.branch) {
			t.Errorf("item %d FilterValue %q missing repo %q or branch %q", i, fv, w.repo, w.branch)
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

	items, err := recoverListItems()
	if err != nil {
		t.Fatalf("expected nil err on missing root, got %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty items, got %v", items)
	}
}
