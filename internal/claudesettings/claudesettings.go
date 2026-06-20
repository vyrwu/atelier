// Package claudesettings manages atelier's own Claude settings file,
// passed to every `claude` invocation via `--settings`. This injects
// atelier's required Stop hook (notify-attention) without mutating the
// user's `~/.config/claude/settings.json` — Claude CLI layers our
// settings on top of the user's, so agent-deck and other user hooks
// continue to fire.
//
// File: $XDG_CACHE_HOME/atelier/claude/settings.json
//
// Atelier rewrites this file on every `Ensure()` call (cheap; idempotent
// content). Version-bumping schema changes are handled by the
// `__atelier_version` field, which we check on read.
package claudesettings

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/vyrwu/atelier/internal/dispatch"
)

// schemaVersion lets future atelier releases force a rewrite when the
// embedded hooks change. Bump when modifying the canonical settings.
const schemaVersion = 1

// canonical is the settings JSON atelier guarantees. Stop hook routes
// every Claude task completion to `atelier tools claude notify-attention`,
// which (a) flags the parent workspace window with @needs_attention,
// (b) backgrounds a recap generator that stamps @attention_recap.
//
// Marker field `__atelier_version` is read on Ensure() to decide
// whether to overwrite stale content.
type canonicalSettings struct {
	AtelierVersion int                       `json:"__atelier_version"`
	Hooks          map[string][]hookGroup    `json:"hooks"`
}

type hookGroup struct {
	Hooks []hookEntry `json:"hooks"`
}

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func canonical() canonicalSettings {
	return canonicalSettings{
		AtelierVersion: schemaVersion,
		Hooks: map[string][]hookGroup{
			"Stop": {
				{Hooks: []hookEntry{{
					Type:    "command",
					Command: dispatch.ToolCmd("claude", "notify-attention"),
				}}},
			},
		},
	}
}

// Path returns the absolute path atelier writes its Claude settings to.
func Path() string {
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".cache")
	}
	return filepath.Join(root, "atelier", "claude", "settings.json")
}

// Ensure writes the canonical atelier settings JSON to Path() if it
// doesn't exist or its `__atelier_version` is older than the current
// schemaVersion. Returns the file path so callers can pass it to
// `claude --settings`.
//
// Idempotent and cheap — call before every claude invocation.
func Ensure() (string, error) {
	path := Path()
	if needRewrite(path) {
		if err := writeCanonical(path); err != nil {
			return "", err
		}
	}
	return path, nil
}

func needRewrite(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var current canonicalSettings
	if err := json.Unmarshal(data, &current); err != nil {
		return true
	}
	return current.AtelierVersion < schemaVersion
}

func writeCanonical(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(canonical(), "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
