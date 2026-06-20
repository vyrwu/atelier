// Package config loads atelier's TOML config and dispatches sections to
// plugins. The core defines no plugin schemas — each plugin owns its
// own struct and calls LoadSection with its section name.
//
// User config lives at $XDG_CONFIG_HOME/atelier/config.toml. Each plugin
// reads its own top-level section:
//
//	[workspaces]
//	code_root = "~/code/github"
//
//	[k8s]
//	contexts = "~/.config/atelier/k8s/contexts.yaml"
//
// Adding a new plugin requires zero changes here.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Path returns the resolved config file path.
func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(xdgConfig(home), "atelier", "config.toml")
}

// LoadSection reads the named top-level TOML section into target. target
// should already contain defaults; if the section is absent (or the file
// is missing entirely), target is left unchanged. Errors only on malformed
// TOML or section/struct shape mismatches.
//
// Each plugin defines its own config struct + defaults and calls this with
// its section name. Core code never inspects plugin-specific fields.
func LoadSection(name string, target interface{}) error {
	path := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]toml.Primitive
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	section, ok := raw[name]
	if !ok {
		return nil
	}
	if err := md.PrimitiveDecode(section, target); err != nil {
		return fmt.Errorf("decode section [%s] in %s: %w", name, path, err)
	}
	return nil
}

// ExpandPath expands `~` (home), `~/...`, and `$VAR` references. Plugins
// call this on user-supplied path fields after LoadSection.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if p == "~" {
		return home
	}
	if len(p) > 1 && p[0] == '~' && p[1] == '/' {
		return filepath.Join(home, p[2:])
	}
	if len(p) > 0 && p[0] == '$' {
		return os.ExpandEnv(p)
	}
	return p
}

// XDGConfigHome returns the resolved $XDG_CONFIG_HOME (defaults to ~/.config).
// Plugins use this to build default paths under their own subdir.
func XDGConfigHome() string {
	home, _ := os.UserHomeDir()
	return xdgConfig(home)
}

// XDGCacheHome returns the resolved $XDG_CACHE_HOME (defaults to ~/.cache).
func XDGCacheHome() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache")
}

func xdgConfig(home string) string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	return filepath.Join(home, ".config")
}
