package agent

import (
	"encoding/json"
	"github.com/BurntSushi/toml"
	"github.com/vyrwu/atelier/internal/core"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMCPArgsAndMaitreEnv(t *testing.T) {
	if !reflect.DeepEqual(mcpArgs(core.MaitreSlug), []string{"mcp", "--maitre"}) || !reflect.DeepEqual(mcpArgs("space"), []string{"mcp"}) {
		t.Fatal("wrong tool set")
	}
	t.Setenv("ATELIER_SOCKET", "isolated-maitre")
	t.Setenv("CODEX_HOME", "/custom/codex")
	if env := mcpEnv(core.MaitreSlug); env["ATELIER_SOCKET"] != "isolated-maitre" || env["CODEX_HOME"] != "/custom/codex" {
		t.Fatal(env)
	}
	if Env(core.MaitreSlug, Claude)["MCP_TOOL_TIMEOUT"] != "600000" || Env("space", Claude)["MCP_TOOL_TIMEOUT"] != "" {
		t.Fatal("wrong timeout")
	}
}

func TestAllowMaitreToolsMerges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"statusLine":{"command":"mine"},"permissions":{"allow":["Read"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := allowMaitreTools(dir); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	var cfg struct {
		StatusLine  map[string]string
		Permissions struct{ Allow []string }
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Permissions.Allow, []string{"Read", "mcp__atelier", "Bash(atelier:*)"}) || cfg.StatusLine["command"] != "mine" {
		t.Fatal(string(data))
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := allowMaitreTools(dir); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if data, _ := os.ReadFile(path); string(data) != "{" {
		t.Fatal("invalid config overwritten")
	}
}

func TestWriteCodexProjectForMaitre(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = 'custom'\n[mcp_servers.kept]\ncommand = 'kept'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCodexProject(dir, core.MaitreSlug); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Model      string
		MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers["atelier"]
	if !reflect.DeepEqual(server.Args, []string{"mcp", "--maitre"}) || server.ToolTimeout != 600 || cfg.Model != "custom" || cfg.MCPServers["kept"].Command != "kept" {
		t.Fatalf("%+v", cfg)
	}
	if err := writeCodexProject(dir, "space"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "tool_timeout_sec") {
		t.Fatal(string(data))
	}
}

func TestPrepareGrokForMaitre(t *testing.T) {
	dir := t.TempDir()
	if err := prepareGrok(dir, core.MaitreSlug); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]struct{ Args []string } `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, ".grok", "config.toml"), &cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Servers["atelier"].Args, []string{"mcp", "--maitre"}) {
		t.Fatal(cfg)
	}
}

func TestEnsureMaitreGuideKeepsEdits(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := EnsureMaitreGuide()
	if err != nil {
		t.Fatal(err)
	}
	if path != core.MaitreGuidePath() {
		t.Fatal(path)
	}
	if err := os.WriteFile(path, []byte("my guide"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMaitreGuide(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "my guide" {
		t.Fatal(string(data))
	}
}

func TestBlockedMaitreCountsForAttention(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := SetStatus(core.MaitreSlug, core.StatusBlocked); err != nil {
		t.Fatal(err)
	}
	if got := BlockedCount([]string{core.MaitreSlug}); got != 1 {
		t.Fatal(got)
	}
}
