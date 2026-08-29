package claude

import (
	"context"
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
		name                              string
		prompt, workspaceSys, mcp, resume string
		wantContains                      []string
		wantNotContains                   []string
	}{
		{
			name:            "no prompt, no resume, no mcp → bare claude",
			wantContains:    []string{"claude "},
			wantNotContains: []string{"--resume", "--append-system-prompt", "--mcp-config"},
		},
		{
			name:            "no prompt with mcp → claude --mcp-config",
			mcp:             "/cfg.json",
			wantContains:    []string{"claude ", "--mcp-config '/cfg.json'"},
			wantNotContains: []string{"--resume", "--append-system-prompt"},
		},
		{
			name:            "resume when no prompt",
			resume:          "sess-123",
			wantContains:    []string{"--resume 'sess-123'"},
			wantNotContains: []string{"--append-system-prompt"},
		},
		{
			name:            "resume includes mcp-config when set",
			mcp:             "/cfg.json",
			resume:          "sess-123",
			wantContains:    []string{"--mcp-config '/cfg.json'", "--resume 'sess-123'"},
			wantNotContains: []string{"--append-system-prompt"},
		},
		{
			// The respawn bug: restore re-stamps the spent one-shot @ai_prompt
			// alongside the live @ai_active_session_id, so OpenAgent hands
			// buildClaudeStartCmd BOTH. A validated resume id must win —
			// otherwise the workspace forks a fresh session and orphans the
			// prior conversation.
			name:            "validated resume wins over stale prompt",
			prompt:          "do a thing",
			workspaceSys:    "SYS",
			resume:          "sess-123",
			wantContains:    []string{"--resume 'sess-123'"},
			wantNotContains: []string{"'do a thing'", "--append-system-prompt"},
		},
		{
			name:            "fresh launch with prompt appends workspace system prompt",
			prompt:          "task",
			workspaceSys:    "SYS",
			wantContains:    []string{"--append-system-prompt 'SYS'", "'task'"},
			wantNotContains: []string{"--resume"},
		},
		{
			name:            "fresh launch with prompt + mcp",
			prompt:          "task",
			workspaceSys:    "SYS",
			mcp:             "/cfg.json",
			wantContains:    []string{"--mcp-config '/cfg.json'", "--append-system-prompt 'SYS'", "'task'"},
			wantNotContains: []string{"--resume"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildClaudeStartCmd(c.prompt, c.workspaceSys, c.mcp, c.resume)
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

// buildSummaryInput renders the WS-7 rollup input: an INTENT line, an AGENT
// RECAP line, and a PULL REQUESTS block with a compact per-PR line. Each
// section is emitted only when it has content, and empty CI/review collapse to
// the "no-ci"/"no-review" sentinels.
func TestBuildSummaryInput(t *testing.T) {
	prs := []integration.PullRequest{
		{Repo: "vyrwu/atelier", Number: 42, State: integration.ForgeOpen, CI: integration.CIPass, ReviewDecision: integration.ReviewApproved},
		{Repo: "vyrwu/cnd2", Number: 7, State: integration.ForgeDraft},
	}
	cases := []struct {
		name            string
		intent, recap   string
		prs             []integration.PullRequest
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:   "all sections",
			intent: "ship the redesign",
			recap:  "wrote statestore v3",
			prs:    prs,
			wantContains: []string{
				"INTENT: ship the redesign",
				"AGENT RECAP: wrote statestore v3",
				"PULL REQUESTS:",
				"vyrwu/atelier #42 open ci=pass review=approved",
				"vyrwu/cnd2 #7 draft ci=no-ci review=no-review",
			},
		},
		{
			name:            "intent omitted when empty",
			recap:           "did work",
			wantContains:    []string{"AGENT RECAP: did work"},
			wantNotContains: []string{"INTENT:", "PULL REQUESTS:"},
		},
		{
			name:            "prs only, no recap or intent",
			prs:             prs,
			wantContains:    []string{"PULL REQUESTS:", "vyrwu/atelier #42 open"},
			wantNotContains: []string{"INTENT:", "AGENT RECAP:"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSummaryInput(c.intent, c.recap, c.prs)
			for _, w := range c.wantContains {
				if !strings.Contains(got, w) {
					t.Errorf("input %q missing %q", got, w)
				}
			}
			for _, w := range c.wantNotContains {
				if strings.Contains(got, w) {
					t.Errorf("input %q should not contain %q", got, w)
				}
			}
			if strings.HasSuffix(got, "\n") {
				t.Errorf("input should be trailing-newline trimmed: %q", got)
			}
		})
	}
}

// SummarizeWorkspace short-circuits to "" with no model call when there's
// nothing to summarize (no agent recap and no PRs). This is the cheap-path
// guard the daemon relies on; it must not shell out to claude.
func TestSummarizeWorkspace_EmptyShortCircuit(t *testing.T) {
	got, err := New().SummarizeWorkspace(context.Background(), "some intent", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("empty recap + no PRs must short-circuit to \"\", got %q", got)
	}
}
