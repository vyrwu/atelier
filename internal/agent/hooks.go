package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vyrwu/atelier/internal/core"
)

// PrepareProject makes dir a first-class atelier workspace for Claude Code, in
// one merge into ~/.claude.json's projects.<dir> entry:
//   - hasTrustDialogAccepted: skip the folder-trust dialog when launching here.
//   - mcpServers.atelier: register atelier's own MCP server at *local* (this
//     project) scope, so create_worktree / create_pr / register_pr are available
//     here without a per-project approval prompt and without cluttering the
//     user's other Claude sessions. It runs the current binary (`<self> mcp`)
//     with the workspace's identity and path env baked in, so the server
//     resolves the same state this atelier instance uses (prod or `make dev`).
//
// There is no flag or settings key for either, so we merge directly, preserving
// all other content and the file's mode (it holds secrets → default 0600), and
// leaving an unparseable file untouched. Best-effort: worst case is a one-time
// prompt or an unregistered tool, never a corrupt file (atomic write).
func PrepareProject(dir, slug string) error {
	path := claudeConfigJSON()

	mode := os.FileMode(0o600)
	cfg := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if json.Unmarshal(data, &cfg) != nil {
			return nil // don't clobber a file we can't parse
		}
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	proj, _ := projects[dir].(map[string]any)
	if proj == nil {
		proj = map[string]any{}
	}
	proj["hasTrustDialogAccepted"] = true

	servers, _ := proj["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["atelier"] = atelierMCPServer(slug)
	proj["mcpServers"] = servers

	projects[dir] = proj
	cfg["projects"] = projects

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicMode(path, out, mode)
}

// atelierMCPServer is the local-scope MCP server entry for a workspace. It pins
// the workspace via ATELIER_SESSION and forwards the path-controlling env that
// is active now, so the server (launched later by Claude) reads the same state,
// config, and ateliers root — critical under `make dev`, which relocates them.
func atelierMCPServer(slug string) map[string]any {
	self, err := os.Executable()
	if err != nil {
		self = "atelier"
	}
	env := map[string]string{"ATELIER_SESSION": slug}
	for _, k := range []string{"ATELIER_ROOT", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	return map[string]any{
		"type":    "stdio",
		"command": self,
		"args":    []string{"mcp"},
		"env":     env,
	}
}

// EnsureWorkspaceGuide writes the default per-workspace CLAUDE.md to the central
// WorkspaceGuidePath if it does not exist yet (never overwriting the user's
// edits), and returns its path. New workspaces symlink their CLAUDE.md to it.
func EnsureWorkspaceGuide() (string, error) {
	path := core.WorkspaceGuidePath()
	if _, err := os.Stat(path); err == nil {
		return path, nil // exists — respect user edits
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(workspaceGuide), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// hookCommand builds the guarded shell command atelier wires into a Claude
// lifecycle event: it is a no-op unless $ATELIER_SESSION is set, so a user's
// non-atelier Claude sessions are unaffected.
func hookCommand(status string) string {
	return `[ -n "$ATELIER_SESSION" ] && atelier hook ` + status + " || true"
}

// atelierHooks maps each Claude lifecycle event to the status the guarded
// `atelier hook` command records:
//   - UserPromptSubmit / SessionStart → working (the agent is running)
//   - Notification                    → blocked (Claude needs the user)
//   - Stop                            → idle    (the turn finished)
func atelierHooks() map[string]string {
	return map[string]string{
		"UserPromptSubmit": "working",
		"SessionStart":     "working",
		"Notification":     "blocked",
		"Stop":             "idle",
	}
}

// claudeDir returns Claude Code's config directory. Claude honors
// $CLAUDE_CONFIG_DIR, which relocates BOTH its JSON config and its settings; the
// two live flat inside it (<dir>/.claude.json, <dir>/settings.json). When unset,
// Claude uses the legacy split: $HOME/.claude.json and $HOME/.claude/. Every
// atelier→Claude write MUST resolve through here, or it lands in a file Claude
// never reads (silent no-op for hooks, trust, and MCP registration).
func claudeDir() (dir string, legacy bool) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, false
	}
	home, _ := os.UserHomeDir()
	return home, true
}

// claudeConfigJSON is Claude's main JSON store (projects, trust, MCP servers).
func claudeConfigJSON() string {
	dir, _ := claudeDir()
	return filepath.Join(dir, ".claude.json")
}

// settingsPath returns Claude's global settings file (hooks live here).
func settingsPath() string {
	dir, legacy := claudeDir()
	if legacy {
		return filepath.Join(dir, ".claude", "settings.json")
	}
	return filepath.Join(dir, "settings.json")
}

// InstallHooks merges atelier's lifecycle hooks into the user's global Claude
// settings (~/.claude/settings.json), preserving all existing content. It reads
// the current settings, adds one guarded command hook per lifecycle event
// (replacing any prior atelier hook for that event while keeping the user's own
// hooks), and writes the result atomically.
func InstallHooks() error {
	path := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return err
		}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	for event, status := range atelierHooks() {
		cmd := hookCommand(status)
		hooks[event] = mergeEventHook(hooks[event], cmd)
	}
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, out)
}

// mergeEventHook returns the matcher-group list for one event with atelier's
// command present exactly once. It preserves any existing non-atelier groups
// (the user's own hooks) and drops stale atelier groups so re-running
// InstallHooks is idempotent. The Claude schema is a list of
// {"matcher": "...", "hooks":[{"type":"command","command":...}]} entries.
func mergeEventHook(existing any, cmd string) []any {
	var kept []any
	if list, ok := existing.([]any); ok {
		for _, g := range list {
			if !groupHasAtelierHook(g) {
				kept = append(kept, g)
			}
		}
	}
	group := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": cmd},
		},
	}
	return append(kept, group)
}

// groupHasAtelierHook reports whether a matcher-group already contains an
// atelier-owned command hook (one that invokes `atelier hook`).
func groupHasAtelierHook(group any) bool {
	g, ok := group.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := g["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, "atelier hook ") {
			return true
		}
	}
	return false
}

// writeAtomic writes data to path via a temp file + rename so a crash never
// leaves a half-written settings file.
func writeAtomic(path string, data []byte) error {
	return writeAtomicMode(path, data, 0o644)
}

func writeAtomicMode(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// HandleHook is the body of the `atelier hook <status>` command. It is guarded:
// with no $ATELIER_SESSION it does nothing (a non-atelier Claude session).
// Otherwise it records status as the session's runtime state. PR refresh is the
// caller's concern, not this function's.
func HandleHook(status string) error {
	session := os.Getenv("ATELIER_SESSION")
	if session == "" {
		return nil
	}
	return SetStatus(session, core.AgentStatus(status))
}
