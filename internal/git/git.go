// Package git derives a workspace's worktrees from the filesystem and answers
// git queries via the "git" command. The workspace directory is the record:
// each linked worktree is a directory containing a `.git` FILE (git's marker
// for a linked worktree, versus a `.git` directory in a normal clone).
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// Worktrees walks workspaceRoot and returns every git worktree found beneath it.
// A worktree is any directory that directly contains a regular `.git` file. For
// each, Path is the absolute directory, Branch is its current branch (falling
// back to the directory's base name), and Repo is the first path segment under
// workspaceRoot (the <owner-repo> directory). The result is sorted by Repo then
// Branch. Best-effort: returns nil on any error or a missing root, and does not
// recurse into a worktree once one is found.
func Worktrees(workspaceRoot string) []core.Worktree {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil
	}
	var out []core.Worktree
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
		return filepath.SkipDir // don't recurse into a worktree
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Branch < out[j].Branch
	})
	return out
}

// isWorktreeDir reports whether dir directly contains a regular `.git` file.
func isWorktreeDir(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && info.Mode().IsRegular()
}

// repoSegment returns the first path segment of path relative to root — the
// <owner-repo> directory that owns the worktree. Falls back to the worktree's
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
// path, or "" on error (e.g. detached HEAD or not a repo).
func CurrentBranch(path string) string {
	out, err := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
	if out, err := exec.Command("git", "-C", path, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	for _, c := range []string{"origin/main", "origin/master"} {
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
	out, err := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IsDirty reports whether the git worktree at path has uncommitted changes.
func IsDirty(path string) bool {
	out, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// RemoveWorktree removes the git worktree at path via `git worktree remove
// --force`. If that fails (e.g. corrupt bookkeeping), it falls back to deleting
// the directory outright and pruning stale worktree entries best-effort.
func RemoveWorktree(path string) error {
	// The source repo is found before anything is deleted: the prune below has to
	// run there, and once the worktree is gone it can't be reached through it.
	common := ""
	if out, err := exec.Command("git", "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		common = strings.TrimSpace(string(out))
	}
	// Forced twice: once for local changes, twice for a lock. Deleting a space is
	// confirmed first, and its worktrees go with it either way — a single force
	// refused a locked one, and the fallback below can't prune a locked record.
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
// branch names. Worktrees() spends a `git rev-parse` per worktree purely to fill
// in Branch, which the space lists never show — they show how many there are.
func CountWorktrees(workspaceRoot string) int {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return 0
	}
	n := 0
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
		n++
		return filepath.SkipDir
	})
	return n
}
