package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

const MaitreToolTimeout = 10 * time.Minute

func mcpArgs(slug string) []string {
	if slug == core.MaitreSlug {
		return []string{"mcp", "--maitre"}
	}
	return []string{"mcp"}
}

// Only Maître gets the operator permissions. Destruction still passes through
// atelier's own confirmation, independently of these harness permissions.
func allowMaitreTools(root string) error {
	path := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg := map[string]any{}
	// #nosec G304 -- Reads the owner-controlled project settings, preserving their existing entries.
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
	}
	if cfg == nil {
		return fmt.Errorf("project settings must be an object")
	}
	perms, ok := cfg["permissions"].(map[string]any)
	if !ok {
		if _, exists := cfg["permissions"]; exists {
			return fmt.Errorf("permissions must be an object")
		}
		perms = map[string]any{}
	}
	allow := []string{}
	if value, exists := perms["allow"]; exists {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &allow); err != nil {
			return err
		}
	}
	for _, permission := range []string{"mcp__atelier", "Bash(atelier:*)"} {
		if !slices.Contains(allow, permission) {
			allow = append(allow, permission)
		}
	}
	perms["allow"] = allow
	cfg["permissions"] = perms
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeConfig(path, data, 0o600)
}

func EnsureMaitreGuide() (string, error) {
	path := core.MaitreGuidePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	// Create once, even if two clients open Maître together. Never replace edits.
	// #nosec G304 -- Writes the owner-configured central Maître guide with exclusive creation.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	_, writeErr := f.WriteString(maitreGuide)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	return path, closeErr
}

func LinkMaitreGuide(root string) error {
	path, err := EnsureMaitreGuide()
	if err != nil {
		return err
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if err := os.Symlink(path, filepath.Join(root, name)); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

const maitreGuide = `# Maître

You are Maître, the operator of this atelier instance. You run spaces for the
human the way they would: start them, check on them, answer their agents, tidy
up. You do not write code or create worktrees yourself — every piece of work
happens in a space.

## Tools

Use the atelier MCP tools (or the same verbs as atelier <verb> in a shell):
list_spaces, create_space, rename_space, retire_space, restore_space,
delete_space, purge_spaces, swap_agent, read_screen, send_input,
wait_for_change, focus_space.

## How to work

- Read a space's screen before typing to it. Answer what it actually asks.
- To start work, create a space with a clear, self-contained intent: the repo,
  the goal, and how to know it's done.
- Waiting for spaces: call wait_for_change, never poll in a loop. Omit its
  timeout for the provider default (5 minutes, or 50 seconds under Grok).
- Approve routine permission prompts in spaces (tests, builds, edits, git
  commits, opening PRs). Ask the human before approving anything destructive or
  irreversible: force-pushes, deleting data or branches, production changes.
- Deleting and purging always ask the human through atelier. If it reports they
  aren't looking, tell them what you wanted to delete and why.
- When the human wants to see a space, use focus_space.
- Keep replies short: you live in a small popup.
`
