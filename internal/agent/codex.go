package agent

import (
	"bytes"
	"fmt"
	"github.com/vyrwu/atelier/internal/core"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// codexHome is Codex's config directory: $CODEX_HOME, else ~/.codex. Every
// atelier→Codex write resolves through here, as Claude's do through claudeDir.
func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// codexHooks maps Codex's lifecycle events to the status the guarded `atelier
// hook` command records. It mirrors atelierHooks, except where Codex differs:
//   - Codex has no Notification event. It stops on you through a tool,
//     request_user_input, so that tool's PreToolUse is blocked and the
//     PostToolUse after your answer is working again. There is deliberately no
//     unmatched PreToolUse: it would race the blocked one on the same call.
//   - No PermissionRequest: atelier runs Codex with approvals off, so it never
//     fires, and every hook is one more for you to review and trust.
//   - Nothing fires when a turn ends in an error — a usage limit included — so
//     a Codex space that hits one reads working until that decays (NFR-R5).
func codexHooks() map[string]atelierHook {
	return map[string]atelierHook{
		"UserPromptSubmit": {status: "working"},
		"SessionStart":     {status: "idle"},
		"PreToolUse":       {status: "blocked", matcher: "request_user_input"},
		"PostToolUse":      {status: "working"},
		"Stop":             {status: "idle"},
	}
}

// InstallCodexHooks merges atelier's lifecycle hooks into Codex's
// ~/.codex/hooks.json, which has the same matcher-group shape as Claude's
// settings. Codex runs a user hook only once it is trusted: the first Codex
// launch after this asks you to review them.
func InstallCodexHooks() error {
	return installHooks(filepath.Join(codexHome(), "hooks.json"), Codex, codexHooks(), 0o600)
}

// prepareCodex makes dir a first-class atelier workspace for Codex: it trusts
// the directory in the user's Codex config — so Codex starts without its trust
// screen and loads the project config — and writes that project config, which
// registers atelier's MCP server for this space alone.
func prepareCodex(dir, slug string) error {
	if err := trustCodexProject(dir); err != nil {
		return err
	}
	return writeCodexProject(dir, slug)
}

// trustCodexProject adds `[projects."<dir>"] trust_level = "trusted"` to the
// user's Codex config.toml unless dir already has a trust level — set by atelier,
// by Codex's own trust screen, or by hand, which is the user's to keep. It
// appends rather than re-encodes, so the user's comments and layout survive, and
// leaves the file untouched when it can't parse it, or couldn't after the append
// (a projects table written inline can't be extended) — a config Codex can't
// parse would stop every Codex session.
func trustCodexProject(dir string) error {
	path := filepath.Join(codexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()

	// #nosec G304 -- Reads the owner's config.toml under CODEX_HOME, as required by Codex.
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var cfg struct {
		Projects map[string]map[string]any `toml:"projects"`
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil // don't touch a file we can't parse
	}
	if _, ok := cfg.Projects[dir]["trust_level"]; ok {
		return nil
	}
	var b bytes.Buffer
	b.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString("\n[projects." + tomlString(dir) + "]\ntrust_level = \"trusted\"\n")
	if _, err := toml.Decode(b.String(), &cfg); err != nil {
		return nil
	}
	return writeConfig(path, b.Bytes(), 0o600)
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// codexForwardEnv are the variables Codex hands atelier's MCP server from its
// own environment. Codex starts MCP servers with almost none of it, and
// create_worktree and create_pr shell out to git and gh, which need the
// operator's identity, credentials, and proxy.
var codexForwardEnv = []string{
	"GIT_CONFIG_GLOBAL", "GH_TOKEN", "GITHUB_TOKEN", "GH_HOST", "GH_CONFIG_DIR",
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSH_AUTH_SOCK",
}

// codexMCPServer is the [mcp_servers.atelier] entry in a space's
// .codex/config.toml. Its tools run without an approval prompt: they only act on
// the space.
type codexMCPServer struct {
	ToolTimeout int               `toml:"tool_timeout_sec,omitzero"`
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	EnvVars     []string          `toml:"env_vars"`
	Approval    string            `toml:"default_tools_approval_mode"`
	Env         map[string]string `toml:"env"`
}

// writeCodexProject merges atelier's server into the trusted project config.
func writeCodexProject(dir, slug string) error {
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg := map[string]any{}
	// #nosec G304 -- Reads the owner-controlled project config to preserve other settings.
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	servers, ok := cfg["mcp_servers"].(map[string]any)
	if !ok {
		if _, exists := cfg["mcp_servers"]; exists {
			return fmt.Errorf("mcp_servers must be a table")
		}
		servers = map[string]any{}
	}
	server := codexMCPServer{Command: selfPath(), Args: mcpArgs(slug), EnvVars: codexForwardEnv, Approval: "approve", Env: mcpEnv(slug)}
	server.Env["ATELIER_AGENT"] = string(Codex)
	if slug == core.MaitreSlug {
		server.ToolTimeout = int(MaitreToolTimeout.Seconds())
	}
	servers["atelier"] = server
	cfg["mcp_servers"] = servers
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return err
	}
	return writeConfig(path, b.Bytes(), 0o600)
}
