package agent

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

const antigravitySettings = ".atelier/antigravity-settings.json"
const antigravityMCP = ".agents/mcp_config.json"
const antigravityMCPHash = ".atelier/antigravity-mcp.sha256"

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
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := mergeAntigravityMCP(root, slug); err != nil {
		return err
	}
	return prepareAntigravitySettings(root, filepath.Join(antigravityDir(), "settings.json"))
}

func readJSONObject(files fs.FS, path string) (map[string]any, error) {
	data, err := fs.ReadFile(files, path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("%s: invalid JSON", path)
	}
	var cfg map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("%s: expected a JSON object", path)
	}
	return cfg, nil
}

func configObject(parent map[string]any, key string) (map[string]any, error) {
	if value, exists := parent[key]; exists {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("antigravity: %s must be an object", key)
		}
		return obj, nil
	}
	obj := map[string]any{}
	parent[key] = obj
	return obj, nil
}

func mergeAntigravityMCP(root, slug string) error {
	space, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = space.Close() }()
	unlock, err := lockConfig(filepath.Join(root, antigravityMCP))
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := readJSONObject(space.FS(), antigravityMCP)
	if err != nil {
		return err
	}
	servers, err := configObject(cfg, "mcpServers")
	if err != nil {
		return err
	}
	server, err := configObject(servers, "atelier")
	if err != nil {
		return err
	}
	if server["serverUrl"] != nil || server["url"] != nil {
		return fmt.Errorf("antigravity: atelier MCP server is configured with a remote URL")
	}
	env, err := configObject(server, "env")
	if err != nil {
		return err
	}
	server["command"], server["args"] = selfPath(), []string{"mcp"}
	paths := runtimePaths()
	keys := []string{"ATELIER_ROOT", "ATELIER_SOCKET", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"}
	for _, key := range keys {
		env[key] = paths[key]
	}
	env["ATELIER_SESSION"] = slug
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAntigravityFile(space, antigravityMCP, data); err != nil {
		return err
	}
	if len(cfg) == 1 && len(servers) == 1 && len(server) == 3 && len(env) == len(keys)+1 {
		// The executable and launch environment may have changed by the time purge runs.
		return writeAntigravityFile(space, antigravityMCPHash, []byte(fmt.Sprintf("%x\n", sha256.Sum256(data))))
	}
	return nil
}

// GeneratedAntigravityMCP reports whether the file still matches the generated-only config.
func GeneratedAntigravityMCP(root string) bool {
	// #nosec G304 -- Both files are workspace metadata inspected by purge.
	expected, err := os.ReadFile(filepath.Join(root, antigravityMCPHash))
	if err != nil {
		return false
	}
	// #nosec G304 -- The purge walk checks this entry is a regular file.
	data, err := os.ReadFile(filepath.Join(root, ".agents", "mcp_config.json"))
	return err == nil && string(expected) == fmt.Sprintf("%x\n", sha256.Sum256(data))
}

func prepareAntigravitySettings(root, global string) error {
	cfg, err := readJSONObject(os.DirFS(filepath.Dir(global)), filepath.Base(global))
	if err != nil {
		return err
	}
	// agy treats a boolean statusLine as a configuration without a command.
	status, _ := cfg["statusLine"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}
	// The collector inherits the instance paths pinned by Env on every launch.
	cmd := shellQuote(selfPath()) + " usage antigravity " + shellQuote(root)
	if previous, _ := status["command"].(string); previous != "" && status["enabled"] != false {
		cmd += " " + shellQuote(base64.StdEncoding.EncodeToString([]byte(previous)))
	} else {
		status["stack_with_default"] = true
	}
	status["type"], status["command"], status["enabled"] = "command", cmd, true
	// Trust only the space supplied by Atelier, for the lifetime of this launch.
	return writeAntigravityJSON(root, antigravitySettings, map[string]any{
		"statusLine": status, "trustedWorkspaces": []string{root},
	})
}

func writeAntigravityJSON(root, path string, value any) error {
	space, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = space.Close() }()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAntigravityFile(space, path, data)
}

func writeAntigravityFile(space *os.Root, path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := space.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	parent, err := space.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	info, err := parent.Lstat(name)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("antigravity: expected a regular file at %s", path)
		}
		prior, err := parent.ReadFile(name)
		if err != nil || bytes.Equal(prior, data) {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// Publish atomically within the opened directory, including across concurrent TUI reports.
	tmp := ".atelier-" + rand.Text()
	f, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Remove(tmp) }()
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return parent.Rename(tmp, name)
}

type antigravitySession struct {
	ID         string `json:"id"`
	Transcript string `json:"transcript"`
}

var antigravityID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type antigravityReport struct {
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

// The TUI's documented status payload identifies its main conversation. Keep
// only a pointer to the native transcript, never a copy of messages or tokens.
// Initial --prompt-interactive messages do not appear in native history.jsonl;
// the CLI's SQLite summaries can also omit workspace paths, so neither reliably
// associates a conversation with a space. The pointer survives state recovery.
func CaptureAntigravity(data []byte, root string, now time.Time) (string, error) {
	var input antigravityReport
	if err := json.Unmarshal(data, &input); err != nil {
		return "", err
	}
	if input.Product != "antigravity" || (!sameDirectory(root, input.Workspace.Current) && !sameDirectory(root, input.Workspace.Project)) {
		return "", nil
	}
	// Authentication and initialization can report status before a conversation
	// exists. They must still reach the attention badge.
	s := antigravitySession{input.ID, input.Transcript}
	var refErr error
	if validAntigravitySession(s) {
		refErr = writeAntigravityJSON(root, filepath.Join(".atelier", "antigravity", s.ID+".json"), s)
	}
	return input.status(), errors.Join(refErr, input.saveQuota(now))
}

func (input antigravityReport) saveQuota(now time.Time) error {
	// Startup and gateway accounts can omit the snapshot. An explicit disabled
	// bucket, however, invalidates its previous percentage immediately.
	if len(input.Quota) == 0 {
		return nil
	}
	unlock, err := lockConfig(usagePath(Antigravity))
	if err != nil {
		return err
	}
	defer unlock()
	cached := LoadUsage(Antigravity)
	if cached.Observed.After(now) {
		return nil
	}
	buckets := map[string]UsageBucket{}
	for _, b := range cached.Buckets {
		if b.Primary != nil && b.Primary.Reset > now.Unix() && b.Primary.Used != nil {
			buckets[b.ID] = b
		}
	}
	for key, q := range input.Quota {
		if q == nil || q.Disabled || q.Remaining == nil || *q.Remaining < 0 || *q.Remaining > 1 {
			delete(buckets, key)
			continue
		}
		used := math.Round((1-*q.Remaining)*1e6) / 1e4
		reset := q.Reset.Unix()
		// Another space can repaint a previous snapshot of the same account.
		if old := buckets[key].Primary; old != nil && (old.Reset > reset || (old.Reset == reset && *old.Used > used)) {
			continue
		}
		if reset > now.Unix() {
			buckets[key] = UsageBucket{ID: key, Name: key, Primary: &UsageWindow{Used: &used, Reset: reset}}
		}
	}
	u := Usage{Observed: now, Source: "statusline"}
	for _, key := range slices.Sorted(maps.Keys(buckets)) {
		u.Buckets = append(u.Buckets, buckets[key])
	}
	if now.Sub(cached.Observed) < time.Minute && reflect.DeepEqual(u.Buckets, cached.Buckets) {
		return nil
	}
	return saveUsage(Antigravity, u)
}

func (input antigravityReport) status() string {
	if input.Confirmation {
		return "blocked"
	}
	switch input.State {
	case "idle":
		return "idle"
	case "working", "tool_use", "initializing":
		return "working"
	case "reviewing", "authenticating", "error":
		return "blocked"
	}
	return ""
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
	dir := antigravityDir()
	shared := filepath.Join(filepath.Dir(dir), "antigravity")
	return s.Transcript == filepath.Join(dir, antigravityLogDir(s.ID), "transcript.jsonl") ||
		s.Transcript == filepath.Join(shared, antigravityLogDir(s.ID), "transcript.jsonl")
}

func antigravityLogDir(id string) string {
	return filepath.Join("brain", id, ".system_generated", "logs")
}

func latestAntigravitySession(root string, since time.Time) (id, path string) {
	refs := newestFirst(filepath.Join(root, ".atelier", "antigravity"), time.Time{}, func(name string) bool { return strings.HasSuffix(name, ".json") })
	var newest time.Time
	for _, ref := range refs {
		// #nosec G304 -- Reads a native transcript pointer under this workspace's metadata directory.
		data, err := os.ReadFile(ref)
		var s antigravitySession
		if err != nil || json.Unmarshal(data, &s) != nil || !validAntigravitySession(s) || filepath.Base(ref) != s.ID+".json" {
			continue
		}
		// agy 1.5.3 reports the shared brain path even when it writes the CLI brain.
		for _, dir := range []string{filepath.Dir(s.Transcript), filepath.Join(antigravityDir(), antigravityLogDir(s.ID))} {
			for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
				candidate := filepath.Join(dir, name)
				fi, err := os.Stat(candidate)
				if err != nil || !fi.Mode().IsRegular() {
					continue
				}
				if !fi.ModTime().Before(since) && fi.ModTime().After(newest) {
					id, path, newest = s.ID, candidate, fi.ModTime()
				}
				break
			}
		}
	}
	return id, path
}
