package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Codex has no Notification event: it stops on the operator through the
// request_user_input tool, so only that tool's PreToolUse may mark it blocked.
func TestCodexHooksMapping(t *testing.T) {
	want := map[string]atelierHook{
		"UserPromptSubmit": {status: "working"},
		"SessionStart":     {status: "idle"},
		"PreToolUse":       {status: "blocked", matcher: "request_user_input"},
		"PostToolUse":      {status: "working"},
		"Stop":             {status: "idle"},
	}
	got := codexHooks()
	if len(got) != len(want) {
		t.Fatalf("codex hook events = %v, want %v", got, want)
	}
	for event, h := range want {
		if got[event] != h {
			t.Errorf("%s → %+v, want %+v", event, got[event], h)
		}
	}
	if _, ok := got["Notification"]; ok {
		t.Error("Codex has no Notification event")
	}
}

// hooks.json is the user's file too: their hooks and anything else in it
// survive, and re-installing changes nothing.
func TestInstallCodexHooksPreservesTheUsersHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "hooks.json")
	user := `{"description":"mine","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`
	if err := os.WriteFile(path, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexHooks(); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if err := InstallCodexHooks(); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Error("re-installing changed hooks.json")
	}
	var got struct {
		Description string                       `json:"description"`
		Hooks       map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(second, &got); err != nil {
		t.Fatal(err)
	}
	if got.Description != "mine" {
		t.Error("lost the user's description")
	}
	if len(got.Hooks["Stop"]) != 2 || !strings.Contains(string(got.Hooks["Stop"][0]), "say done") {
		t.Errorf("Stop groups = %s, want the user's then atelier's", got.Hooks["Stop"])
	}
	if !strings.Contains(string(got.Hooks["PreToolUse"][0]), `"matcher": "request_user_input"`) {
		t.Errorf("PreToolUse = %s, want it matched to request_user_input", got.Hooks["PreToolUse"])
	}
}

// Trusting a space appends to the user's Codex config, so its comments and
// layout survive; a directory that already has a trust level keeps it; a file
// that won't parse is left alone.
func TestTrustCodexProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "config.toml")
	orig := "# my settings\nmodel = \"gpt-x\"\n\n[projects.\"/kept\"]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := trustCodexProject("/spaces/brave-otter"); err != nil {
			t.Fatal(err)
		}
		if err := trustCodexProject("/kept"); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), orig) {
		t.Errorf("rewrote the user's config:\n%s", data)
	}
	var cfg struct {
		Projects map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		t.Fatalf("config no longer parses: %v\n%s", err, data)
	}
	if cfg.Projects["/spaces/brave-otter"].TrustLevel != "trusted" {
		t.Error("space not trusted")
	}
	if cfg.Projects["/kept"].TrustLevel != "untrusted" {
		t.Error("overrode a trust level the user set")
	}
	if n := strings.Count(string(data), "brave-otter"); n != 1 {
		t.Errorf("trust entry written %d times, want once", n)
	}

	bad := "not = [valid"
	_ = os.WriteFile(path, []byte(bad), 0o600)
	if err := trustCodexProject("/x"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != bad {
		t.Error("touched a config it could not parse")
	}
}

// Codex starts MCP servers with almost no environment, so the space's identity
// must be in the server's own env, and the operator's git and gh setup must be
// forwarded by name.
func TestWriteCodexProject(t *testing.T) {
	dir := t.TempDir()
	if err := writeCodexProject(dir, "brave-otter"); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, ".codex", "config.toml"), &cfg); err != nil {
		t.Fatal(err)
	}
	srv := cfg.MCPServers["atelier"]
	if srv.Env["ATELIER_SESSION"] != "brave-otter" {
		t.Errorf("env = %v, want ATELIER_SESSION pinned", srv.Env)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "mcp" || srv.Approval != "approve" {
		t.Errorf("server = %+v", srv)
	}
	for _, v := range []string{"GIT_CONFIG_GLOBAL", "GH_TOKEN"} {
		if !strings.Contains(strings.Join(srv.EnvVars, " "), v) {
			t.Errorf("env_vars = %v, missing %s", srv.EnvVars, v)
		}
	}
}
