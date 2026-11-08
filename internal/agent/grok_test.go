package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestPrepareGrokProject(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ATELIER_ROOT", "/spaces")
	dir := t.TempDir()
	config := filepath.Join(dir, ".grok")
	if err := os.MkdirAll(config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "config.toml"), []byte("[models]\ndefault = 'my-model'\n[mcp_servers.kept]\ncommand = 'mine'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Grok.Prepare(dir, "test-space"); err != nil {
			t.Fatal(err)
		}
	}
	var cfg map[string]any
	if _, err := toml.DecodeFile(filepath.Join(config, "config.toml"), &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcp_servers"].(map[string]any)
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
	if len(servers) != 2 {
		t.Fatalf("servers = %v", servers)
	}
	mcp := servers["atelier"].(map[string]any)
	env := mcp["env"].(map[string]any)
	if mcp["command"] != selfPath() || env["ATELIER_SESSION"] != "test-space" || env["ATELIER_ROOT"] != "/spaces" {
		t.Fatalf("wrong MCP context: %v", mcp)
	}
}

func TestPrepareGrokRejectsMalformedConfig(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, tc := range []struct {
		path, body string
	}{
		{".grok/config.toml", "[broken"},
		{".grok/hooks/atelier.json", "null"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, tc.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Grok.Prepare(dir, "test-space"); err == nil {
			t.Errorf("accepted malformed %s", tc.path)
		}
		if got, _ := os.ReadFile(path); string(got) != tc.body {
			t.Errorf("clobbered %s", tc.path)
		}
	}
}

func TestGrokHookStatus(t *testing.T) {
	for _, tc := range []struct {
		data, want string
	}{
		{`{"hookEventName":"user_prompt_submit","hook_event_name":"UserPromptSubmit"}`, "working"},
		{`{"hookEventName":"pre_tool_use","hook_event_name":"PreToolUse","toolName":"ask_user_question"}`, "blocked"},
		{`{"hookEventName":"post_tool_use","toolName":"ask_user_question"}`, "working"},
		{`{"hookEventName":"notification","notificationType":"permission_prompt"}`, "blocked"},
		{`{"hookEventName":"stop_failure","hook_event_name":"StopFailure"}`, "blocked"},
		{`{"hookEventName":"stop_cancelled"}`, "idle"},
		{`{"hook_event_name":"PreToolUse","tool_name":"ask_user_question"}`, "blocked"},
		{`{"hookEventName":"UserPromptSubmit"}`, "working"},
		{`{"hookEventName":"PreToolUse","toolName":"ask_user"}`, "blocked"},
		{`{"hookEventName":"Notification","notificationType":"permission_prompt"}`, "blocked"},
		{`{"hookEventName":"Notification","notificationType":"idle_prompt"}`, "idle"},
		{`{"hookEventName":"StopFailure"}`, "blocked"},
		{`{"hookEventName":"StopCancelled"}`, "idle"},
		{`{"hookEventName":"StopFailure","subagentType":"explore"}`, ""},
		{`{"hook_event_name":"unknown"}`, ""},
		{`{broken`, ""},
	} {
		if got := GrokHookStatus([]byte(tc.data), ""); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.data, got, tc.want)
		}
	}
}

func TestGrokIdleReminderPreservesFailure(t *testing.T) {
	status := "idle"
	for _, tc := range []struct{ event, want string }{
		{`{"hookEventName":"user_prompt_submit"}`, "working"},
		{`{"hookEventName":"stop_failure"}`, "blocked"},
		{`{"hookEventName":"notification","notificationType":"idle_prompt"}`, "blocked"},
		{`{"hookEventName":"user_prompt_submit"}`, "working"},
		{`{"hookEventName":"stop"}`, "idle"},
		{`{"hookEventName":"notification","notificationType":"idle_prompt"}`, "idle"},
	} {
		if next := GrokHookStatus([]byte(tc.event), status); next != "" {
			status = next
		}
		if status != tc.want {
			t.Fatalf("after %s: %s, want %s", tc.event, status, tc.want)
		}
	}
}

func TestProviderConfigPathsAndEnvironment(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	t.Setenv("CODEX_HOME", "/custom/codex")
	t.Setenv("GROK_HOME", "/custom/grok")
	for _, k := range Kinds() {
		env := Env("space", k)
		for key, want := range map[string]string{
			"CLAUDE_CONFIG_DIR": "/custom/claude", "CODEX_HOME": "/custom/codex",
			"GROK_HOME": "/custom/grok",
		} {
			if env[key] != want {
				t.Errorf("swap to %s lost %s: %q, want %q", k, key, env[key], want)
			}
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if value, present := Env("space", Grok)["CLAUDE_CONFIG_DIR"]; !present || value != "" {
		t.Fatal("legacy Claude layout must clear a stale server override")
	}
}
