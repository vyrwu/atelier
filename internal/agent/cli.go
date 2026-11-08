package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

func grokHome() string {
	if dir := os.Getenv("GROK_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".grok")
}

// Cursor separates configuration (including auth) from transcript data.
func cursorDir(data bool) string {
	key := "CURSOR_CONFIG_DIR"
	if data {
		key = "CURSOR_DATA_DIR"
	}
	if dir := os.Getenv(key); dir != "" {
		return dir
	}
	if !data && os.Getenv("XDG_CONFIG_HOME") != "" {
		return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "cursor")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cursor")
}

// These agents accept project hooks, so their setup stays inside the space.
func prepareGrok(dir, slug string) error {
	path := filepath.Join(dir, ".grok", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg := map[string]any{}
	// #nosec G304 -- Reads the owner-controlled workspace config to preserve existing settings.
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	servers, _ := cfg["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["atelier"] = map[string]any{"command": selfPath(), "args": []string{"mcp"}, "env": mcpEnv(slug)}
	cfg["mcp_servers"] = servers
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return err
	}
	if err := writeConfig(path, b.Bytes(), 0o600); err != nil {
		return err
	}
	table := map[string]atelierHook{}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "StopFailure", "StopCancelled", "SessionEnd"} {
		table[event] = atelierHook{status: "grok"}
	}
	return installHooks(filepath.Join(dir, ".grok", "hooks", "atelier.json"), Grok, table, 0o600)
}

func prepareCursor(dir, slug string) error {
	if err := mergeJSON(filepath.Join(dir, ".cursor", "mcp.json"), func(cfg map[string]any) {
		servers, _ := cfg["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		servers["atelier"] = atelierMCPServer(slug)
		cfg["mcpServers"] = servers
	}); err != nil {
		return err
	}
	return mergeJSON(filepath.Join(dir, ".cursor", "hooks.json"), func(cfg map[string]any) {
		cfg["version"] = 1
		hooks, _ := cfg["hooks"].(map[string]any)
		if hooks == nil {
			hooks = map[string]any{}
		}
		for _, event := range []string{"sessionStart", "beforeSubmitPrompt", "preToolUse", "postToolUse", "stop", "sessionEnd"} {
			var kept []any
			entries, _ := hooks[event].([]any)
			for _, entry := range entries {
				h, _ := entry.(map[string]any)
				cmd, _ := h["command"].(string)
				if !strings.Contains(cmd, "atelier hook ") {
					kept = append(kept, entry)
				}
			}
			// Cursor permission hooks require valid JSON even outside atelier.
			cmd := hookCommand("cursor", Cursor) + `; printf '%s\n' '{"permission":"allow","continue":true}'`
			hooks[event] = append(kept, map[string]any{"command": cmd})
		}
		cfg["hooks"] = hooks
	})
}

func mergeJSON(path string, edit func(map[string]any)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg := map[string]any{}
	// #nosec G304 -- Reads the owner-controlled workspace config to preserve existing settings.
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("%s: expected a JSON object", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	edit(cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeConfig(path, data, 0o600)
}

// CLIHookStatus interprets native payloads; nested agents must never settle
// their parent's status. Unknown events leave the previous status alone.
func CLIHookStatus(k Kind, data []byte) string {
	var h struct {
		Event            string `json:"hook_event_name"`
		GrokEvent        string `json:"hookEventName"`
		Subagent         string `json:"subagentType"`
		AgentID          string `json:"agent_id"`
		Tool             string `json:"tool_name"`
		GrokTool         string `json:"toolName"`
		Notification     string `json:"notification_type"`
		GrokNotification string `json:"notificationType"`
		Status           string `json:"status"`
	}
	if json.Unmarshal(data, &h) != nil || h.Subagent != "" || h.AgentID != "" {
		return ""
	}
	if k == Grok {
		h.Event, h.Tool, h.Notification = h.GrokEvent, h.GrokTool, h.GrokNotification
	}
	switch h.Event {
	case "SessionStart", "SessionEnd", "Stop", "StopCancelled", "sessionStart", "sessionEnd":
		return "idle"
	case "StopFailure":
		return "blocked"
	case "stop":
		if h.Status == "error" {
			return "blocked"
		}
		return "idle"
	case "UserPromptSubmit", "beforeSubmitPrompt", "PostToolUse", "postToolUse":
		return "working"
	case "PreToolUse", "preToolUse":
		if h.Tool == "ask_user" || h.Tool == "AskUserQuestion" || h.Tool == "AskQuestion" {
			return "blocked"
		}
		return "working"
	case "Notification":
		switch h.Notification {
		case "permission_prompt", "agent_needs_input":
			return "blocked"
		case "idle_prompt":
			return "idle"
		}
	}
	return ""
}
