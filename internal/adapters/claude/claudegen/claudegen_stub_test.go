package claudegen

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var _ = filepath.Join // appease unused-import in some build modes

// writeStubClaude creates an executable shell script at dir/claude that
// emits the given response on every invocation. Returns the directory it
// was placed in (so the caller can prepend it to PATH).
func writeStubClaude(t *testing.T, response string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub binary tests require POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat <<'STUB_EOF'\n" + response + "\nSTUB_EOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub claude: %v", err)
	}
	return dir
}

// withPATH prepends dir to PATH for the duration of the test.
func withPATH(t *testing.T, dir string) {
	t.Helper()
	orig := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", orig) })
	os.Setenv("PATH", dir+string(os.PathListSeparator)+orig)
}

func TestRun_StubReturnsSingleLine(t *testing.T) {
	withPATH(t, writeStubClaude(t, "feat/auth-refactor"))
	got, err := New().Run("name a branch")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "feat/auth-refactor" {
		t.Fatalf("Run: got %q want %q", got, "feat/auth-refactor")
	}
}

func TestRun_StubMultiLineReturnsFirst(t *testing.T) {
	withPATH(t, writeStubClaude(t, "feat/main-line\nignored"))
	got, err := New().Run("anything")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "feat/main-line" {
		t.Fatalf("Run: got %q want %q", got, "feat/main-line")
	}
}

func TestRecapFromTranscript_SummarizesViaClaude(t *testing.T) {
	withPATH(t, writeStubClaude(t, "running auth tests, two failing"))
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	contents := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"add auth tests"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"writing tests now"}]}}`,
		``,
	}, "\n")
	if err := os.WriteFile(transcript, []byte(contents), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	got, err := New().RecapFromTranscript(transcript)
	if err != nil {
		t.Fatalf("RecapFromTranscript: %v", err)
	}
	if got != "running auth tests, two failing" {
		t.Fatalf("got %q want %q", got, "running auth tests, two failing")
	}
}

func TestRecapFromTranscript_EmptyTranscriptReturnsEmpty(t *testing.T) {
	// Stub claude so it doesn't hang if accidentally invoked.
	withPATH(t, writeStubClaude(t, "unused"))
	transcript := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(transcript, []byte(""), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	got, err := New().RecapFromTranscript(transcript)
	if err != nil {
		t.Fatalf("RecapFromTranscript empty: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty for empty transcript, got %q", got)
	}
}

func TestRecapFromTranscript_Truncates(t *testing.T) {
	// Stub returns a string past the generous ceiling — it should truncate.
	long := strings.Repeat("x", 400)
	withPATH(t, writeStubClaude(t, long))
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	_ = os.WriteFile(transcript, []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644)
	got, err := New().RecapFromTranscript(transcript)
	if err != nil {
		t.Fatalf("RecapFromTranscript: %v", err)
	}
	if n := len([]rune(got)); n > recapFallbackMaxRunes {
		t.Fatalf("expected length ≤%d runes, got %d (%q)", recapFallbackMaxRunes, n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestRecapFromTranscript_MissingFile(t *testing.T) {
	if _, err := New().RecapFromTranscript("/nonexistent/transcript.jsonl"); err == nil {
		t.Fatalf("expected error for missing transcript")
	}
}

func TestRun_BinaryNotFound(t *testing.T) {
	// Force PATH to not contain claude
	orig := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", orig) })
	os.Setenv("PATH", "/nonexistent-path-only")

	_, err := New().Run("anything")
	if err == nil {
		t.Fatalf("expected error when claude not on PATH")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error should mention claude: %v", err)
	}
}
