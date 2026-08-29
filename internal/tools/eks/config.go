package eks

import (
	"path/filepath"

	"github.com/vyrwu/atelier/internal/config"
)

// Config is the eks tool's own config, loaded from the `[eks]` section of
// $XDG_CONFIG_HOME/atelier/config.toml. Mirrors the k8s tool's config but under
// its own dir so the two tools' context lists are independent.
type Config struct {
	Contexts string `toml:"contexts"`
	Configs  string `toml:"configs"`
}

func DefaultConfig() Config {
	root := filepath.Join(config.XDGConfigHome(), "atelier", "eks")
	return Config{
		Contexts: filepath.Join(root, "contexts.yaml"),
		Configs:  filepath.Join(root, "configs.yaml"),
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := config.LoadSection("eks", &cfg); err != nil {
		return cfg, err
	}
	cfg.Contexts = config.ExpandPath(cfg.Contexts)
	cfg.Configs = config.ExpandPath(cfg.Configs)
	return cfg, nil
}
