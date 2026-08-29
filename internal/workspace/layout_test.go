package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

// TestCanonicalPath covers WS-2's landmine: a path reaching an AI adapter must
// hash to its REAL location, never a symlink into the workspace root.
func TestCanonicalPath(t *testing.T) {
	t.Run("empty stays empty", func(t *testing.T) {
		if got := CanonicalPath(""); got != "" {
			t.Errorf("CanonicalPath(\"\") = %q, want \"\"", got)
		}
	})

	t.Run("symlink resolves to its target", func(t *testing.T) {
		root := t.TempDir()
		real := filepath.Join(root, "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		got := CanonicalPath(link)
		want, _ := filepath.EvalSymlinks(real) // macOS /var → /private/var
		if got != want {
			t.Errorf("CanonicalPath(%q) = %q, want %q", link, got, want)
		}
	})

	t.Run("missing path returns input unchanged", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		if got := CanonicalPath(missing); got != missing {
			t.Errorf("CanonicalPath(%q) = %q, want the input back", missing, got)
		}
	})
}

// TestWorktreeLinkPath locks the symlink layout policy: <root>/<repo-base>/
// <flat-branch>, where repo-base is the LAST segment of the owner/repo slug and
// the branch is FLATTENED (slashes → dashes) so worktrees within a repo are a
// flat list, never nested under feat/.
func TestWorktreeLinkPath(t *testing.T) {
	root := "/home/u/ateliers/ws"
	cases := []struct {
		repo, branch, want string
	}{
		{"wawa/helm-charts", "feat/x", "/home/u/ateliers/ws/helm-charts/feat-x"},
		{"vyrwu/atelier", "main", "/home/u/ateliers/ws/atelier/main"},
		{"atelier", "feat/a/b", "/home/u/ateliers/ws/atelier/feat-a-b"},
	}
	for _, c := range cases {
		if got := WorktreeLinkPath(root, c.repo, c.branch); got != c.want {
			t.Errorf("WorktreeLinkPath(%q,%q,%q) = %q, want %q",
				root, c.repo, c.branch, got, c.want)
		}
	}
}

func TestWorktreeDirName(t *testing.T) {
	cases := map[string]string{
		"feat/x":   "feat-x",
		"feat/a/b": "feat-a-b",
		"main":     "main",
		"":         "",
	}
	for in, want := range cases {
		if got := WorktreeDirName(in); got != want {
			t.Errorf("WorktreeDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLinkWorktree_CreateReplaceRemove exercises the full lifecycle of a
// worktree symlink under a temp root.
func TestLinkWorktree_CreateReplaceRemove(t *testing.T) {
	root := t.TempDir()
	wtA := filepath.Join(t.TempDir(), "wt-a")
	wtB := filepath.Join(t.TempDir(), "wt-b")
	for _, d := range []string{wtA, wtB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Create.
	link, err := LinkWorktree(root, wtA, "owner/repo", "feat/x")
	if err != nil {
		t.Fatalf("LinkWorktree create: %v", err)
	}
	if want := WorktreeLinkPath(root, "owner/repo", "feat/x"); link != want {
		t.Errorf("link path = %q, want %q", link, want)
	}
	if target, _ := os.Readlink(link); target != wtA {
		t.Errorf("symlink target = %q, want %q", target, wtA)
	}

	// Idempotent re-link to the same target returns without churn.
	if _, err := LinkWorktree(root, wtA, "owner/repo", "feat/x"); err != nil {
		t.Fatalf("LinkWorktree idempotent: %v", err)
	}
	if target, _ := os.Readlink(link); target != wtA {
		t.Errorf("after idempotent re-link, target = %q, want %q", target, wtA)
	}

	// Replace: same link, different target.
	if _, err := LinkWorktree(root, wtB, "owner/repo", "feat/x"); err != nil {
		t.Fatalf("LinkWorktree replace: %v", err)
	}
	if target, _ := os.Readlink(link); target != wtB {
		t.Errorf("after replace, target = %q, want %q", target, wtB)
	}

	// Remove.
	UnlinkWorktree(link)
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("UnlinkWorktree left something at %q (err=%v)", link, err)
	}
}

// TestLinkWorktree_RefusesToClobberRealDir guards user data: a real directory
// squatting the link path is left alone (error), not nuked.
func TestLinkWorktree_RefusesToClobberRealDir(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create a real dir where the link would go.
	realDir := WorktreeLinkPath(root, "owner/repo", "feat/x")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkWorktree(root, wt, "owner/repo", "feat/x"); err == nil {
		t.Errorf("expected error when a real dir squats the link path, got nil")
	}
}

// TestReconcileLayout_Relinks re-creates a missing link whose worktree is live.
func TestReconcileLayout_Relinks(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	wts := []statestore.Worktree{{Repo: "owner/repo", Branch: "feat/x", Path: wt}}

	fixes := ReconcileLayout(root, wts)
	if len(fixes) != 1 || fixes[0].Code != "relinked" {
		t.Fatalf("expected one 'relinked' fix, got %+v", fixes)
	}
	link := WorktreeLinkPath(root, "owner/repo", "feat/x")
	if target, err := os.Readlink(link); err != nil || target != wt {
		t.Errorf("link not created: target=%q err=%v", target, err)
	}

	// Second pass is a no-op — the link already points at the right target.
	if fixes := ReconcileLayout(root, wts); len(fixes) != 0 {
		t.Errorf("second reconcile should be a no-op, got %+v", fixes)
	}
}

// TestReconcileLayout_ReportsDangling flags a worktree whose real dir is gone.
func TestReconcileLayout_ReportsDangling(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	wts := []statestore.Worktree{{Repo: "owner/repo", Branch: "feat/x", Path: gone}}

	fixes := ReconcileLayout(root, wts)
	if len(fixes) != 1 || fixes[0].Code != "dangling" {
		t.Fatalf("expected one 'dangling' fix, got %+v", fixes)
	}
	// No symlink should have been created for a dangling worktree.
	link := WorktreeLinkPath(root, "owner/repo", "feat/x")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("dangling worktree should not produce a symlink at %q", link)
	}
}

// TestReconcileLayout_GCsOrphanSymlink removes a symlink under the root that no
// worktree record backs.
func TestReconcileLayout_GCsOrphanSymlink(t *testing.T) {
	root := t.TempDir()
	realWt := filepath.Join(t.TempDir(), "real")
	orphanTarget := filepath.Join(t.TempDir(), "orphan-target")
	for _, d := range []string{realWt, orphanTarget} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// One backed worktree, one orphan symlink with no record.
	if _, err := LinkWorktree(root, realWt, "owner/repo", "keep"); err != nil {
		t.Fatal(err)
	}
	orphanLink := WorktreeLinkPath(root, "owner/repo", "orphan")
	if err := os.MkdirAll(filepath.Dir(orphanLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(orphanTarget, orphanLink); err != nil {
		t.Fatal(err)
	}

	wts := []statestore.Worktree{{Repo: "owner/repo", Branch: "keep", Path: realWt}}
	fixes := ReconcileLayout(root, wts)

	var gcCount int
	for _, f := range fixes {
		if f.Code == "gc-link" {
			gcCount++
			if f.Subject != orphanLink {
				t.Errorf("gc'd wrong link: got %q want %q", f.Subject, orphanLink)
			}
		}
	}
	if gcCount != 1 {
		t.Fatalf("expected exactly one gc-link fix, got %+v", fixes)
	}
	if _, err := os.Lstat(orphanLink); !os.IsNotExist(err) {
		t.Errorf("orphan symlink not removed")
	}
	// The backed link survives.
	keepLink := WorktreeLinkPath(root, "owner/repo", "keep")
	if target, err := os.Readlink(keepLink); err != nil || target != realWt {
		t.Errorf("backed link should survive GC: target=%q err=%v", target, err)
	}
}

// TestReconcileLayout_EmptyRootNoOp: an empty root is a no-op (nothing to fix).
func TestReconcileLayout_EmptyRootNoOp(t *testing.T) {
	if fixes := ReconcileLayout("", []statestore.Worktree{{Repo: "a/b", Branch: "x", Path: "/p"}}); fixes != nil {
		t.Errorf("empty root should yield nil fixes, got %+v", fixes)
	}
}

// TestWorkspaceRootFor derives <base>/<slug>, honoring ATELIER_WORKSPACE_ROOT.
func TestWorkspaceRootFor(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ATELIER_WORKSPACE_ROOT", base)

	if got, want := WorkspaceRootBase(), base; got != want {
		t.Errorf("WorkspaceRootBase() = %q, want %q", got, want)
	}
	if got, want := WorkspaceRootFor("vyrwu/atelier"), filepath.Join(base, "vyrwu/atelier"); got != want {
		t.Errorf("WorkspaceRootFor = %q, want %q", got, want)
	}
}
