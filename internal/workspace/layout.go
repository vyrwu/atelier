package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vyrwu/atelier/internal/config"
	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// WorkspaceRootBase returns the base directory workspaces live under
// (default ~/ateliers). Honors $ATELIER_WORKSPACE_ROOT (tests/one-offs) and the
// `[workspaces] workspace_root` config key. The primitive owns this path policy
// so the creator, restore, and the CLI verbs all agree.
func WorkspaceRootBase() string {
	if v := os.Getenv("ATELIER_WORKSPACE_ROOT"); v != "" {
		return v
	}
	var cfg struct {
		WorkspaceRoot string `toml:"workspace_root"`
	}
	_ = config.LoadSection("workspaces", &cfg)
	if cfg.WorkspaceRoot != "" {
		return config.ExpandPath(cfg.WorkspaceRoot)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "ateliers")
}

// WorkspaceRootFor returns the dedicated directory for a workspace slug:
// <base>/<slug>. Slashes in the slug (a migrated "auto/foo") nest as subdirs.
func WorkspaceRootFor(slug string) string {
	return filepath.Join(WorkspaceRootBase(), slug)
}

// CodeRootBase returns the directory owner/repo checkouts live under (the repo
// index scanned at M-n). Honors $ATELIER_CODE_ROOT then [workspaces] code_root,
// default ~/code/github. Single source of truth — the workspaces tool and the
// agent CLI verbs both resolve through here.
func CodeRootBase() string {
	if v := os.Getenv("ATELIER_CODE_ROOT"); v != "" {
		return v
	}
	var cfg struct {
		CodeRoot string `toml:"code_root"`
	}
	_ = config.LoadSection("workspaces", &cfg)
	if cfg.CodeRoot != "" {
		return config.ExpandPath(cfg.CodeRoot)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "code", "github")
}

// WorktreeRootBase returns the directory git worktrees are materialized under
// (<root>/<owner>/<repo>/<flat-branch>, where the branch leaf is flattened by
// WorktreeDirName so worktrees within a repo stay a flat list). Honors
// $ATELIER_WORKTREE_ROOT then [workspaces] worktree_root, default
// ~/code/.worktrees/github. Single source of truth alongside CodeRootBase /
// WorkspaceRootBase.
func WorktreeRootBase() string {
	if v := os.Getenv("ATELIER_WORKTREE_ROOT"); v != "" {
		return v
	}
	var cfg struct {
		WorktreeRoot string `toml:"worktree_root"`
	}
	_ = config.LoadSection("workspaces", &cfg)
	if cfg.WorktreeRoot != "" {
		return config.ExpandPath(cfg.WorktreeRoot)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "code", ".worktrees", "github")
}

// DeleteWorkspace tears a workspace down: remove each of its git worktrees +
// their symlinks, delete the (now link-only) workspace root, kill the tmux
// session, and drop the statestore record. The workspace-lifecycle verbs
// (kill-session, worktree removal, the link tree) all belong to the primitive —
// a tool must not inline them (CLAUDE.md). The caller (the M-s picker) is
// responsible only for the tool-specific concerns around it: moving the outer
// client to a sibling first (so the picker survives) and cleaning orphaned
// popups after. Best-effort per step; a single failure is logged, not fatal.
func DeleteWorkspace(h *tmuxhost.Client, session string) error {
	if session == "" {
		return fmt.Errorf("workspace.DeleteWorkspace: empty session")
	}
	if st, err := statestore.Load(); err == nil && st != nil {
		if ws := st.FindWorkspace(session); ws != nil {
			for _, wt := range ws.Worktrees {
				repoPath := filepath.Join(CodeRootBase(), wt.Repo)
				if err := RemoveWorktreeDir(repoPath, wt.Path); err != nil {
					debuglog.LogErr("workspace.DeleteWorkspace: remove worktree "+wt.Path, err)
				}
				UnlinkWorktree(wt.Link)
			}
			if ws.Root != "" {
				if err := os.RemoveAll(ws.Root); err != nil {
					debuglog.LogErr("workspace.DeleteWorkspace: remove root "+ws.Root, err)
				}
			}
		}
	}
	if has, _ := h.HasSession(session); has {
		if err := h.KillSession(session); err != nil {
			debuglog.LogErr("workspace.DeleteWorkspace: kill-session "+session, err)
		}
	}
	return statestore.RemoveSession(session)
}

// This file owns the workspace's on-disk layout: the dedicated root directory
// (~/ateliers/<slug>), the git worktrees the agent produces, and the symlink
// tree that projects those worktrees into the root as <repo>/<branch> — exactly
// as the drawing's `ls` shows. Git worktrees stay repo-local (git bookkeeping
// unchanged); only the symlinks live under the root.
//
// It also owns the path-canonicalization policy (WS-2's landmine): a symlinked
// cwd hashes to a different Claude project dir than its real path, so any path
// that reaches an AI adapter goes through CanonicalPath first.

// CanonicalPath resolves symlinks so a path reaching an AI adapter (which
// derives its per-project transcript dir by hashing the cwd) always hashes the
// REAL path, never a symlink into the workspace root. Falls back to the input
// when the path doesn't exist yet (EvalSymlinks errors on a missing path).
//
// Policy (WS-2): the driver agent already runs from the REAL workspace root, so
// this is defense-in-depth for any flow that hands a possibly-symlinked path to
// an adapter (recap, attention, --resume).
func CanonicalPath(p string) string {
	if p == "" {
		return p
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// EnsureWorkspaceRoot creates the workspace's dedicated directory. Idempotent.
func EnsureWorkspaceRoot(root string) error {
	if root == "" {
		return fmt.Errorf("workspace.EnsureWorkspaceRoot: empty root")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("workspace.EnsureWorkspaceRoot: %w", err)
	}
	return nil
}

// WorktreeDirName flattens a branch name into a SINGLE path segment for the
// worktree's on-disk directory and its symlink under the workspace root: a
// slashed branch ("feat/x") lands as a flat dir ("feat-x") instead of nesting
// (repo/feat/x). The git branch name itself is untouched — only the directory
// leaf is flattened. Pure. Callers that build a worktree path or link leaf
// from a branch MUST route it through here so worktrees within a repo stay
// flat. Empty → empty.
func WorktreeDirName(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// WorktreeLinkPath is the symlink path a worktree gets under the workspace root:
// <root>/<repo-name>/<flat-branch>. repo-name is the last segment of the
// "owner/repo" slug — the drawing shows "helm-charts/feat-x", not
// "wawa/helm-charts/feat/x". The branch is flattened (WorktreeDirName) so
// worktrees within a repo are a flat list, never nested under feat/ etc.
func WorktreeLinkPath(root, repo, branch string) string {
	return filepath.Join(root, filepath.Base(repo), WorktreeDirName(branch))
}

// LinkWorktree (re)creates the symlink <root>/<repo>/<branch> → wtPath. Replaces
// a stale/wrong link in place. Best-effort parent mkdir. Returns the link path.
func LinkWorktree(root, wtPath, repo, branch string) (string, error) {
	link := WorktreeLinkPath(root, repo, branch)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return "", fmt.Errorf("workspace.LinkWorktree mkdir: %w", err)
	}
	// Replace an existing link (or wrong target) so repair is idempotent.
	if cur, err := os.Readlink(link); err == nil {
		if cur == wtPath {
			return link, nil
		}
		_ = os.Remove(link)
	} else if _, err := os.Lstat(link); err == nil {
		// A non-symlink squatting the path — remove only if it's a symlink we
		// own; a real dir/file there is left alone (don't nuke user data).
		return link, fmt.Errorf("workspace.LinkWorktree: %s exists and is not a symlink", link)
	}
	if err := os.Symlink(wtPath, link); err != nil {
		return "", fmt.Errorf("workspace.LinkWorktree symlink: %w", err)
	}
	return link, nil
}

// UnlinkWorktree removes a worktree's symlink from the workspace root (only if
// it is a symlink). Best-effort.
func UnlinkWorktree(link string) {
	if link == "" {
		return
	}
	if _, err := os.Readlink(link); err == nil {
		_ = os.Remove(link)
	}
}

// AddWorktree materializes repoPath@branch as a git worktree at wtPath, symlinks
// it into the workspace root, and registers it on the workspace's statestore
// record. Idempotent: an existing worktree/branch is reused (no re-`worktree
// add`). This is THE primitive both the M-n creator and `atelier workspace
// worktree add` (MCP) call — one code path so a fix lands once.
func AddWorktree(h *tmuxhost.Client, session, root, repoPath, repo, branch, wtPath string) (statestore.Worktree, error) {
	if repoPath == "" || branch == "" || wtPath == "" {
		return statestore.Worktree{}, fmt.Errorf("workspace.AddWorktree: repoPath, branch, wtPath required")
	}
	if err := EnsureWorkspaceRoot(root); err != nil {
		return statestore.Worktree{}, err
	}
	// Create the worktree unless it (or a same-named worktree) already exists.
	if existing := worktreePathForBranch(repoPath, branch); existing != "" {
		wtPath = existing
	} else {
		_ = os.MkdirAll(filepath.Dir(wtPath), 0o755)
		defaultBranch, _ := computeDefaultBranch(repoPath)
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		base := resolveWorktreeBase(repoPath, branch, defaultBranch)
		if err := runGit(repoPath, "worktree", "add", wtPath, "-b", branch, base); err != nil {
			// A branch that exists but isn't a worktree: check it out without -b.
			if branchExists(repoPath, branch) {
				if err2 := runGit(repoPath, "worktree", "add", wtPath, branch); err2 != nil {
					return statestore.Worktree{}, fmt.Errorf("workspace.AddWorktree: %w", err)
				}
			} else {
				return statestore.Worktree{}, fmt.Errorf("workspace.AddWorktree: %w", err)
			}
		}
	}
	link, err := LinkWorktree(root, wtPath, repo, branch)
	if err != nil {
		debuglog.LogErr("workspace.AddWorktree: link", err)
	}
	wt := statestore.Worktree{Repo: repo, Branch: branch, Path: wtPath, Link: link}
	registerWorktree(session, wt)
	return wt, nil
}

// registerWorktree appends (or updates in place) a worktree on the workspace's
// statestore record so restore + the M-c view see it. Best-effort.
func registerWorktree(session string, wt statestore.Worktree) {
	_ = statestore.UpdateWorkspace(session, func(ws *statestore.Workspace) {
		for i := range ws.Worktrees {
			if ws.Worktrees[i].Repo == wt.Repo && ws.Worktrees[i].Branch == wt.Branch {
				ws.Worktrees[i] = wt
				return
			}
		}
		ws.Worktrees = append(ws.Worktrees, wt)
	})
}

// RemoveWorktreeDir removes a git worktree: the clean `git worktree remove
// --force` first (updates the repo's worktree registration), falling back to a
// direct rm -rf when the main repo is gone (git can't chdir into it).
func RemoveWorktreeDir(repoPath, wtPath string) error {
	if _, err := os.Stat(repoPath); err == nil {
		if err := runGit(repoPath, "worktree", "remove", "--force", wtPath); err == nil {
			return nil
		}
	}
	if err := os.RemoveAll(wtPath); err != nil {
		return fmt.Errorf("workspace.RemoveWorktreeDir (fallback rm -rf): %w", err)
	}
	return nil
}

// LayoutFix records a repair the layout reconcile made (or would make).
type LayoutFix struct {
	Code    string // "relinked" | "dangling" | "gc-link"
	Subject string // the link or worktree path
	Detail  string
}

// ReconcileLayout repairs a workspace's symlink tree against its worktree
// records: re-creates a missing link whose worktree is live (relinked), reports
// a worktree whose real dir is gone (dangling — VDanglingWorktreeLink), and
// GC's a stale symlink under the root that no record backs. Idempotent; safe to
// run on every restore + reconcile tick.
func ReconcileLayout(root string, worktrees []statestore.Worktree) []LayoutFix {
	var fixes []LayoutFix
	if root == "" {
		return nil
	}
	valid := map[string]bool{}
	for _, wt := range worktrees {
		// Key `valid` on the CANONICAL link path — the same path LinkWorktree
		// writes — not the possibly-stale persisted wt.Link. Otherwise, when a
		// record's Link diverges from the canonical path (a changed root, a
		// migrated record), the relink below would write the canonical path
		// while `valid` held the old one, and the GC sweep would delete the
		// symlink we just created.
		link := WorktreeLinkPath(root, wt.Repo, wt.Branch)
		valid[link] = true
		if wt.Path == "" || !pathExists(wt.Path) {
			fixes = append(fixes, LayoutFix{"dangling", wt.Path, "worktree directory no longer exists"})
			continue
		}
		if cur, err := os.Readlink(link); err != nil || cur != wt.Path {
			if _, err := LinkWorktree(root, wt.Path, wt.Repo, wt.Branch); err == nil {
				fixes = append(fixes, LayoutFix{"relinked", link, "re-created missing/stale worktree symlink"})
			}
		}
	}
	// GC: remove symlinks under the root that no worktree record backs.
	for _, orphan := range orphanLinks(root, valid) {
		UnlinkWorktree(orphan)
		fixes = append(fixes, LayoutFix{"gc-link", orphan, "removed symlink with no backing worktree record"})
	}
	return fixes
}

// orphanLinks walks the workspace root two levels deep (<repo>/<branch> and
// <repo>/<branch-part>/<rest>) collecting symlinks not present in `valid`.
func orphanLinks(root string, valid map[string]bool) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if !valid[p] {
				out = append(out, p)
			}
			return nil
		}
		return nil
	})
	return out
}

// --- git worktree mechanics (moved from the workspaces tool so the primitive,
// not the tool, owns worktree lifecycle per CLAUDE.md) ------------------------

// computeDefaultBranch returns the repo's default branch: origin/HEAD's target,
// else origin/{main,master}, else the local HEAD's branch name.
func computeDefaultBranch(repoPath string) (string, error) {
	if out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		ref := strings.TrimSpace(string(out))
		if i := strings.Index(ref, "/"); i >= 0 {
			return ref[i+1:], nil
		}
		return ref, nil
	}
	for _, name := range []string{"main", "master"} {
		if err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name).Run(); err == nil {
			return name, nil
		}
	}
	if out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("computeDefaultBranch: no default branch in %s", repoPath)
}

// branchExists reports whether repoPath has a local branch named `branch`.
func branchExists(repoPath, branch string) bool {
	return exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// remoteBranchExists reports whether origin has a branch named `branch`.
func remoteBranchExists(repoPath, branch string) bool {
	out, err := exec.Command("git", "-C", repoPath, "ls-remote", "--heads", "origin", branch).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// resolveWorktreeBase chooses the git ref to base a new worktree on: origin's
// branch of the same name (fetched first) when it exists, else origin/<default>.
func resolveWorktreeBase(repoPath, branch, defaultBranch string) string {
	if remoteBranchExists(repoPath, branch) {
		if err := runGit(repoPath, "fetch", "origin", branch); err == nil {
			return "origin/" + branch
		}
	}
	return "origin/" + defaultBranch
}

// worktreePathForBranch returns the existing worktree path for `branch`, or "".
func worktreePathForBranch(repoPath, branch string) string {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	var curPath string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			curPath = strings.TrimPrefix(line, "worktree ")
		}
		if line == "branch refs/heads/"+branch {
			return curPath
		}
	}
	return ""
}
