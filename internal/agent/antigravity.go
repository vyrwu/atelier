package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

const antigravitySettings = ".atelier/antigravity-settings.json"

func antigravityDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli")
}

func antigravityCmd(id, prompt string) string {
	cmd := "agy --settings " + shellQuote(antigravitySettings)
	if id != "" {
		cmd += " --conversation=" + shellQuote(id)
	}
	if prompt != "" {
		// Initial slash commands and shell shortcuts must remain task text.
		if strings.HasPrefix(strings.TrimSpace(prompt), "/") || strings.HasPrefix(strings.TrimSpace(prompt), "!") {
			prompt = "Task: " + prompt
		}
		cmd += " --prompt-interactive=" + shellQuote(prompt)
	}
	return cmd
}

// Antigravity's session-only settings overlay keeps the native model, effort,
// authentication and permissions. Only this launch gets the status collector;
// a nested CLI or a session outside Atelier cannot inherit it from global config.
func prepareAntigravity(root, slug string) error {
	if err := mergeAntigravityMCP(filepath.Join(root, ".agents", "mcp_config.json"), slug); err != nil {
		return err
	}
	return prepareAntigravitySettings(root, filepath.Join(antigravityDir(), "settings.json"))
}

func readJSONObject(path string) (map[string]any, error) {
	// #nosec G304 -- Reads the owner's native or workspace-local agent configuration.
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("%s: expected a JSON object", path)
	}
	return cfg, nil
}

func mergeAntigravityMCP(path, slug string) error {
	unlock, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := readJSONObject(path)
	if err != nil {
		return err
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		if _, exists := cfg["mcpServers"]; exists {
			return fmt.Errorf("%s: mcpServers must be an object", path)
		}
		servers = map[string]any{}
	}
	server := atelierMCPServer(slug)
	delete(server, "type") // agy infers stdio from command.
	servers["atelier"] = server
	cfg["mcpServers"] = servers
	return writeAntigravityJSON(path, cfg)
}

func prepareAntigravitySettings(root, global string) error {
	cfg, err := readJSONObject(global)
	if err != nil {
		return err
	}
	status := map[string]any{"type": "command"}
	original, ok := cfg["statusLine"].(map[string]any)
	if !ok && cfg["statusLine"] != nil {
		return fmt.Errorf("antigravity: statusLine must be an object")
	}
	if original == nil || original["enabled"] == false {
		status["stack_with_default"] = true
	}
	for key, value := range original {
		status[key] = value
	}
	// Pin the paths and socket even when tmux predates a dev environment.
	socket := core.LoadConfig().Socket
	cmd := "env XDG_CACHE_HOME=" + shellQuote(filepath.Dir(core.CacheDir())) +
		" XDG_STATE_HOME=" + shellQuote(filepath.Dir(filepath.Dir(core.StatePath()))) +
		" XDG_CONFIG_HOME=" + shellQuote(filepath.Dir(filepath.Dir(core.ConfigPath()))) +
		" ATELIER_ROOT=" + shellQuote(core.AteliersRoot()) +
		" ATELIER_SOCKET=" + shellQuote(socket) + " " +
		shellQuote(selfPath()) + " usage antigravity " + shellQuote(root)
	if previous, _ := original["command"].(string); previous != "" && original["enabled"] != false {
		cmd += " " + shellQuote(base64.StdEncoding.EncodeToString([]byte(previous)))
	}
	status["type"], status["command"], status["enabled"] = "command", cmd, true
	// Trust only the space supplied by Atelier, for the lifetime of this launch.
	return writeAntigravityJSON(filepath.Join(root, antigravitySettings), map[string]any{
		"statusLine": status, "trustedWorkspaces": []string{root},
	})
}

func writeAntigravityJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Native status updates run frequently; unchanged pointers need no write.
	// #nosec G304 -- path is an owner-controlled agent configuration or workspace metadata path.
	prior, _ := os.ReadFile(path)
	if bytes.Equal(prior, data) {
		return nil
	}
	return writeConfig(path, data, 0o600)
}

type antigravitySession struct {
	ID         string `json:"id"`
	Transcript string `json:"transcript"`
}

var antigravityID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// The TUI's documented status payload identifies its main conversation. Keep
// only a pointer to the native transcript, never a copy of messages or tokens.
// Initial --prompt-interactive messages do not appear in native history.jsonl;
// the CLI's SQLite summaries can also omit workspace paths, so neither reliably
// associates a conversation with a space. The pointer survives state recovery.
func CaptureAntigravity(data []byte, root string, now time.Time) (string, error) {
	var input struct {
		Product      string `json:"product"`
		ID           string `json:"conversation_id"`
		Transcript   string `json:"transcript_path"`
		State        string `json:"agent_state"`
		Confirmation bool   `json:"tool_confirmation_pending"`
		Workspace    struct {
			Current string `json:"current_dir"`
			Project string `json:"project_dir"`
		} `json:"workspace"`
		Quota map[string]*struct {
			Remaining *float64  `json:"remaining_fraction"`
			Reset     time.Time `json:"reset_time"`
			Disabled  bool      `json:"disabled"`
		} `json:"quota"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return "", err
	}
	if input.Product != "antigravity" || (!sameDirectory(root, input.Workspace.Current) && !sameDirectory(root, input.Workspace.Project)) {
		return "", nil
	}
	// Authentication and initialization can report status before a conversation
	// exists. They must still reach the attention badge.
	if input.ID != "" || input.Transcript != "" {
		s := antigravitySession{input.ID, input.Transcript}
		if !validAntigravitySession(s) {
			return "", nil
		}
		if err := writeAntigravityJSON(filepath.Join(root, ".atelier", "antigravity", s.ID+".json"), s); err != nil {
			return "", err
		}
	}
	u := Usage{Observed: now, Source: "statusline"}
	keys := make([]string, 0, len(input.Quota))
	for key := range input.Quota {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		q := input.Quota[key]
		if q == nil || q.Disabled || q.Remaining == nil || *q.Remaining < 0 || *q.Remaining > 1 || !q.Reset.After(now) {
			continue
		}
		used := math.Round((1-*q.Remaining)*1e6) / 1e4
		u.Buckets = append(u.Buckets, UsageBucket{ID: key, Name: key, Primary: &UsageWindow{Used: &used, Reset: q.Reset.Unix()}})
	}
	// A missing balance (startup or gateway accounts) must not imply zero use.
	if len(u.Buckets) > 0 {
		unlock, err := lockConfig(usagePath(Antigravity))
		if err != nil {
			return "", err
		}
		defer unlock()
		if err := saveUsage(Antigravity, u); err != nil {
			return "", err
		}
	}
	if input.Confirmation {
		return "blocked", nil
	}
	switch input.State {
	case "idle":
		return "idle", nil
	case "working", "tool_use", "initializing":
		return "working", nil
	case "reviewing", "authenticating", "error":
		return "blocked", nil
	}
	return "", nil
}

func sameDirectory(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if real, err := filepath.EvalSymlinks(a); err == nil {
		a = real
	}
	if real, err := filepath.EvalSymlinks(b); err == nil {
		b = real
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func validAntigravitySession(s antigravitySession) bool {
	if !antigravityID.MatchString(s.ID) || !filepath.IsAbs(s.Transcript) {
		return false
	}
	suffix := filepath.Join("brain", s.ID, ".system_generated", "logs", "transcript.jsonl")
	return strings.HasSuffix(filepath.Clean(s.Transcript), string(filepath.Separator)+suffix)
}

func latestAntigravitySession(root string, since time.Time) (id, path string) {
	refs := newestFirst(filepath.Join(root, ".atelier", "antigravity"), time.Time{}, func(name string) bool { return strings.HasSuffix(name, ".json") })
	var newest time.Time
	for _, ref := range refs {
		// #nosec G304 -- Reads a native transcript pointer under this workspace's metadata directory.
		data, err := os.ReadFile(ref)
		var s antigravitySession
		if err != nil || json.Unmarshal(data, &s) != nil || !validAntigravitySession(s) {
			continue
		}
		transcript := s.Transcript
		full := filepath.Join(filepath.Dir(transcript), "transcript_full.jsonl")
		if _, err := os.Stat(full); err == nil {
			transcript = full
		}
		if fi, err := os.Stat(transcript); err == nil && !fi.ModTime().Before(since) && fi.ModTime().After(newest) {
			id, path, newest = s.ID, transcript, fi.ModTime()
		}
	}
	return id, path
}
