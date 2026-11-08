package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestPrepareCLIProjects(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ATELIER_ROOT", "/spaces")
	for _, k := range []Kind{Grok, Cursor} {
		t.Run(string(k), func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "."+string(k))
			if err := os.MkdirAll(config, 0o755); err != nil {
				t.Fatal(err)
			}
			if k == Cursor {
				if err := os.WriteFile(filepath.Join(config, "hooks.json"), []byte(`{"version":1,"hooks":{"stop":[{"command":"my-hook"}]},"custom":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(config, "mcp.json"), []byte(`{"mcpServers":{"kept":{"command":"mine"}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(config, "config.toml"), []byte("[models]\ndefault = 'my-model'\n[mcp_servers.kept]\ncommand = 'mine'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := k.Prepare(dir, "test-space"); err != nil {
					t.Fatal(err)
				}
			}
			var servers map[string]any
			if k == Cursor {
				var cfg map[string]any
				data, _ := os.ReadFile(filepath.Join(config, "mcp.json"))
				if err := json.Unmarshal(data, &cfg); err != nil {
					t.Fatal(err)
				}
				servers = cfg["mcpServers"].(map[string]any)
				data, _ = os.ReadFile(filepath.Join(config, "hooks.json"))
				if err := json.Unmarshal(data, &cfg); err != nil {
					t.Fatal(err)
				}
				stop := cfg["hooks"].(map[string]any)["stop"].([]any)
				if cfg["custom"] != true || len(stop) != 2 || stop[0].(map[string]any)["command"] != "my-hook" {
					t.Fatalf("hooks lost or duplicated: %s", data)
				}
				cmd := stop[1].(map[string]any)["command"].(string)
				if !strings.Contains(cmd, "ATELIER_AGENT") || !strings.Contains(cmd, `"permission":"allow"`) {
					t.Fatalf("unguarded or invalid hook reply: %s", cmd)
				}
			} else {
				var cfg map[string]any
				if _, err := toml.DecodeFile(filepath.Join(config, "config.toml"), &cfg); err != nil {
					t.Fatal(err)
				}
				servers = cfg["mcp_servers"].(map[string]any)
				if cfg["models"].(map[string]any)["default"] != "my-model" {
					t.Fatal("changed the selected model")
				}
				data, err := os.ReadFile(filepath.Join(config, "hooks", "atelier.json"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(string(data), "atelier hook grok") != 9 {
					t.Fatalf("missing or duplicate hooks: %s", data)
				}
			}
			if len(servers) != 2 {
				t.Fatalf("servers = %v", servers)
			}
			mcp := servers["atelier"].(map[string]any)
			env := mcp["env"].(map[string]any)
			if mcp["command"] != selfPath() || env["ATELIER_SESSION"] != "test-space" || env["ATELIER_ROOT"] != "/spaces" {
				t.Fatalf("wrong MCP context: %v", mcp)
			}
		})
	}
}

func TestPrepareCLIRejectsMalformedConfig(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, tc := range []struct {
		kind       Kind
		path, body string
	}{
		{Grok, ".grok/config.toml", "[broken"},
		{Cursor, ".cursor/mcp.json", "{broken"},
		{Cursor, ".cursor/mcp.json", "null"},
		{Cursor, ".cursor/hooks.json", "{broken"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, tc.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := tc.kind.Prepare(dir, "test-space"); err == nil {
			t.Errorf("accepted malformed %s", tc.path)
		}
		if got, _ := os.ReadFile(path); string(got) != tc.body {
			t.Errorf("clobbered %s", tc.path)
		}
	}
}

func TestCLIHookStatus(t *testing.T) {
	for _, tc := range []struct {
		kind       Kind
		data, want string
	}{
		{Grok, `{"hookEventName":"UserPromptSubmit"}`, "working"},
		{Grok, `{"hookEventName":"PreToolUse","toolName":"ask_user"}`, "blocked"},
		{Grok, `{"hookEventName":"Notification","notificationType":"permission_prompt"}`, "blocked"},
		{Grok, `{"hookEventName":"Notification","notificationType":"idle_prompt"}`, "idle"},
		{Grok, `{"hookEventName":"StopFailure"}`, "blocked"},
		{Grok, `{"hookEventName":"StopCancelled"}`, "idle"},
		{Grok, `{"hookEventName":"StopFailure","subagentType":"explore"}`, ""},
		{Cursor, `{"hook_event_name":"beforeSubmitPrompt"}`, "working"},
		{Cursor, `{"hook_event_name":"preToolUse","tool_name":"AskQuestion"}`, "blocked"},
		{Cursor, `{"hook_event_name":"postToolUse","tool_name":"AskQuestion"}`, "working"},
		{Cursor, `{"hook_event_name":"stop","status":"error"}`, "blocked"},
		{Cursor, `{"hook_event_name":"stop","status":"completed"}`, "idle"},
		{Cursor, `{"hook_event_name":"stop","agent_id":"child"}`, ""},
		{Cursor, `{"hook_event_name":"unknown"}`, ""},
		{Cursor, `{broken`, ""},
	} {
		if got := CLIHookStatus(tc.kind, []byte(tc.data)); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.kind, tc.data, got, tc.want)
		}
	}
}

func TestCLIConfigPathsAndEnvironment(t *testing.T) {
	t.Setenv("GROK_HOME", "/custom/grok")
	t.Setenv("CURSOR_CONFIG_DIR", "/custom/cursor-config")
	t.Setenv("CURSOR_DATA_DIR", "/custom/cursor-data")
	if env := Env("space", Grok); env["GROK_HOME"] != "/custom/grok" {
		t.Fatal(env)
	}
	if env := Env("space", Cursor); env["CURSOR_CONFIG_DIR"] != "/custom/cursor-config" || env["CURSOR_DATA_DIR"] != "/custom/cursor-data" {
		t.Fatal(env)
	}
	t.Setenv("CURSOR_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if cursorDir(false) != "/xdg/cursor" || cursorDir(true) != "/custom/cursor-data" {
		t.Fatal("Cursor config and data paths were conflated")
	}
}
