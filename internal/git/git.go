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
	"strings"

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
		if err != nil || !d.IsDir() || path == root {
			return err
		}
		if !isWorktreeDir(path) {
			return nil
		}
		branch := CurrentBranch(path)
		if branch == "" {
			branch = filepath.Base(path)
		}
		out = append(out, core.Worktree{
			Repo:   repoSegment(root, path),
			Branch: branch,
			Path:   path,
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
	if err != nil {
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
	if err := exec.Command("git", "-C", path, "worktree", "remove", "--force", path).Run(); err == nil {
		return nil
	}
	rmErr := os.RemoveAll(path)
	_ = exec.Command("git", "-C", path, "worktree", "prune").Run()
	return rmErr
}
