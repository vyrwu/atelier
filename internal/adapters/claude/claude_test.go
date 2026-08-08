package claude

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

// Adapter must satisfy the port (compile-time guard also in claude.go).
func TestAdapter_SatisfiesPort(t *testing.T) {
	var _ integration.AIIntegration = New()
	if New().Name() != "claude" {
		t.Errorf("Name = %q, want claude", New().Name())
	}
	if New().DisplayName() != "Claude Code" {
		t.Errorf("DisplayName = %q, want Claude Code", New().DisplayName())
	}
}

func TestBuildClaudeStartCmd(t *testing.T) {
	cases := []struct {
		name                                         string
		prompt, kind, multiRepoSys, settings, resume string
		wantContains                                 []string
		wantNotContains                              []string
	}{
		{
			name:            "no prompt, no resume",
			settings:        "/s.json",
			wantContains:    []string{"claude ", "--settings '/s.json'"},
			wantNotContains: []string{"--resume", "--append-system-prompt"},
		},
		{
			name:     "resume when no prompt",
			settings: "/s.json", resume: "sess-123",
			wantContains:    []string{"--settings '/s.json'", "--resume 'sess-123'"},
			wantNotContains: []string{"--append-system-prompt"},
		},
		{
			// The respawn bug: restore re-stamps the spent one-shot @ai_prompt
			// alongside the live @ai_active_session_id, so OpenAgent hands
			// buildClaudeStartCmd BOTH. A validated resume id must win —
			// otherwise the workspace forks a fresh session and orphans the
			// prior conversation.
			name:   "validated resume wins over stale prompt",
			prompt: "do a thing", settings: "/s.json", resume: "sess-123",
			wantContains:    []string{"--resume 'sess-123'"},
			wantNotContains: []string{"'do a thing'", "--append-system-prompt"},
		},
		{
			// Multi-repo respawn: same precedence, resume still wins and the
			// stale prompt / system prompt are not replayed.
			name:   "validated resume wins over stale multi-repo prompt",
			prompt: "task", kind: WorkspaceKindMultiRepo, multiRepoSys: "SYS", settings: "/s.json", resume: "sess-9",
			wantContains:    []string{"--resume 'sess-9'"},
			wantNotContains: []string{"'task'", "--append-system-prompt"},
		},
		{
			name:   "multi-repo appends system prompt",
			prompt: "task", kind: WorkspaceKindMultiRepo, multiRepoSys: "SYS", settings: "/s.json",
			wantContains: []string{"--append-system-prompt 'SYS'", "'task'"},
		},
		{
			name:            "worktree prompt, no settings",
			prompt:          "task",
			wantContains:    []string{"claude ", "'task'"},
			wantNotContains: []string{"--settings", "--append-system-prompt"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildClaudeStartCmd(c.prompt, c.kind, c.multiRepoSys, c.settings, c.resume)
			for _, w := range c.wantContains {
				if !strings.Contains(got, w) {
					t.Errorf("cmd %q missing %q", got, w)
				}
			}
			for _, w := range c.wantNotContains {
				if strings.Contains(got, w) {
					t.Errorf("cmd %q should not contain %q", got, w)
				}
			}
		})
	}
}

func TestParseRecapVerdict(t *testing.T) {
	cases := []struct {
		name, raw, wantRecap, wantStatus string
	}{
		{"blocked", "ATTENTION: blocked\nasked which db driver to use", "asked which db driver to use", "blocked"},
		{"running", "ATTENTION: running\nrunning migration", "running migration", "running"},
		{"idle", "ATTENTION: idle\nfinished refactor", "finished refactor", "idle"},
		{"case-insensitive + extra words", "attention: BLOCKED — waiting\nneeds review", "needs review", "blocked"},
		{"missing verdict defaults idle", "just a recap line", "just a recap line", "idle"},
		{"unknown verdict → idle", "ATTENTION: banana\nrecap", "recap", "idle"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recap, status := parseRecapVerdict(c.raw)
			if recap != c.wantRecap {
				t.Errorf("recap = %q, want %q", recap, c.wantRecap)
			}
			if status != c.wantStatus {
				t.Errorf("status = %q, want %q", status, c.wantStatus)
			}
		})
	}
}

func TestEnsurePrefix(t *testing.T) {
	cases := []struct{ in, prefix, want string }{
		{"", "@", ""},
		{"@3", "@", "@3"},
		{"3", "@", "@3"},
		{"$1", "$", "$1"},
	}
	for _, c := range cases {
		if got := ensurePrefix(c.in, c.prefix); got != c.want {
			t.Errorf("ensurePrefix(%q,%q)=%q want %q", c.in, c.prefix, got, c.want)
		}
	}
}

func TestResumeIDForLaunch_GuardsMissingTranscript(t *testing.T) {
	// Empty stored id → no resume. A stored id whose transcript is absent
	// (isolated HOME → no ~/.claude transcripts) → no resume, and the id is
	// NOT erased (non-mutating). Present-transcript resume is covered by
	// claudeproj's own tests.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := resumeIDForLaunch(""); got != "" {
		t.Errorf("empty stored id should not resume, got %q", got)
	}
	if got := resumeIDForLaunch("nonexistent-session-uuid"); got != "" {
		t.Errorf("id with no transcript should not resume, got %q", got)
	}
}

func TestTruncateLine(t *testing.T) {
	cases := []struct {
		in, want string
		max      int
	}{
		{"short", "short", 75},
		{"first line\nsecond line", "first line", 75},
		{`"quoted"`, "quoted", 75},
		{"Recap: did the thing", "did the thing", 75},
		{"abcdefghij", "abcd…", 5},
	}
	for _, c := range cases {
		if got := truncateLine(c.in, c.max); got != c.want {
			t.Errorf("truncateLine(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}
