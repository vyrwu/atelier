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
