package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func grokFixture(t *testing.T, path, body string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestLatestGrokSession(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, entry := range []struct{ id, cwd, kind, extra string }{
		{"wanted", "/space", "", `,"parent_session_id":"fork-source"`},
		{"headless", "/space", "headless", ""},
		{"child", "/space", "subagent", ""},
		{"elsewhere", "/other", "", ""},
		{"hidden", "/space", "", `,"hidden":true`},
	} {
		at := now.Add(time.Duration(i) * time.Second)
		base := filepath.Join(dir, "encoded-cwd", entry.id)
		grokFixture(t, filepath.Join(base, "summary.json"), `{"info":{"id":"`+entry.id+`","cwd":"`+entry.cwd+`"},"session_kind":"`+entry.kind+`"`+entry.extra+`}`, at)
		grokFixture(t, filepath.Join(base, "updates.jsonl"), "", at)
	}
	id, path := latestGrokSession(dir, "/space", now.Add(-time.Minute))
	if id != "wanted" || filepath.Base(path) != "updates.jsonl" {
		t.Fatalf("got %q %q", id, path)
	}
	if id, _ := latestGrokSession(dir, "/space", now.Add(time.Second)); id != "" {
		t.Fatalf("revived an older space: %s", id)
	}
}

func TestExportGrok(t *testing.T) {
	w := &exportWriter{agent: Grok.Label()}
	input := strings.Join([]string{
		`{"timestamp":100,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"old reply"}}}}`,
		`{"timestamp":200,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"fix it"}}}}`,
		`{"timestamp":200,"params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"private reasoning"}}}}`,
		`{"timestamp":201,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Checking "}}}}`,
		`{"timestamp":201,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"the test."}}}}`,
		`{"timestamp":201,"params":{"update":{"sessionUpdate":"tool_call","title":"run_terminal_command","rawInput":{"command":"go test ./..."}}}}`,
		`{"timestamp":202,"params":{"update":{"sessionUpdate":"tool_call_update","content":[{"type":"text","text":"large tool output"}]}}}`,
	}, "\n")
	if err := exportGrok(strings.NewReader(input), time.Unix(150, 0), w); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Operator", "fix it", "## Grok Build", "Checking the test.", "go test ./..."} {
		if !strings.Contains(w.String(), want) {
			t.Errorf("missing %q: %s", want, w.String())
		}
	}
	for _, skip := range []string{"old reply", "private reasoning", "large tool output"} {
		if strings.Contains(w.String(), skip) {
			t.Errorf("included %q", skip)
		}
	}
}

func TestGrokExportKeepsUserMessagesAfterHandoff(t *testing.T) {
	w := &exportWriter{agent: Grok.Label()}
	input := strings.Join([]string{
		`{"timestamp":200,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"[atelier handoff] Read the export."}}}}`,
		`{"timestamp":201,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"Also fix the race."}}}}`,
		`{"timestamp":202,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"Keep the public API."}}}}`,
		`{"timestamp":203,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"I will check both."}}}}`,
	}, "\n")
	if err := exportGrok(strings.NewReader(input), time.Unix(150, 0), w); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Also fix the race.\n\nKeep the public API.", "I will check both."} {
		if !strings.Contains(w.String(), want) {
			t.Fatalf("lost user steering %q: %s", want, w.String())
		}
	}
	if strings.Contains(w.String(), HandoffMark) {
		t.Fatal("export included its own handoff note")
	}
}
