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
	"os"
	"strings"

	"github.com/vyrwu/atelier/internal/config"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/workspace"
)

// workspaceCodeRoot is the directory the repo index is scanned from
// (owner/repo checkouts). Delegates to the workspace primitive's single
// source of truth (which honors $ATELIER_CODE_ROOT then [workspaces] code_root).
func workspaceCodeRoot() string { return workspace.CodeRootBase() }

// workspaceWorktreeRoot is where git worktrees are materialized (repo-local git
// bookkeeping): <root>/<owner>/<repo>/<branch>. Delegates to the primitive.
func workspaceWorktreeRoot() string { return workspace.WorktreeRootBase() }

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
