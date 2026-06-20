package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildClaudeStartCmd_NoPrompt(t *testing.T) {
	got := buildClaudeStartCmd("", "", "sys", "", "")
	if strings.TrimSpace(got) != "claude" {
		t.Fatalf("got %q want %q", got, "claude")
	}
}

func TestBuildClaudeStartCmd_NoPrompt_WithSettings(t *testing.T) {
	got := buildClaudeStartCmd("", "", "sys", "/tmp/atelier-settings.json", "")
	if !strings.Contains(got, "--settings '/tmp/atelier-settings.json'") {
		t.Fatalf("expected --settings flag, got %q", got)
	}
}

func TestBuildClaudeStartCmd_WorktreePrompt(t *testing.T) {
	got := buildClaudeStartCmd("fix the test", WorkspaceKindWorktree, "sys", "", "")
	if !strings.HasPrefix(got, "claude '") {
		t.Fatalf("expected claude '<prompt>', got %q", got)
	}
	if strings.Contains(got, "--append-system-prompt") {
		t.Fatalf("worktree must NOT add --append-system-prompt: %q", got)
	}
	if !strings.Contains(got, "fix the test") {
		t.Fatalf("missing prompt in: %q", got)
	}
}

func TestBuildClaudeStartCmd_WorktreePrompt_WithSettings(t *testing.T) {
	got := buildClaudeStartCmd("fix the test", WorkspaceKindWorktree, "sys", "/p/s.json", "")
	if !strings.Contains(got, "--settings '/p/s.json'") {
		t.Fatalf("missing --settings: %q", got)
	}
	if !strings.Contains(got, "'fix the test'") {
		t.Fatalf("missing user prompt: %q", got)
	}
}

func TestBuildClaudeStartCmd_MultiRepo_AppendsSystemPrompt(t *testing.T) {
	got := buildClaudeStartCmd("do thing", WorkspaceKindMultiRepo, "SYSTEM PROMPT", "", "")
	if !strings.Contains(got, "--append-system-prompt 'SYSTEM PROMPT'") {
		t.Fatalf("expected sys prompt single-quoted, got %q", got)
	}
	if !strings.Contains(got, "'do thing'") {
		t.Fatalf("expected user prompt single-quoted, got %q", got)
	}
}

func TestBuildClaudeStartCmd_MultiRepo_WithSettings(t *testing.T) {
	got := buildClaudeStartCmd("do thing", WorkspaceKindMultiRepo, "SYS", "/s.json", "")
	for _, want := range []string{"--settings '/s.json'", "--append-system-prompt 'SYS'", "'do thing'"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %s", want, got)
		}
	}
}

// TestBuildClaudeStartCmd_ResumeWhenSessionIDSetAndPromptEmpty locks
// in the auto-resume contract: when @claude_active_session_id is set
// (notify-attention stamped it after Claude's last task) and no fresh
// prompt is queued, open with --resume <id>. This is what makes the
// "tmux died, restored workspace, Claude picks up where it left off"
// flow work end-to-end.
func TestBuildClaudeStartCmd_ResumeWhenSessionIDSetAndPromptEmpty(t *testing.T) {
	got := buildClaudeStartCmd("", "", "sys", "/s.json", "abc-uuid")
	if !strings.Contains(got, "--resume 'abc-uuid'") {
		t.Errorf("expected --resume 'abc-uuid', got %q", got)
	}
}

// TestBuildClaudeStartCmd_PromptOverridesResume covers the precedence:
// when a fresh @claude_prompt is queued, the user wants a NEW
// conversation — resume must be ignored. (Otherwise the prompt the
// user just typed would be lost into a stale conversation.)
func TestBuildClaudeStartCmd_PromptOverridesResume(t *testing.T) {
	got := buildClaudeStartCmd("fresh task", WorkspaceKindWorktree, "sys", "", "abc-uuid")
	if strings.Contains(got, "--resume") {
		t.Errorf("prompt should override resume, got %q", got)
	}
	if !strings.Contains(got, "'fresh task'") {
		t.Errorf("missing fresh prompt: %q", got)
	}
}

// TestBuildClaudeStartCmd_NoResumeWhenIDEmpty asserts the absent-id
// path doesn't accidentally emit a stray --resume flag.
func TestBuildClaudeStartCmd_NoResumeWhenIDEmpty(t *testing.T) {
	got := buildClaudeStartCmd("", "", "sys", "", "")
	if strings.Contains(got, "--resume") {
		t.Errorf("no session id should mean no --resume, got %q", got)
	}
}

// TestClaudeSessionIDFromPath locks in the transcript-path → session-id
// parser used by notify-attention. Claude writes
// `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl` per session; the
// filename STEM is the session id passed to `claude --resume <id>`.
// If the parser drifts (e.g. Claude changes the extension), the entire
// auto-resume story silently breaks.
func TestClaudeSessionIDFromPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/Users/u/.claude/projects/-cwd/abc-123-def-456.jsonl", "abc-123-def-456"},
		{"abc-123.jsonl", "abc-123"},                                  // bare filename
		{"/abs/path/foo-bar.jsonl", "foo-bar"},
		{"", ""},                                                      // empty input → empty id
		{"/no/extension/file", ""},                                    // wrong extension → empty
		{"/wrong.txt", ""},                                            // wrong extension → empty
		{"/Users/u/.claude/projects/-cwd-with-dashes/uuid.jsonl", "uuid"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := claudeSessionIDFromPath(tc.in); got != tc.want {
				t.Errorf("claudeSessionIDFromPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTranscriptExists locks in the stale-session-id detection. The
// glob spans `~/.claude/projects/*/<id>.jsonl` — when present, the
// resume signal is honored; when absent, OpenCommand clears the stale
// id and starts fresh.
func TestTranscriptExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, ".claude", "projects", "-some-encoded-cwd")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(proj, "abc-123.jsonl")
	if err := os.WriteFile(good, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !transcriptExists("abc-123") {
		t.Error("transcript exists on disk but transcriptExists returned false")
	}
	if transcriptExists("nonexistent-uuid") {
		t.Error("no transcript on disk but transcriptExists returned true")
	}
	if transcriptExists("") {
		t.Error("empty id should not match any file")
	}
}

func TestShellQuote_EscapesSingleQuote(t *testing.T) {
	got := shellQuote("it's a test")
	want := `'it'\''s a test'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTruncateLine_Caps75(t *testing.T) {
	s := strings.Repeat("a", 100)
	got := truncateLine(s, 75)
	if len([]rune(got)) != 75 {
		t.Fatalf("expected 75 runes, got %d (%q)", len([]rune(got)), got)
	}
}

func TestTruncateLine_FirstLineOnly(t *testing.T) {
	got := truncateLine("first\nsecond", 75)
	if got != "first" {
		t.Fatalf("got %q want %q", got, "first")
	}
}

// TestTruncateLine_AddsEllipsisOnOverflow locks in the visible "this got
// cut" marker — when the model overshoots the length cap, the recap
// should end in `…` rather than silently dropping mid-word with no hint.
func TestTruncateLine_AddsEllipsisOnOverflow(t *testing.T) {
	in := strings.Repeat("a", 100)
	got := truncateLine(in, RecapMaxRunes)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated recap should end in `…`, got %q", got)
	}
	if got := []rune(got); len(got) != RecapMaxRunes {
		t.Errorf("expected exactly %d runes, got %d", RecapMaxRunes, len(got))
	}
}

// TestTruncateLine_NoEllipsisWhenWithinLimit asserts the ellipsis is
// only added when truncation actually happens.
func TestTruncateLine_NoEllipsisWhenWithinLimit(t *testing.T) {
	in := "short recap"
	got := truncateLine(in, RecapMaxRunes)
	if got != in {
		t.Errorf("within-limit input changed: got %q want %q", got, in)
	}
}

// TestTruncateLine_StripsQuotes covers Claude's frequent habit of
// wrapping recap output in quotes despite the system prompt forbidding
// it. We strip both straight and curly variants.
func TestTruncateLine_StripsQuotes(t *testing.T) {
	cases := map[string]string{
		`"quoted"`:   "quoted",
		`'apostrophe'`: "apostrophe",
		`“curly”`:    "curly",
		`‘single curly’`: "single curly",
	}
	for in, want := range cases {
		if got := truncateLine(in, RecapMaxRunes); got != want {
			t.Errorf("truncateLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTruncateLine_StripsLeadingLabel covers Claude's habit of
// prefixing a "Recap:" label even though the prompt forbids it.
func TestTruncateLine_StripsLeadingLabel(t *testing.T) {
	cases := map[string]string{
		"Recap: did the thing":   "did the thing",
		"Summary: did the thing": "did the thing",
		"recap: lowercase too":   "lowercase too",
	}
	for in, want := range cases {
		if got := truncateLine(in, RecapMaxRunes); got != want {
			t.Errorf("truncateLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRecapMaxRunes_MatchesPromptAdvisory keeps the system-prompt
// advisory (75) and the post-processor cap (RecapMaxRunes) from
// drifting apart. If the constant changes, the prompt must change
// to match — otherwise the model is told one limit while we enforce
// another.
func TestRecapMaxRunes_MatchesPromptAdvisory(t *testing.T) {
	if !strings.Contains(DefaultRecapSystemPrompt, "≤75 characters") &&
		!strings.Contains(DefaultRecapSystemPrompt, "75 characters") {
		t.Errorf("system prompt no longer mentions %d-char limit; keep prompt + RecapMaxRunes in sync", RecapMaxRunes)
	}
	if RecapMaxRunes != 75 {
		t.Errorf("RecapMaxRunes changed to %d but prompt still says 75 — update both", RecapMaxRunes)
	}
}

func TestTailNLines_KeepsTail(t *testing.T) {
	in := strings.Join([]string{"a", "b", "c", "d", "e"}, "\n")
	got := tailNLines(in, 2)
	if got != "d\ne" {
		t.Fatalf("got %q want %q", got, "d\ne")
	}
}

func TestEnsurePrefix(t *testing.T) {
	if got := ensurePrefix("123", "@"); got != "@123" {
		t.Fatalf("got %q", got)
	}
	if got := ensurePrefix("@123", "@"); got != "@123" {
		t.Fatalf("got %q", got)
	}
	if got := ensurePrefix("", "@"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLatestRecap_MissingProject(t *testing.T) {
	got, err := LatestRecap("/nonexistent/path/that/should/never/exist")
	if err != nil {
		t.Fatalf("LatestRecap: %v", err)
	}
	if got != "" {
		t.Fatalf("LatestRecap: expected empty, got %q", got)
	}
}

func TestLatestRecap_EmptyProject(t *testing.T) {
	got, err := LatestRecap("")
	if err != nil {
		t.Fatalf("LatestRecap empty project: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty for empty project, got %q", got)
	}
}

func TestFindLatestTranscript_MissingDirReturnsEmpty(t *testing.T) {
	got, err := findLatestTranscript("/totally/nonexistent")
	if err != nil {
		t.Fatalf("findLatestTranscript: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
