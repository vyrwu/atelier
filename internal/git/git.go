// Package git derives a workspace's worktrees from the filesystem and answers
// git queries via the "git" command. The workspace directory is the record:
// each linked worktree is a directory containing a `.git` FILE pointing at its
// git directory (git's marker for a linked worktree, versus a `.git` directory
// in a normal clone).
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// Worktrees walks workspaceRoot and returns every git worktree found beneath it
// (see walkWorktrees). For each, Path is the absolute directory, Branch is its
// current branch (falling back to the directory's base name), and Repo is the
// first path segment under workspaceRoot (the <repo> directory). The result is
// sorted by Repo then Branch. Best-effort: returns nil on any error or a missing
// root.
func Worktrees(workspaceRoot string) []core.Worktree {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil
	}
	var out []core.Worktree
	walkWorktrees(root, func(path string) {
		branch := CurrentBranch(path)
		if branch == "" {
			branch = filepath.Base(path)
		}
		var created time.Time
		if fi, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			created = fi.ModTime()
		}
		out = append(out, core.Worktree{
			Repo:    repoSegment(root, path),
			Branch:  branch,
			Path:    path,
			Created: created,
		})
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Branch < out[j].Branch
	})
	return out
}

// walkWorktrees calls fn with every worktree under root (see isWorktreeDir),
// without recursing into a worktree once one is found.
func walkWorktrees(root string, fn func(path string)) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// One unreadable directory skips itself, not the rest of the space.
			if path == root {
				return err
			}
			return filepath.SkipDir
		}
		if !d.IsDir() || path == root {
			return nil
		}
		if !isWorktreeDir(path) {
			return nil
		}
		fn(path)
		return filepath.SkipDir
	})
}

// isWorktreeDir reports whether dir directly contains a regular `.git` file
// pointing at a git directory, as a linked worktree's does. Some caches mark
// themselves with an empty `.git` file (uv's does), which is no worktree.
func isWorktreeDir(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && info.Mode().IsRegular() && gitFileDir(dir) != ""
}

// gitFileDir is the git directory the `.git` file in path points at
// ("gitdir: <dir>"), or "" when path has no such file. A relative pointer
// (worktree.useRelativePaths) is resolved from path's real location, as git
// resolves it, not through a symlink on the way there.
func gitFileDir(path string) string {
	// #nosec G304 -- Reads the .git metadata of a directory in the owner's space.
	data, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return ""
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(dir) {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return ""
		}
		dir = filepath.Join(real, dir)
	}
	return dir
}

// repoSegment returns the first path segment of path relative to root — the
// <repo> directory that owns the worktree. Falls back to the worktree's
// own base name if path is not under root.
func repoSegment(root, path string) string {
	rel, err := filepath.Rel(root, path)
	// Rel succeeds with a "../" path when path is outside root, so escaping the
	// root has to be checked for as well as an outright error.
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(path)
	}
	return strings.SplitN(rel, string(filepath.Separator), 2)[0]
}

// CurrentBranch returns the abbreviated current branch of the git worktree at
// path, "HEAD" when it is detached, or "" on error. A linked worktree's HEAD file
// is read directly: a git process per worktree cost a space with dozens of them
// over a second before its popup drew. Anything that file can't settle (a clone,
// the reftable backend's placeholder) is asked of git.
func CurrentBranch(path string) string {
	if b := readHead(path); b != "" {
		return b
	}
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	out, err := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// readHead is the branch the HEAD file of the linked worktree at path names, or
// "HEAD" when it is detached, as `git rev-parse --abbrev-ref HEAD` would print
// them — except that git qualifies a name another ref shares (heads/x) and
// errs on a branch with no commits yet, where this names the branch. "" when
// it isn't a linked worktree or the file names neither a branch nor a commit.
func readHead(path string) string {
	dir := gitFileDir(path)
	if dir == "" {
		return ""
	}
	// A symlinked HEAD (core.preferSymlinkRefs) is a ref, not its contents.
	file := filepath.Join(dir, "HEAD")
	if fi, err := os.Lstat(file); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	// #nosec G304 G703 -- Reads the HEAD of the owner's worktree, found from its .git file.
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if b, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		if b == ".invalid" || !plainRefName(b) {
			return ""
		}
		return b
	}
	if objectID(head) {
		return "HEAD"
	}
	return ""
}

// plainRefName rejects what git never allows in a ref name and a list row must
// not print: control characters, spaces, and ".." among them. git is asked
// about anything else odd.
func plainRefName(s string) bool {
	if s == "" || strings.Contains(s, "..") || strings.ContainsAny(s, " ~^:?*[\\") {
		return false
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// objectID reports whether s is a full SHA-1 or SHA-256 object name.
func objectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// AheadBehind reports how many commits the worktree at path is ahead of and
// behind its repo's default branch, comparing HEAD against the last-fetched
// origin/<default> (no network — fast, and origin is kept fresh by
// create_worktree/create_pr). ok is false when it can't be computed (detached
// HEAD, no origin default ref, not a repo).
func AheadBehind(path string) (ahead, behind int, ok bool) {
	def := defaultRef(path)
	if def == "" {
		return 0, 0, false
	}
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	out, err := exec.Command("git", "-C", path, "rev-list", "--left-right", "--count", def+"...HEAD").Output()
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(strings.TrimSpace(string(out)))
	if len(f) != 2 {
		return 0, 0, false
	}
	behind, _ = strconv.Atoi(f[0]) // left: in origin/<default> not in HEAD
	ahead, _ = strconv.Atoi(f[1])  // right: in HEAD not in origin/<default>
	return ahead, behind, true
}

// defaultRef returns the repo's default branch as a remote-tracking ref
// ("origin/main"), preferring origin/HEAD and falling back to origin/main or
// origin/master when it exists.
func defaultRef(path string) string {
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	if out, err := exec.Command("git", "-C", path, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	for _, c := range []string{"origin/main", "origin/master"} {
		// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
		if exec.Command("git", "-C", path, "rev-parse", "--verify", "--quiet", c).Run() == nil {
			return c
		}
	}
	return ""
}

// OriginRepo returns the worktree's origin remote as "owner/repo", or "" when
// there is no origin or it isn't a recognisable one. Local and instant (no
// network), which is what lets the PR refresh address repos by name.
func OriginRepo(path string) string {
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	out, err := exec.Command("git", "-C", path, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return RepoFromRemoteURL(strings.TrimSpace(string(out)))
}

// RepoFromRemoteURL pulls "owner/repo" out of a git remote URL — scp-style
// (git@host:owner/repo.git), ssh://, https://, with or without the .git suffix.
// It takes the last two path segments, so a self-hosted or enterprise host works
// the same as github.com.
func RepoFromRemoteURL(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), "/")
	u = strings.TrimSuffix(u, ".git")
	if u == "" {
		return ""
	}
	// scp-style has no scheme: everything after the first colon is the path.
	if !strings.Contains(u, "://") {
		if _, rest, ok := strings.Cut(u, ":"); ok {
			u = rest
		}
	}
	parts := strings.Split(u, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, repo := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || repo == "" || strings.Contains(owner, ":") {
		return ""
	}
	return owner + "/" + repo
}

// HeadOID is the commit the worktree at path has checked out, or "".
func HeadOID(path string) string {
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	out, err := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IsDirty reports whether the git worktree at path has uncommitted changes.
func IsDirty(path string) bool {
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	out, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// Unsaved says what in the worktree at path exists nowhere else: uncommitted
// changes, or commits no remote-tracking ref contains, unless HEAD is one of
// published — a pull request's head, which GitHub keeps after its branch is
// deleted. Initialized submodules are checked too: publishing a superproject
// does not publish their commits. It fails closed when git can't read the work.
// Empty means nothing would be lost.
func Unsaved(path string, published []string) string {
	// #nosec G204 -- Fixed git inspection; the owner's repository path is a separate argument, without a shell.
	out, err := exec.Command("git", "-C", path, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none").Output()
	if err != nil {
		return "unreadable"
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return "uncommitted changes"
	}
	// #nosec G204 -- Fixed git inspection; the owner's repository path is a separate argument, without a shell.
	out, err = exec.Command("git", "-C", path, "rev-list", "--count", "HEAD", "--not", "--remotes").Output()
	if err != nil {
		return "unreadable"
	}
	n := strings.TrimSpace(string(out))
	if n == "0" {
		return unsavedSubmodules(path)
	}
	head := HeadOID(path)
	for _, oid := range published {
		if head != "" && oid == head {
			return unsavedSubmodules(path)
		}
	}
	return n + " unpushed commit(s)"
}

func unsavedSubmodules(path string) string {
	// Read gitlinks from the index with NUL delimiters so paths containing
	// spaces, tabs, or newlines remain intact. Uninitialized modules hold no
	// checkout to remove, so only follow ones with their own .git entry.
	// #nosec G204 -- Fixed git inspection; the owner's repository path is a separate argument, without a shell.
	out, err := exec.Command("git", "-C", path, "ls-files", "--stage", "-z").Output()
	if err != nil {
		return "unreadable submodules"
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		if !strings.HasPrefix(entry, "160000 ") {
			continue
		}
		_, name, ok := strings.Cut(entry, "\t")
		if !ok {
			return "unreadable submodules"
		}
		module := filepath.Join(path, name)
		if _, err := os.Lstat(filepath.Join(module, ".git")); os.IsNotExist(err) {
			continue
		}
		if why := Unsaved(module, nil); why != "" {
			return "submodule " + name + ": " + why
		}
	}
	return ""
}

// SourceGitDir is the git directory of the repository the worktree at path
// belongs to, read off its `.git` file ("gitdir: <repo>/.git/worktrees/<name>")
// without running git — the file outlives a source repo that has since moved.
// "" if path isn't a linked worktree.
func SourceGitDir(path string) string {
	dir := gitFileDir(path)
	if filepath.Base(filepath.Dir(dir)) != "worktrees" {
		return ""
	}
	return filepath.Dir(filepath.Dir(dir))
}

// PruneWorktrees drops gitDir's records of worktrees whose directories are gone
// and returns what it pruned; with dry set it only reports.
func PruneWorktrees(gitDir string, dry bool) ([]string, error) {
	args := []string{"--git-dir", gitDir, "worktree", "prune", "--verbose"}
	if dry {
		args = append(args, "--dry-run")
	}
	// #nosec G204 -- Fixed worktree pruning; the owner's git directory is a separate argument, without a shell.
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree prune in %s: %w (%s)", gitDir, err, strings.TrimSpace(string(out)))
	}
	var pruned []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			pruned = append(pruned, l)
		}
	}
	return pruned, nil
}

// RemoveWorktree removes the git worktree at path via `git worktree remove
// --force`. If that fails (e.g. corrupt bookkeeping), it falls back to deleting
// the directory outright and pruning stale worktree entries best-effort.
func RemoveWorktree(path string) error {
	// The source repo is found before anything is deleted: the prune below has to
	// run there, and once the worktree is gone it can't be reached through it.
	common := ""
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	if out, err := exec.Command("git", "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		common = strings.TrimSpace(string(out))
	}
	// Forced twice: once for local changes, twice for a lock. Deleting a space is
	// confirmed first, and its worktrees go with it either way — a single force
	// refused a locked one, and the fallback below can't prune a locked record.
	// #nosec G204 G702 -- Fixed git operations; repository paths and refs are separate arguments, without a shell.
	if err := exec.Command("git", "-C", path, "worktree", "remove", "--force", "--force", path).Run(); err == nil {
		return nil
	}
	rmErr := os.RemoveAll(path)
	if common != "" {
		_ = exec.Command("git", "--git-dir", common, "worktree", "prune").Run()
	}
	return rmErr
}

// CountWorktrees counts the worktrees under workspaceRoot without resolving any
// branch names, which the space lists never show — they show how many there are.
func CountWorktrees(workspaceRoot string) int { return len(WorktreeDirs(workspaceRoot)) }

// WorktreeDirs is the path of every worktree under workspaceRoot, found the way
// Worktrees finds them but without reading their branches.
func WorktreeDirs(workspaceRoot string) []string {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil
	}
	var dirs []string
	walkWorktrees(root, func(path string) { dirs = append(dirs, path) })
	return dirs
}
