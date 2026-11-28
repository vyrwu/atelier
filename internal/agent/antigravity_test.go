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
	writeAt(t, mcp, time.Now(), `{"extra":9999999999999999999,"mcpServers":{"existing":{"command":"keep"},"atelier":{"timeoutSeconds":123,"env":{"CUSTOM":"keep"}}}}`)
	for range 2 {
		if err := mergeAntigravityMCP(root, "otter"); err != nil {
			t.Fatal(err)
		}
		if err := prepareAntigravitySettings(root, global); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := readJSONObject(os.DirFS(root), antigravityMCP)
	if err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	if cfg["extra"] != json.Number("9999999999999999999") || len(servers) != 2 || servers["existing"].(map[string]any)["command"] != "keep" {
		t.Fatalf("lost config: %v", cfg)
	}
	server := servers["atelier"].(map[string]any)
	if server["timeoutSeconds"] != json.Number("123") || server["env"].(map[string]any)["CUSTOM"] != "keep" {
		t.Fatalf("lost custom server settings: %v", server)
	}
	if server["env"].(map[string]any)["ATELIER_SESSION"] != "otter" || server["command"] != selfPath() {
		t.Fatalf("wrong MCP: %v", server)
	}
	cfg, err = readJSONObject(os.DirFS(root), antigravitySettings)
	if err != nil {
		t.Fatal(err)
	}
	status := cfg["statusLine"].(map[string]any)
	if len(cfg) != 2 || status["padding"] != json.Number("3") || status["stack_with_default"] != false || status["enabled"] != true {
		t.Fatalf("overlay = %v", cfg)
	}
	command := status["command"].(string)
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

func TestAntigravityKeepsWorkspaceWritesLocal(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	for _, path := range []string{antigravitySettings, antigravityMCPHash, antigravityMCP, ".atelier", ".agents"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			target := filepath.Join(outside, "settings.json")
			const original = `{"model":"keep"}`
			if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			dest := target
			if path == ".atelier" || path == ".agents" {
				dest = outside
			}
			if err := os.Symlink(dest, link); err != nil {
				t.Fatal(err)
			}
			if err := prepareAntigravity(root, "fixture"); err == nil {
				t.Fatal("accepted workspace link")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != original {
				t.Fatalf("external settings changed: %s, %v", data, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("unexpected external files: %v, %v", entries, err)
			}
		})
	}
}

func TestAntigravityKeepsBuiltInStatusLine(t *testing.T) {
	for _, setting := range []string{`true`, `false`, `{"enabled":false,"command":"disabled","stack_with_default":false}`} {
		root := t.TempDir()
		path := filepath.Join(root, "settings.json")
		writeAt(t, path, time.Now(), `{"statusLine":`+setting+`}`)
		if err := prepareAntigravitySettings(root, path); err != nil {
			t.Fatal(err)
		}
		cfg, err := readJSONObject(os.DirFS(root), antigravitySettings)
		if err != nil {
			t.Fatal(err)
		}
		st := cfg["statusLine"].(map[string]any)
		if st["stack_with_default"] != true || st["command"] != shellQuote(selfPath())+" usage antigravity "+shellQuote(root) {
			t.Errorf("%s: %v", setting, st)
		}
	}
}

func TestAntigravityRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, contents := range []string{`null`, `[]`, `broken`, `{"mcpServers":null}`, `{"mcpServers":[]}`} {
		root := t.TempDir()
		path := filepath.Join(root, ".agents", "mcp_config.json")
		writeAt(t, path, time.Now(), contents)
		if err := mergeAntigravityMCP(root, "otter"); err == nil {
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
	t.Setenv("HOME", t.TempDir())
	transcript := filepath.Join(antigravityDir(), "brain", "main-session", ".system_generated", "logs", "transcript.jsonl")
	full := filepath.Join(filepath.Dir(transcript), "transcript_full.jsonl")
	writeAt(t, transcript, now, `{}`)
	writeAt(t, full, now, `{}`)
	payload := map[string]any{
		"product": "antigravity", "conversation_id": "main-session", "transcript_path": filepath.Join(filepath.Dir(antigravityDir()), "antigravity", antigravityLogDir("main-session"), "transcript.jsonl"),
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
	if !strings.Contains(rendered, "\U00100002") || !strings.Contains(rendered, "20%") || !strings.Contains(rendered, "~") || strings.Contains(rendered, "0m") {
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
	payload["quota"] = map[string]any{"gemini-weekly": map[string]any{"remaining_fraction": 0.9, "reset_time": now.Add(time.Hour)}}
	now = now.Add(time.Second)
	capture()
	if got := LoadUsage(Antigravity); *got.Buckets[0].Primary.Used != 20 {
		t.Fatalf("older snapshot replaced current quota: %v", got)
	}
	delete(payload, "quota")
	now = now.Add(2 * time.Minute)
	capture()
	if got := LoadUsage(Antigravity); !got.Observed.Equal(u.Observed) {
		t.Fatal("missing quota refreshed cached observation")
	}
	payload["quota"] = map[string]any{"gemini-weekly": map[string]bool{"disabled": true}}
	capture()
	if got := LoadUsage(Antigravity); len(got.Buckets) != 0 {
		t.Fatalf("disabled quota still visible: %+v", got)
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

func TestAntigravityQuotaAcrossAccountsAndConversations(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(time.Hour)
	const conversation = "resumed-native-conversation"
	report := func(email, instance string, window map[string]any) Usage {
		t.Helper()
		t.Setenv(antigravityInstance, instance)
		now = now.Add(time.Second)
		payload := map[string]any{"product": "antigravity", "workspace": map[string]string{"current_dir": root}, "agent_state": "idle", "email": email, "conversation_id": conversation}
		if window != nil {
			payload["quota"] = map[string]any{"weekly": window}
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureAntigravity(data, root, now); err != nil {
			t.Fatal(err)
		}
		return LoadUsage(Antigravity)
	}
	remaining := func(f float64, until time.Time) map[string]any {
		return map[string]any{"remaining_fraction": f, "reset_time": until}
	}
	expectUsed := func(u Usage, percent float64) {
		t.Helper()
		if len(u.Buckets) != 1 || u.Buckets[0].Primary == nil || u.Buckets[0].Primary.Used == nil || *u.Buckets[0].Primary.Used != percent {
			t.Fatalf("wanted %v%% spent, got %+v", percent, u)
		}
	}
	expectHidden := func(u Usage) {
		t.Helper()
		if len(u.Buckets) != 0 {
			t.Fatalf("expected unavailable quota, got %+v", u)
		}
	}
	const a = "first@example.invalid"
	const b = "second@example.invalid"
	expectUsed(report(a, "old", remaining(0.05, reset)), 95)
	expectUsed(report(b, "current", remaining(0.9, reset)), 10)
	expectUsed(report(a, "old", remaining(0.05, reset)), 10)
	expectUsed(report(b, "other", remaining(0.8, reset)), 20)
	expectHidden(report(b, "current", map[string]any{"disabled": true}))
	data, err := os.ReadFile(usagePath(Antigravity))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), a) || strings.Contains(string(data), b) || strings.Contains(string(data), conversation) {
		t.Fatal("quota cache exposed a raw account or conversation identifier")
	}
	expectHidden(report(b, "other", remaining(0.8, reset)))
	expectHidden(report(b, "other", map[string]any{"disabled": true}))
	expectUsed(report(b, "current", remaining(0.7, reset)), 30)
	expectUsed(report(b, "other", map[string]any{"disabled": true}), 30)
	expectHidden(report(b, "current", map[string]any{"disabled": true, "reset_time": reset}))
	next := reset.Add(time.Hour)
	expectUsed(report(b, "other", remaining(0.9, next)), 10)
	expectUsed(report(b, "current", map[string]any{"disabled": true, "reset_time": reset}), 10)
	expectHidden(report("", "gateway", nil))
	expectHidden(report(b, "other", remaining(0.9, next)))
	expectUsed(report(a, Env("space", Antigravity)[antigravityInstance], remaining(0.8, next)), 20)
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

func TestAntigravityCommands(t *testing.T) {
	for _, prompt := range []string{"", "--help", "'$(touch /tmp/no)'", "!echo no", "/usage"} {
		for _, id := range []string{"", "convo-'$()"} {
			cmd := Antigravity.ResumeCmd(id, prompt)
			if !strings.HasPrefix(cmd, "agy --settings ") || strings.Contains(cmd, "--model") || strings.Contains(cmd, "--effort") || strings.Contains(cmd, "skip-permissions") {
				t.Fatalf("unexpected overrides: %s", cmd)
			}
			if id != "" && (!strings.Contains(cmd, "--conversation="+shellQuote(id)) || !strings.Contains(cmd, " || agy ")) {
				t.Errorf("resume/fallback: %s", cmd)
			}
			if prompt != "" {
				want := prompt
				if strings.HasPrefix(prompt, "/") || strings.HasPrefix(prompt, "!") {
					want = "Task: " + prompt
				}
				if !strings.Contains(cmd, "--prompt-interactive="+shellQuote(want)) {
					t.Errorf("prompt: %s", cmd)
				}
			}
		}
	}
}
