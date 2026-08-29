package workspaces

import (
	"os"
	"path/filepath"

	"github.com/vyrwu/atelier/internal/config"
)

// Config is the workspaces plugin's own config, under the `[workspaces]`
// section of $XDG_CONFIG_HOME/atelier/config.toml.
type Config struct {
	// CodeRoot is where owner/repo checkouts live — the repo index scanned by
	// intent-first creation to pick which repos an intent touches.
	CodeRoot string `toml:"code_root"`
	// WorktreeRoot is where git worktrees are materialized (repo-local git
	// bookkeeping): <root>/<owner>/<repo>/<branch>. They are symlinked into the
	// per-workspace root.
	WorktreeRoot string `toml:"worktree_root"`
	// WorkspaceRoot is the base for per-workspace dedicated directories:
	// <workspace_root>/<slug>. Worktrees symlink into it as <repo>/<branch> and
	// the driver agent runs from it. Default ~/ateliers.
	WorkspaceRoot string `toml:"workspace_root"`
	// AutoTag controls whether creation asks the AI to also suggest a grouping
	// tag alongside the workspace name. Default true; M-t always overrides.
	AutoTag bool `toml:"auto_tag"`
}

func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		CodeRoot:      filepath.Join(home, "code", "github"),
		WorktreeRoot:  filepath.Join(home, "code", ".worktrees", "github"),
		WorkspaceRoot: filepath.Join(home, "ateliers"),
		AutoTag:       true,
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := config.LoadSection("workspaces", &cfg); err != nil {
		return cfg, err
	}
	cfg.CodeRoot = config.ExpandPath(cfg.CodeRoot)
	cfg.WorktreeRoot = config.ExpandPath(cfg.WorktreeRoot)
	cfg.WorkspaceRoot = config.ExpandPath(cfg.WorkspaceRoot)
	return cfg, nil
}
