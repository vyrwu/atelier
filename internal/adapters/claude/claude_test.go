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

func TestTargetFromAgentSession(t *testing.T) {
	cases := []struct{ session, want string }{
		{"_atelier_claude_1_2", "@2"},
		{"_atelier_claude_10_47", "@47"},
		{"_claudepop_3_9", "@9"},     // legacy bash session name
		{"_atelier_lazygit_1_2", ""}, // not an agent popup
		{"main", ""},                 // a real workspace session
		{"", ""},
	}
	for _, c := range cases {
		if got := targetFromAgentSession(c.session); got != c.want {
			t.Errorf("targetFromAgentSession(%q) = %q, want %q", c.session, got, c.want)
		}
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

func TestTailNLines(t *testing.T) {
	if got := tailNLines("a\nb\nc", 2); got != "b\nc" {
		t.Errorf("tailNLines last-2 = %q", got)
	}
	if got := tailNLines("a", 5); got != "a" {
		t.Errorf("tailNLines fewer-than-n = %q", got)
	}
	if got := tailNLines("a\nb\n", 5); got != "a\nb" {
		t.Errorf("tailNLines trailing-newline = %q", got)
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
