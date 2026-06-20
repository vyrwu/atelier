package k8s

import (
	"path/filepath"

	"github.com/vyrwu/atelier/internal/config"
)

// Config is the k8s plugin's own config, loaded from the `[k8s]` section
// of $XDG_CONFIG_HOME/atelier/config.toml.
type Config struct {
	Contexts string `toml:"contexts"`
	Configs  string `toml:"configs"`
}

func DefaultConfig() Config {
	root := filepath.Join(config.XDGConfigHome(), "atelier", "k8s")
	return Config{
		Contexts: filepath.Join(root, "contexts.yaml"),
		Configs:  filepath.Join(root, "configs.yaml"),
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := config.LoadSection("k8s", &cfg); err != nil {
		return cfg, err
	}
	cfg.Contexts = config.ExpandPath(cfg.Contexts)
	cfg.Configs = config.ExpandPath(cfg.Configs)
	return cfg, nil
}
