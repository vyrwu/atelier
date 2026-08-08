package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpTranscript bounds the transcript tail to maxTranscriptRunes, keeping
// the END (latest turns) and dropping the head; a small file passes through.
func TestDumpTranscript(t *testing.T) {
	dir := t.TempDir()

	small := filepath.Join(dir, "small.jsonl")
	if err := os.WriteFile(small, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dumpTranscript(small); got != "line1\nline2" {
		t.Fatalf("small transcript: got %q", got)
	}

	// Oversized: head marker must be dropped, tail marker kept, within the cap.
	big := filepath.Join(dir, "big.jsonl")
	body := "HEAD_MARKER" + strings.Repeat("x", maxTranscriptRunes) + "TAIL_MARKER\n"
	if err := os.WriteFile(big, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dumpTranscript(big)
	if len([]rune(got)) > maxTranscriptRunes {
		t.Fatalf("dump exceeded cap: %d runes", len([]rune(got)))
	}
	if !strings.Contains(got, "TAIL_MARKER") {
		t.Error("tail (latest turns) must be kept")
	}
	if strings.Contains(got, "HEAD_MARKER") {
		t.Error("head should be dropped when over the cap")
	}

	if dumpTranscript(filepath.Join(dir, "nope.jsonl")) != "" {
		t.Error("missing file should dump empty")
	}
}

// TestDumpTranscript_TruncatedTailIsLineAligned is the regression guard for the
// mid-line-cut bug: a rune-truncated tail must start at a whole JSONL object,
// never a partial line — a partial line can begin with '-' (a session-id
// fragment), which the `claude` CLI parses as an unknown flag, failing recaps.
func TestDumpTranscript_TruncatedTailIsLineAligned(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.jsonl")
	var b strings.Builder
	for i := 0; i < 800; i++ {
		// Each line embeds a leading-'-' fragment mid-line; if the cut kept a
		// partial line, the dump could start with it.
		fmt.Fprintf(&b, `{"id":"abc-5551ca84c229","turn":%d}`+"\n", i)
	}
	if err := os.WriteFile(f, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dumpTranscript(f)
	if len([]rune(got)) <= maxTranscriptRunes-200 || len([]rune(got)) > maxTranscriptRunes {
		t.Fatalf("expected a truncated dump near the cap; got %d runes", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "{") {
		t.Errorf("truncated dump must start at a line boundary; got prefix %q", firstN(got, 24))
	}
	if strings.HasPrefix(got, "-") {
		t.Error("truncated dump must not start with '-' (parsed as a flag by claude)")
	}
}

func firstN(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// TestAgentActivelyWorking locks the deterministic "running" signal: an agent
// is working only while a tool is in flight (a trailing tool_use, or a
// tool_result whose reply isn't written yet). A turn handed back to the user —
// an assistant message ending in text — is NOT working, so a finished/merged
// workspace never shows the blue running dot just because it wrote a reply. It
// scans past trailing non-message meta events to the last real turn.
func TestAgentActivelyWorking(t *testing.T) {
	asst := func(lastBlock string) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"x"},{"type":%q}]}}`, lastBlock)
	}
	asstText := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done, ready to merge"}]}}`
	toolResult := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	humanMsg := `{"type":"user","message":{"role":"user","content":"please continue"}}`
	meta := `{"type":"file-history-snapshot","snapshot":{}}`

	cases := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"trailing tool_use → working", []string{asstText, asst("tool_use")}, true},
		{"trailing thinking → working", []string{asstText, asst("thinking")}, true},
		{"tool_result pending reply → working", []string{asst("tool_use"), toolResult}, true},
		{"assistant text turn-end → not working", []string{toolResult, asstText}, false},
		{"plain human message → not working", []string{asstText, humanMsg}, false},
		{"skips trailing meta to the real turn", []string{toolResult, asst("tool_use"), meta}, true},
		{"skips trailing meta to a finished turn", []string{toolResult, asstText, meta}, false},
		{"empty file → not working", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "t.jsonl")
			if err := os.WriteFile(f, []byte(strings.Join(c.lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := agentActivelyWorking(f); got != c.want {
				t.Errorf("agentActivelyWorking = %v, want %v", got, c.want)
			}
		})
	}

	if agentActivelyWorking(filepath.Join(t.TempDir(), "missing.jsonl")) {
		t.Error("missing transcript must not read as working")
	}
}

// TestLastTranscriptLines returns the newest non-empty lines (newest first),
// reading only a trailing window so large transcripts stay cheap to poll.
func TestLastTranscriptLines(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "t.jsonl")
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "{\"turn\":%d}\n\n", i) // blank line between records
	}
	if err := os.WriteFile(f, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := lastTranscriptLines(f, 3)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d", len(got))
	}
	if got[0] != `{"turn":499}` {
		t.Errorf("newest-first: got[0] = %q, want turn 499", got[0])
	}
	for _, ln := range got {
		if strings.TrimSpace(ln) == "" {
			t.Error("blank lines must be skipped")
		}
	}
	if lastTranscriptLines(filepath.Join(dir, "nope.jsonl"), 3) != nil {
		t.Error("missing file → nil")
	}
}

// TestBuildRecapInput covers how the conversation tail and the git delta are
// combined — either part may be absent, and when both are present the delta is
// appended under a labeled section so the summarizer can weight real changes.
func TestBuildRecapInput(t *testing.T) {
	if got := buildRecapInput("", ""); got != "" {
		t.Errorf("both empty: got %q, want empty", got)
	}
	if got := buildRecapInput("chat", ""); got != "chat" {
		t.Errorf("delta empty: got %q, want %q", got, "chat")
	}
	if got := buildRecapInput("", "DELTA"); !strings.Contains(got, "DELTA") || strings.Contains(got, "chat") {
		t.Errorf("tail empty: got %q", got)
	}
	both := buildRecapInput("chat", "DELTA")
	if !strings.Contains(both, "chat") || !strings.Contains(both, "DELTA") {
		t.Errorf("both present: got %q, want both parts", both)
	}
	// Delta must follow the conversation (labeled section), not precede it.
	if strings.Index(both, "chat") > strings.Index(both, "DELTA") {
		t.Errorf("delta should be appended after the conversation: %q", both)
	}
}
