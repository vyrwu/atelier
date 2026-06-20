package workspaces

import (
	"os"
	"path/filepath"

	"github.com/vyrwu/atelier/internal/config"
)

// Config is the workspaces plugin's own config. Lives under the
// `[workspaces]` section of $XDG_CONFIG_HOME/atelier/config.toml.
type Config struct {
	CodeRoot      string `toml:"code_root"`
	WorktreeRoot  string `toml:"worktree_root"`
	MultiRepoRoot string `toml:"multi_repo_root"`
	// NameGenModel is the Claude model used by --claude-name flows.
	NameGenModel string `toml:"name_gen_model"`
}

func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		CodeRoot:      filepath.Join(home, "code", "github"),
		WorktreeRoot:  filepath.Join(home, "code", ".worktrees", "github"),
		MultiRepoRoot: filepath.Join(home, "code"),
		NameGenModel:  "haiku",
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := config.LoadSection("workspaces", &cfg); err != nil {
		return cfg, err
	}
	cfg.CodeRoot = config.ExpandPath(cfg.CodeRoot)
	cfg.WorktreeRoot = config.ExpandPath(cfg.WorktreeRoot)
	cfg.MultiRepoRoot = config.ExpandPath(cfg.MultiRepoRoot)
	return cfg, nil
}
