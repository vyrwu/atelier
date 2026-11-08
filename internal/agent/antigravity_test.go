package agent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityConfiguration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ATELIER_SOCKET", "test-socket")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	global := filepath.Join(t.TempDir(), "settings.json")
	original := `{"model":"keep-model","effort":"high","statusLine":{"type":"command","command":"cat | jq .model","padding":3,"stack_with_default":false}}`
	writeAt(t, global, time.Now(), original)
	mcp := filepath.Join(root, ".agents", "mcp_config.json")
	writeAt(t, mcp, time.Now(), `{"extra":true,"mcpServers":{"existing":{"command":"keep"}}}`)
	for range 2 {
		if err := mergeAntigravityMCP(mcp, "otter"); err != nil {
			t.Fatal(err)
		}
		if err := prepareAntigravitySettings(root, global); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := readJSONObject(mcp)
	if err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	if cfg["extra"] != true || len(servers) != 2 || servers["existing"].(map[string]any)["command"] != "keep" {
		t.Fatalf("lost config: %v", cfg)
	}
	server := servers["atelier"].(map[string]any)
	if server["env"].(map[string]any)["ATELIER_SESSION"] != "otter" || server["command"] != selfPath() {
		t.Fatalf("wrong MCP: %v", server)
	}
	cfg, err = readJSONObject(filepath.Join(root, antigravitySettings))
	if err != nil {
		t.Fatal(err)
	}
	status := cfg["statusLine"].(map[string]any)
	if len(cfg) != 2 || status["padding"] != float64(3) || status["stack_with_default"] != false || status["enabled"] != true {
		t.Fatalf("overlay = %v", cfg)
	}
	command := status["command"].(string)
	for _, key := range []string{"XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "ATELIER_ROOT", "ATELIER_SOCKET"} {
		if !strings.Contains(command, key+"=") {
			t.Errorf("collector lacks %s", key)
		}
	}
	if !strings.Contains(command, "usage antigravity "+shellQuote(root)) || !strings.Contains(command, base64.StdEncoding.EncodeToString([]byte("cat | jq .model"))) {
		t.Fatalf("collector = %s", command)
	}
	data, _ := os.ReadFile(global)
	if string(data) != original+"\n" {
		t.Fatal("global settings were changed")
	}
	fi, err := os.Stat(filepath.Join(root, antigravitySettings))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("overlay permissions: %v %v", fi, err)
	}
}

func TestAntigravityRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, contents := range []string{`null`, `[]`, `broken`, `{"mcpServers":null}`, `{"mcpServers":[]}`} {
		path := filepath.Join(t.TempDir(), "mcp_config.json")
		writeAt(t, path, time.Now(), contents)
		if err := mergeAntigravityMCP(path, "otter"); err == nil {
			t.Errorf("accepted %s", contents)
		}
		got, _ := os.ReadFile(path)
		if string(got) != contents+"\n" {
			t.Errorf("overwrote %s", contents)
		}
	}
}

func TestAntigravityCaptureAndResume(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	transcript := filepath.Join(t.TempDir(), "brain", "main-session", ".system_generated", "logs", "transcript.jsonl")
	full := filepath.Join(filepath.Dir(transcript), "transcript_full.jsonl")
	writeAt(t, transcript, now, `{}`)
	writeAt(t, full, now, `{}`)
	payload := map[string]any{
		"product": "antigravity", "conversation_id": "main-session", "transcript_path": transcript,
		"workspace": map[string]string{"current_dir": root}, "agent_state": "working",
		"quota": map[string]any{
			"gemini-weekly": map[string]any{"remaining_fraction": 0.8, "reset_time": now.Add(time.Hour)},
			"tokens":        map[string]any{"remaining_amount": 10000, "reset_time": now.Add(time.Hour)},
			"disabled":      map[string]any{"remaining_fraction": 0.5, "reset_time": now.Add(time.Hour), "disabled": true},
			"expired":       map[string]any{"remaining_fraction": 0.5, "reset_time": now.Add(-time.Hour)},
			"invalid":       map[string]any{"remaining_fraction": 1.5, "reset_time": now.Add(time.Hour)},
		},
	}
	capture := func() string {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		status, err := CaptureAntigravity(data, root, now)
		if err != nil {
			t.Fatal(err)
		}
		return status
	}
	for state, want := range map[string]string{"idle": "idle", "working": "working", "tool_use": "working", "reviewing": "blocked", "error": "blocked", "authenticating": "blocked", "initializing": "working", "future": ""} {
		payload["agent_state"] = state
		if got := capture(); got != want {
			t.Errorf("state %s = %s, want %s", state, got, want)
		}
	}
	if id, path := latestSession(Antigravity, root, now.Add(-time.Minute)); id != "main-session" || path != full {
		t.Fatalf("session = %s %s", id, path)
	}
	if id, _ := latestSession(Antigravity, root, now.Add(time.Second)); id != "" {
		t.Fatal("resumed a session older than the space")
	}
	u := LoadUsage(Antigravity)
	if len(u.Buckets) != 1 || *u.Buckets[0].Primary.Used != 20 {
		t.Fatalf("quota = %+v", u)
	}
	rendered := renderUsage(Antigravity, u, now)
	if !strings.Contains(rendered, "20%") || !strings.Contains(rendered, "~") || strings.Contains(rendered, "0m") {
		t.Fatalf("quota display = %s", rendered)
	}
	if strings.Contains(renderUsage(Antigravity, u, now.Add(2*time.Hour)), "%") {
		t.Fatal("expired quota remains visible")
	}
	payload["tool_confirmation_pending"] = true
	payload["agent_state"] = "tool_use"
	if got := capture(); got != "blocked" {
		t.Fatal("permission dialog did not request attention")
	}
	delete(payload, "tool_confirmation_pending")
	delete(payload, "conversation_id")
	delete(payload, "transcript_path")
	payload["agent_state"] = "authenticating"
	if got := capture(); got != "blocked" {
		t.Fatal("authentication before first conversation did not request attention")
	}
	delete(payload, "quota")
	capture()
	if got := LoadUsage(Antigravity); !got.Observed.Equal(u.Observed) {
		t.Fatal("missing quota refreshed cached observation")
	}
	payload["workspace"] = map[string]string{"current_dir": root + "-other"}
	if got := capture(); got != "" {
		t.Fatal("accepted a different workspace")
	}
	if validAntigravitySession(antigravitySession{"../escape", transcript}) || validAntigravitySession(antigravitySession{"main-session", "/tmp/arbitrary"}) {
		t.Fatal("accepted unsafe transcript pointer")
	}
	// A transcript deleted by the native CLI cannot be resumed.
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	if id, _ := latestSession(Antigravity, root, time.Time{}); id != "" {
		t.Fatal("resumed a missing transcript")
	}
}

func TestAntigravityExport(t *testing.T) {
	transcript := strings.Join([]string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-11-08T13:45:24Z","content":"<USER_REQUEST>\nFix the test\n</USER_REQUEST>\n<ADDITIONAL_METADATA>secret</ADDITIONAL_METADATA>"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-11-08T13:45:25Z","content":"I will check it.","thinking":"hidden reasoning","tool_calls":[{"name":"run_command","args":{"command":"go test ./..."}}]}`,
		`{"source":"SYSTEM","type":"USER_INPUT","created_at":"2026-11-08T13:45:26Z","content":"injected"}`,
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-11-08T13:45:26Z","content":"<USER_REQUEST>\n[atelier handoff] previous\n</USER_REQUEST>"}`,
		`{"source":"MODEL","type":"TOOL_RESPONSE","created_at":"2026-11-08T13:45:27Z","content":"tool output"}`,
	}, "\n")
	since, _ := time.Parse(time.RFC3339, "2026-11-08T13:45:24Z")
	w := &exportWriter{agent: Antigravity.Label()}
	if err := exportAntigravity(strings.NewReader(transcript), since.Add(500*time.Millisecond), w); err != nil {
		t.Fatal(err)
	}
	got := w.String()
	for _, want := range []string{"## Operator", "Fix the test", "## Antigravity", "I will check it.", "`run_command` go test ./..."} {
		if !strings.Contains(got, want) {
			t.Errorf("export lacks %s: %s", want, got)
		}
	}
	for _, hidden := range []string{"secret", "hidden reasoning", "injected", "previous", "tool output"} {
		if strings.Contains(got, hidden) {
			t.Errorf("export leaked %s", hidden)
		}
	}
	w = &exportWriter{agent: Antigravity.Label()}
	if err := exportAntigravity(strings.NewReader(transcript), since.Add(time.Second), w); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.String(), "Fix the test") {
		t.Fatal("handoff cutoff ignored")
	}
	if err := exportAntigravity(strings.NewReader("not JSON"), since, w); err == nil {
		t.Fatal("invalid transcript was silently accepted")
	}
}
