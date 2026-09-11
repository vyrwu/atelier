package core

import (
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the single hand-editable config file (NFR-S3). Everything has a
// sensible default, so a fresh install with no config just works.
type Config struct {
	// Root is where workspace directories live (default ~/ateliers).
	Root string `toml:"root"`
	// Socket is the dedicated tmux socket atelier runs its sessions on, so it
	// never disturbs the user's main tmux server (default "atelier").
	Socket string `toml:"socket"`
	// Naming turns on async Claude title generation; false → deterministic
	// title from the intent text.
	Naming bool `toml:"naming"`
	// NamingModel is the model used for title generation (default "sonnet";
	// Haiku follows the "reply with ONLY…" instruction too loosely — it adds
	// preamble/quotes that fail extraction and fall back to the raw prompt).
	NamingModel string `toml:"naming_model"`
}

// LoadConfig reads config.toml (missing file → defaults) and applies ATELIER_ROOT
// so core.AteliersRoot picks up the configured root without a dependency edge.
func LoadConfig() Config {
	c := Config{Socket: "atelier", Naming: true, NamingModel: "sonnet"}
	if data, err := os.ReadFile(ConfigPath()); err == nil {
		_ = toml.Unmarshal(data, &c)
	}
	// $ATELIER_SOCKET wins over config — this is how `make dev` runs an
	// isolated server without editing the user's config.
	if v := os.Getenv("ATELIER_SOCKET"); v != "" {
		c.Socket = v
	}
	if c.Socket == "" {
		c.Socket = "atelier"
	}
	if c.Root != "" && os.Getenv("ATELIER_ROOT") == "" {
		_ = os.Setenv("ATELIER_ROOT", expand(c.Root))
	}
	return c
}
