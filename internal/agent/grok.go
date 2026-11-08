package agent

import (
	"bytes"
	"encoding/json"
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

// Grok accepts project hooks, so its setup stays inside the space.
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

// GrokHookStatus interprets native payloads; nested agents must never settle
// their parent's status. Unknown events leave the previous status alone.
func GrokHookStatus(data []byte, prior string) string {
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
	// Native event values are snake_case; the compatibility alias uses
	// PascalCase. Keep either envelope usable without requiring both.
	if h.GrokEvent != "" {
		h.Event = h.GrokEvent
	}
	if h.GrokTool != "" {
		h.Tool = h.GrokTool
	}
	if h.GrokNotification != "" {
		h.Notification = h.GrokNotification
	}
	switch strings.ReplaceAll(strings.ToLower(h.Event), "_", "") {
	case "sessionstart", "sessionend", "stopcancelled":
		return "idle"
	case "stopfailure":
		return "blocked"
	case "stop":
		if h.Status == "error" {
			return "blocked"
		}
		return "idle"
	case "userpromptsubmit", "posttooluse":
		return "working"
	case "pretooluse":
		if h.Tool == "ask_user" || h.Tool == "ask_user_question" || h.Tool == "AskUserQuestion" {
			return "blocked"
		}
		return "working"
	case "notification":
		switch h.Notification {
		case "permission_prompt", "agent_needs_input":
			return "blocked"
		case "idle_prompt":
			// Grok sends this delayed reminder even after StopFailure. Keep
			// the failure visible until the operator returns or starts a turn.
			if prior != "blocked" {
				return "idle"
			}
		}
	}
	return ""
}
