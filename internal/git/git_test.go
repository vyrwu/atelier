package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mkWorktree fakes what git leaves behind: a linked worktree is marked by a
// regular `.git` FILE, where a normal clone has a `.git` directory.
func mkWorktree(t *testing.T, root string, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /somewhere/.git/worktrees/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Worktrees is the whole "worktrees derive from disk, never from stored state"
// rule: the workspace directory is the record.
func TestWorktrees(t *testing.T) {
	root := t.TempDir()
	mkWorktree(t, root, "owner-repo", "feature-a")
	mkWorktree(t, root, "owner-repo", "feature-b")
	mkWorktree(t, root, "owner-other", "fix")

	// A plain clone (.git as a DIRECTORY) is not a linked worktree.
	clone := filepath.Join(root, "owner-repo", "a-clone")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// So is a directory with no .git at all.
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := Worktrees(root)
	if len(got) != 3 {
		t.Fatalf("found %d worktrees, want 3: %+v", len(got), got)
	}

	// Sorted by repo then branch, so the view's grouping is stable.
	wantRepo := []string{"owner-other", "owner-repo", "owner-repo"}
	for i, w := range got {
		if w.Repo != wantRepo[i] {
			t.Errorf("[%d] repo = %q, want %q", i, w.Repo, wantRepo[i])
		}
		if w.Path == "" || !filepath.IsAbs(w.Path) {
			t.Errorf("[%d] path = %q, want an absolute path", i, w.Path)
		}
		// No branch is resolvable from a fake worktree, so the directory name
		// stands in — a worktree must never render as a blank row.
		if w.Branch == "" {
			t.Errorf("[%d] branch is empty", i)
		}
		if w.Created.IsZero() {
			t.Errorf("[%d] created is zero, want the .git file's mtime", i)
		}
	}
	if got[1].Branch != "feature-a" || got[2].Branch != "feature-b" {
		t.Errorf("branches = %q, %q, want them sorted", got[1].Branch, got[2].Branch)
	}
}

// Nested worktrees are a git impossibility, and descending into one would walk
// the whole checkout on every keypress.
func TestWorktreesDoesNotRecurse(t *testing.T) {
	root := t.TempDir()
	outer := mkWorktree(t, root, "owner-repo", "feature")
	mkWorktree(t, outer, "vendor", "inner")

	if got := Worktrees(root); len(got) != 1 {
		t.Fatalf("found %d worktrees, want only the outer one: %+v", len(got), got)
	}
}

// Best-effort by contract: a missing or empty root is not an error, it is just
// no worktrees — a brand-new space must render, not fail.
func TestWorktreesMissingRoot(t *testing.T) {
	if got := Worktrees(filepath.Join(t.TempDir(), "does-not-exist")); got != nil {
		t.Errorf("missing root = %+v, want nil", got)
	}
	if got := Worktrees(t.TempDir()); got != nil {
		t.Errorf("empty root = %+v, want nil", got)
	}
}

func TestIsWorktreeDir(t *testing.T) {
	root := t.TempDir()
	wt := mkWorktree(t, root, "wt")

	clone := filepath.Join(root, "clone")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	emptyGit := filepath.Join(root, "cache")
	if err := os.MkdirAll(emptyGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emptyGit, ".git"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		dir  string
		want bool
	}{
		"worktree":    {wt, true},
		"empty .git":  {emptyGit, false},
		"clone":       {clone, false},
		"plain-dir":   {root, false},
		"nonexistent": {filepath.Join(root, "nope"), false},
	}
	for name, c := range cases {
		if got := isWorktreeDir(c.dir); got != c.want {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

// Repo is the <owner-repo> directory that owns the worktree — the first segment
// under the workspace root, however deep the worktree sits.
func TestRepoSegment(t *testing.T) {
	cases := []struct {
		root, path, want string
	}{
		{"/ws", "/ws/owner-repo/branch", "owner-repo"},
		{"/ws", "/ws/owner-repo", "owner-repo"},
		{"/ws", "/ws/owner-repo/nested/deep", "owner-repo"},
		{"/ws", "/elsewhere/thing", "thing"}, // not under root — fall back to the base name
	}
	for _, c := range cases {
		if got := repoSegment(c.root, c.path); got != c.want {
			t.Errorf("repoSegment(%q, %q) = %q, want %q", c.root, c.path, got, c.want)
		}
	}
}

// The git queries answer "" / false rather than erroring when pointed at
// something that is not a repo — the views call them on every row.
func TestGitQueriesOnNonRepo(t *testing.T) {
	dir := t.TempDir()
	if got := CurrentBranch(dir); got != "" {
		t.Errorf("CurrentBranch = %q, want empty", got)
	}
	if _, _, ok := AheadBehind(dir); ok {
		t.Error("AheadBehind claimed a result outside a repo")
	}
	if IsDirty(dir) {
		t.Error("IsDirty = true outside a repo")
	}
	if defaultRef(dir) != "" {
		t.Error("defaultRef found a default branch outside a repo")
	}
}

// The PR query addresses repos by owner/name, and a worktree directory carries
// only the repo's base name — so the owner comes from its origin remote.
func TestRepoFromRemoteURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:owner/repo.git":             "owner/repo",
		"git@github.com:owner/repo":                 "owner/repo",
		"ssh://git@github.com/owner/repo.git":       "owner/repo",
		"https://github.com/owner/repo.git":         "owner/repo",
		"https://github.com/owner/repo":             "owner/repo",
		"https://user@github.com/owner/repo.git":    "owner/repo",
		"https://github.com/owner/repo/":            "owner/repo",
		"git@github.enterprise.internal:team/thing": "team/thing",
		"  git@github.com:owner/repo.git\n":         "owner/repo",
		"/local/path/to/repo.git":                   "to/repo",
		"":                                          "",
		"garbage":                                   "",
	}
	for in, want := range cases {
		if got := RepoFromRemoteURL(in); got != want {
			t.Errorf("RepoFromRemoteURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// OriginRepo is best-effort: no repo, or no origin, is "" rather than an error.
func TestOriginRepo(t *testing.T) {
	dir := t.TempDir()
	if got := OriginRepo(dir); got != "" {
		t.Errorf("outside a repo: %q, want empty", got)
	}

	run := func(args ...string) {
		t.Helper()
		if err := exec.Command("git", append([]string{"-C", dir}, args...)...).Run(); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}
	run("init", "-q")
	if got := OriginRepo(dir); got != "" {
		t.Errorf("no origin: %q, want empty", got)
	}
	run("remote", "add", "origin", "git@github.com:vyrwu/atelier.git")
	if got, want := OriginRepo(dir), "vyrwu/atelier"; got != want {
		t.Errorf("OriginRepo = %q, want %q", got, want)
	}
}

// Deleting a space removes its worktrees and frees their branches, locked or not.
// A single --force refused a locked worktree, and the fallback's prune — which
// used to run inside the directory it had just deleted — could never clear a
// locked record either, so the branch stayed "already used by worktree" for good.
func TestRemoveWorktreeFreesTheBranch(t *testing.T) {
	src := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(src, "init", "-q", "-b", "main")
	run(src, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(t.TempDir(), "wt")
	run(src, "worktree", "add", "-q", "-b", "feat", wt)
	run(src, "worktree", "lock", wt) // a single --force refuses this

	if err := RemoveWorktree(wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree directory still there")
	}
	// The branch is free again only if the source repo's record was pruned.
	c := exec.Command("git", "-C", src, "worktree", "add", "-q", filepath.Join(t.TempDir(), "again"), "feat")
	if out, err := c.CombinedOutput(); err != nil {
		t.Errorf("branch still held by the deleted worktree: %s", out)
	}
}

// One directory that can't be read skips itself; the worktrees after it are
// still found.
func TestWorktreesSurvivesAnUnreadableDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "aaa-locked")
	if err := os.MkdirAll(filepath.Join(locked, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkWorktree(t, root, "zzz-repo", "feat")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if got := Worktrees(root); len(got) != 1 {
		t.Errorf("found %d worktrees past an unreadable directory, want 1", len(got))
	}
	if got := CountWorktrees(root); got != 1 {
		t.Errorf("counted %d worktrees past an unreadable directory, want 1", got)
	}
}

// A worktree's `.git` file points at its git directory. An empty one (uv marks
// its cache with one) is no worktree, while a repository whose name starts with
// '.' (an org's .github) still holds real ones.
func TestWorktreesNeedAGitdirPointer(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".uv-cache", "sdists-v9")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, ".git"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mkWorktree(t, root, ".github", "feat-x")

	if got := Worktrees(root); len(got) != 1 || got[0].Repo != ".github" {
		t.Errorf("worktrees = %+v, want only .github's", got)
	}
	if got := CountWorktrees(root); got != 1 {
		t.Errorf("counted %d worktrees, want 1", got)
	}
}

// Listing a space's worktrees spent a git process per worktree on its branch,
// over a second before the popup drew for a space with 60. A linked worktree's
// HEAD file names it; anything else is still asked of git.
func TestCurrentBranchReadsTheWorktreesHEAD(t *testing.T) {
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	src := t.TempDir()
	run(src, "init", "-q", "-b", "main")
	run(src, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(t.TempDir(), "src", "feat", "x")
	run(src, "worktree", "add", "-q", "-b", "feat/x", wt)

	if got := readHead(wt); got != "feat/x" {
		t.Errorf("readHead = %q, want feat/x", got)
	}
	// A tag of the same name makes git answer heads/feat/x; the HEAD file
	// doesn't, which shows CurrentBranch read it rather than ran git.
	run(src, "tag", "feat/x")
	if got := CurrentBranch(wt); got != "feat/x" {
		t.Errorf("CurrentBranch = %q, want feat/x", got)
	}

	run(wt, "checkout", "-q", "--detach")
	if got := readHead(wt); got != "HEAD" {
		t.Errorf("detached readHead = %q, want HEAD", got)
	}
	if got := CurrentBranch(wt); got != "HEAD" {
		t.Errorf("detached CurrentBranch = %q, want HEAD", got)
	}

	// Not a linked worktree: git answers.
	if got := readHead(src); got != "" {
		t.Errorf("clone readHead = %q, want it left to git", got)
	}
	if got := CurrentBranch(src); got != "main" {
		t.Errorf("clone CurrentBranch = %q, want main", got)
	}
}

// A HEAD file names whatever it was written with; a name git would refuse, and
// a list row must not print, is left to git.
func TestReadHeadRefusesWhatGitWould(t *testing.T) {
	gitdir := t.TempDir()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a\x1b[31mb", "a b", "a..b", "a\nb", "a:b", ""} {
		if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readHead(wt); got != "" {
			t.Errorf("readHead(%q) = %q, want it left to git", name, got)
		}
	}
	// A symlinked HEAD is a ref to follow, which git does.
	ref := filepath.Join(gitdir, "other")
	if err := os.WriteFile(ref, []byte(strings.Repeat("a", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(gitdir, "HEAD"))
	if err := os.Symlink(ref, filepath.Join(gitdir, "HEAD")); err != nil {
		t.Fatal(err)
	}
	if got := readHead(wt); got != "" {
		t.Errorf("symlinked readHead = %q, want it left to git", got)
	}
}

// The reftable backend leaves a placeholder HEAD naming refs/heads/.invalid,
// which git sees past and the file can't; a SHA-256 repository's detached HEAD
// is a longer object name.
func TestReadHeadAcrossRepositoryFormats(t *testing.T) {
	run := func(dir string, args ...string) error {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v\n%s", args, err, out)
		}
		return nil
	}
	setup := func(t *testing.T, format string) string {
		t.Helper()
		src := t.TempDir()
		if err := run(src, "init", "-q", "-b", "main", format); err != nil {
			t.Skipf("git can't make this repository: %v", err)
		}
		wt := filepath.Join(t.TempDir(), "wt")
		for _, args := range [][]string{{"commit", "-q", "--allow-empty", "-m", "x"}, {"worktree", "add", "-q", "-b", "feat", wt}} {
			if err := run(src, args...); err != nil {
				t.Fatal(err)
			}
		}
		return wt
	}
	t.Run("reftable", func(t *testing.T) {
		wt := setup(t, "--ref-format=reftable")
		if got := readHead(wt); got != "" {
			t.Errorf("readHead = %q, want it left to git", got)
		}
		if got := CurrentBranch(wt); got != "feat" {
			t.Errorf("CurrentBranch = %q, want feat", got)
		}
	})
	t.Run("sha256", func(t *testing.T) {
		wt := setup(t, "--object-format=sha256")
		if err := run(wt, "checkout", "-q", "--detach"); err != nil {
			t.Fatal(err)
		}
		if got := readHead(wt); got != "HEAD" {
			t.Errorf("detached readHead = %q, want HEAD", got)
		}
	})
}

// git resolves a relative gitdir (worktree.useRelativePaths) from the
// worktree's real location, so must atelier when the spaces root is a symlink:
// joined onto the symlinked path, the pointer would climb somewhere else.
func TestRelativeGitdirThroughASymlinkedRoot(t *testing.T) {
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(base, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	run(src, "init", "-q", "-b", "main")
	run(src, "commit", "-q", "--allow-empty", "-m", "x")
	real := filepath.Join(base, "spaces", "calm-heron")
	wt := filepath.Join(real, "src", "feat")
	run(src, "worktree", "add", "-q", "-b", "feat", wt)
	rel, err := filepath.Rel(wt, filepath.Join(src, ".git", "worktrees", "feat"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "spaces")
	if err := os.Symlink(filepath.Dir(real), link); err != nil {
		t.Fatal(err)
	}

	via := filepath.Join(link, "calm-heron", "src", "feat")
	if got := readHead(via); got != "feat" {
		t.Errorf("readHead = %q, want feat", got)
	}
	if got := SourceGitDir(via); got != filepath.Join(src, ".git") {
		t.Errorf("SourceGitDir = %q, want %q", got, filepath.Join(src, ".git"))
	}
	if got := CountWorktrees(filepath.Join(link, "calm-heron")); got != 1 {
		t.Errorf("counted %d worktrees, want 1", got)
	}
}
