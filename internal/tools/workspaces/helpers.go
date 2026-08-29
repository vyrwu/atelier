// Package workspaces is the atelier workspaces tool — the intent-first
// workspace creator (M-n), the aggregate workspace picker (M-s), and the
// cross-repo Changes view (M-c). A workspace is an INTENT: one driver agent
// running at a dedicated root directory into which git worktrees are symlinked.
//
// The heavy lifting — session/window lifecycle, the worktree symlink layout,
// workspace identity — lives in the internal/workspace primitive; this package
// owns only the fzf-driven UX (pickers, binds, transforms) on top of it.
package workspaces

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/config"
	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/perf"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// workspaceCodeRoot is the directory the repo index is scanned from
// (owner/repo checkouts). Honors $ATELIER_CODE_ROOT then [workspaces] code_root.
func workspaceCodeRoot() string {
	if v := os.Getenv("ATELIER_CODE_ROOT"); v != "" {
		return v
	}
	cfg, _ := LoadConfig()
	return cfg.CodeRoot
}

// workspaceWorktreeRoot is where git worktrees are materialized (repo-local git
// bookkeeping): <root>/<owner>/<repo>/<branch>. Symlinked into the workspace
// root. Honors $ATELIER_WORKTREE_ROOT then [workspaces] worktree_root.
func workspaceWorktreeRoot() string {
	if v := os.Getenv("ATELIER_WORKTREE_ROOT"); v != "" {
		return v
	}
	cfg, _ := LoadConfig()
	return cfg.WorktreeRoot
}

// forgeActive reports whether a forge integration is installed.
func forgeActive() bool { return integration.Active().Forge != nil }

// forgeWriteAllowed reports whether mutating forge ops (PR close) are permitted.
// Gated by [forge] allow_write (default true) so a user can make atelier's forge
// integration strictly read-only.
func forgeWriteAllowed() bool {
	if !forgeActive() {
		return false
	}
	var cfg struct {
		AllowWrite *bool `toml:"allow_write"`
	}
	_ = config.LoadSection("forge", &cfg)
	return cfg.AllowWrite == nil || *cfg.AllowWrite
}

// agentAutoOpenSkipped reports whether the deferred agent auto-open should be
// skipped — true in e2e test contexts (atelier-test-* sockets): the detached
// popup process races t.TempDir cleanup, and tests assert landing/state, not
// the popup.
func agentAutoOpenSkipped() bool {
	return strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-")
}

// DefaultBranch returns the repo's default branch (origin/HEAD → main → master
// → "main").
func DefaultBranch(repoPath string) string {
	out := runGitQuiet(repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if out != "" {
		if i := strings.Index(out, "/"); i >= 0 {
			return out[i+1:]
		}
		return out
	}
	for _, b := range []string{"main", "master"} {
		if runGitQuiet(repoPath, "rev-parse", "--verify", b) != "" {
			return b
		}
	}
	return "main"
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	debuglog.LogGitCmd(dir, args, errBuf.Bytes(), err, dur)
	perf.Add("git", dur)
	if err != nil {
		return err
	}
	return nil
}

func runGitQuiet(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	start := time.Now()
	out, err := cmd.Output()
	dur := time.Since(start)
	debuglog.LogGitCmd(dir, args, out, err, dur)
	perf.Add("git", dur)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getSessionWorkspaceRoot returns a session's @workspace_root, or "" if unset.
func getSessionWorkspaceRoot(h *tmuxhost.Client, session string) string {
	out, _ := h.Run("show-option", "-t", session, "-qv", workspace.OptWorkspaceRoot)
	return strings.TrimSpace(string(out))
}

// repoSlugFromPath recovers "owner/repo" from a repo path's last two segments,
// preserving characters (like '.') that tmux strips from session names.
func repoSlugFromPath(repoPath string) string {
	if repoPath == "" {
		return ""
	}
	repo := filepath.Base(repoPath)
	owner := filepath.Base(filepath.Dir(repoPath))
	if repo == "" || repo == "." || repo == string(filepath.Separator) ||
		owner == "" || owner == "." || owner == string(filepath.Separator) {
		return ""
	}
	return owner + "/" + repo
}

// dropPrompts is retained for callsite compatibility (fzfstyle.Args emits one
// --prompt=). Returns args unchanged.
func dropPrompts(args []string) []string { return args }
