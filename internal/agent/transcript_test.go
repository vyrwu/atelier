package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeAt writes lines as a JSONL file at path, last modified at mod.
func writeAt(t *testing.T, path string, mod time.Time, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// The conversation a space resumes is its newest interactive one. `claude -p`
// runs share the directory and are newer, a subagent's transcript sits beside
// it, and a transcript older than the space belongs to a deleted space of the
// same name — none of them count.
func TestLatestClaudeSession(t *testing.T) {
	projects := t.TempDir()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	dir := filepath.Join(projects, notAlnum.ReplaceAllString(root, "-"))
	created := time.Now().Add(-time.Hour)
	head := func(entry string) string {
		return `{"type":"user","entrypoint":"` + entry + `","cwd":"` + root + `"}`
	}
	writeAt(t, filepath.Join(dir, "old.jsonl"), created.Add(-time.Hour), head("cli"))
	writeAt(t, filepath.Join(dir, "want.jsonl"), created.Add(10*time.Minute), `{"type":"mode"}`, head("cli"))
	writeAt(t, filepath.Join(dir, "print.jsonl"), created.Add(20*time.Minute), head("sdk-cli"))
	writeAt(t, filepath.Join(dir, "want", "subagents", "agent-a.jsonl"), created.Add(30*time.Minute), head("cli"))

	id, path := latestClaudeSession(projects, root, created)
	if id != "want" || path != filepath.Join(dir, "want.jsonl") {
		t.Errorf("latest = %q %q, want the interactive session", id, path)
	}
	if id, _ := latestClaudeSession(projects, root, created.Add(15*time.Minute)); id != "" {
		t.Errorf("latest = %q, want none newer than the space", id)
	}
}

// Codex keeps every session in one tree; a space's is the newest interactive
// one whose directory is exactly the space's — not an exec run, not a
// neighbour's.
func TestLatestCodexSession(t *testing.T) {
	sessions := t.TempDir()
	created := time.Now().Add(-time.Hour)
	meta := func(id, cwd, source string) string {
		return `{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","source":"` + source + `"}}`
	}
	day := filepath.Join(sessions, "2026", "01", "01")
	writeAt(t, filepath.Join(day, "rollout-a.jsonl"), created.Add(10*time.Minute), meta("older", "/s/otter", "cli"))
	writeAt(t, filepath.Join(day, "rollout-b.jsonl"), created.Add(20*time.Minute), meta("want", "/s/otter", "cli"))
	writeAt(t, filepath.Join(day, "rollout-c.jsonl"), created.Add(30*time.Minute), meta("exec", "/s/otter", "exec"))
	writeAt(t, filepath.Join(day, "rollout-d.jsonl"), created.Add(40*time.Minute), meta("other", "/s/otter-2", "cli"))
	writeAt(t, filepath.Join(day, "notes.jsonl"), created.Add(50*time.Minute), meta("x", "/s/otter", "cli"))

	if id, _ := latestCodexSession(sessions, "/s/otter", created); id != "want" {
		t.Errorf("latest = %q, want the newest interactive session in the space", id)
	}
	if id, _ := latestCodexSession(sessions, "/s/none", created); id != "" {
		t.Errorf("latest = %q, want none", id)
	}
}

// A Claude export keeps what was said and one line per tool call; tool output,
// thinking, injected prompts, a previous handoff, and anything before the cutoff
// are left out.
func TestExportClaude(t *testing.T) {
	since := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeAt(t, path, time.Now(),
		`{"type":"user","timestamp":"2026-01-01T11:00:00Z","message":{"content":"before the swap"}}`,
		`{"type":"permission-mode","permissionMode":"auto"}`,
		`{"type":"user","timestamp":"2026-01-01T12:01:00Z","message":{"content":"fix the login bug"}}`,
		`{"type":"user","timestamp":"2026-01-01T12:01:01Z","isMeta":true,"message":{"content":"injected"}}`,
		`{"type":"user","timestamp":"2026-01-01T12:01:02Z","message":{"content":"<command-name>/model</command-name>"}}`,
		`{"type":"user","timestamp":"2026-01-01T12:01:03Z","message":{"content":"[atelier handoff] Codex worked on this space"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T12:02:00Z","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`,
		`{"type":"assistant","timestamp":"2026-01-01T12:02:01Z","message":{"content":[{"type":"text","text":"Looking at auth.go."}]}}`,
		`{"type":"assistant","timestamp":"2026-01-01T12:02:02Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","timestamp":"2026-01-01T12:02:03Z","message":{"content":[{"type":"tool_result","content":"SECRET OUTPUT"}]}}`,
	)
	got, err := Export(Claude, path, since)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Operator\n\nfix the login bug", "## Claude\n\nLooking at auth.go.", "- ran `Bash` go test ./..."} {
		if !strings.Contains(got, want) {
			t.Errorf("export lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"before the swap", "injected", "/model", "atelier handoff", "hmm", "SECRET OUTPUT"} {
		if strings.Contains(got, leak) {
			t.Errorf("export includes %q:\n%s", leak, got)
		}
	}
}

// A Codex export reads the clean messages from event_msg records and the tool
// calls from response_item ones.
func TestExportCodex(t *testing.T) {
	since := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeAt(t, path, time.Now(),
		`{"timestamp":"2026-01-01T11:00:00Z","type":"session_meta","payload":{"id":"x","cwd":"/s"}}`,
		`{"timestamp":"2026-01-01T11:30:00Z","type":"event_msg","payload":{"type":"user_message","message":"before the swap"}}`,
		`{"timestamp":"2026-01-01T12:01:00Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"sandbox rules"}]}}`,
		`{"timestamp":"2026-01-01T12:01:01Z","type":"event_msg","payload":{"type":"user_message","message":"add a test"}}`,
		`{"timestamp":"2026-01-01T12:01:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Adding one to auth_test.go."}}`,
		`{"timestamp":"2026-01-01T12:01:03Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":\"go test\"}"}}`,
		`{"timestamp":"2026-01-01T12:01:04Z","type":"response_item","payload":{"type":"function_call_output","output":"SECRET OUTPUT"}}`,
		`{"timestamp":"2026-01-01T12:01:05Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch"}}`,
	)
	got, err := Export(Codex, path, since)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Operator\n\nadd a test", "## Codex\n\nAdding one to auth_test.go.", "- ran `shell` go test", "- ran `apply_patch` *** Begin Patch"} {
		if !strings.Contains(got, want) {
			t.Errorf("export lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"before the swap", "sandbox rules", "SECRET OUTPUT"} {
		if strings.Contains(got, leak) {
			t.Errorf("export includes %q:\n%s", leak, got)
		}
	}
}

// What the summarizer gets keeps the task (the start) and where things stand
// (the end) within its cap.
func TestClip(t *testing.T) {
	if Clip("short", 100) != "short" {
		t.Error("clipped text under the cap")
	}
	text := "START\n" + strings.Repeat("middle line\n", 1000) + "END\n"
	got := Clip(text, 1000)
	if len(got) > 1100 || !strings.HasPrefix(got, "START") || !strings.HasSuffix(got, "END\n") {
		t.Errorf("Clip kept %d bytes: %q…%q", len(got), got[:20], got[len(got)-20:])
	}
}
