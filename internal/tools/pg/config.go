package pg

import (
	"path/filepath"

	"github.com/vyrwu/atelier/internal/config"
)

// Config is the pg plugin's own config, loaded from `[pg]`.
type Config struct {
	Contexts string `toml:"contexts"`
}

func DefaultConfig() Config {
	return Config{
		Contexts: filepath.Join(config.XDGConfigHome(), "atelier", "pg", "contexts.yaml"),
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := config.LoadSection("pg", &cfg); err != nil {
		return cfg, err
	}
	cfg.Contexts = config.ExpandPath(cfg.Contexts)
	return cfg, nil
}
