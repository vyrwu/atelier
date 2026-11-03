package agent

import (
	"strings"
	"testing"
)

// State and config written before there was a choice of agent say nothing, and
// mean Claude; so does anything unrecognised.
func TestOfDefaultsToClaude(t *testing.T) {
	for name, want := range map[string]Kind{"": Claude, "claude": Claude, "codex": Codex, "gpt": Claude} {
		if got := Of(name); got != want {
			t.Errorf("Of(%q) = %q, want %q", name, got, want)
		}
	}
	if Claude.Other() != Codex || Codex.Other() != Claude {
		t.Error("Other must flip between the two agents")
	}
}

// The prompt is user-written prose that tmux runs through a shell: quotes,
// backticks, and semicolons in it must stay data, for either agent.
func TestLaunchCmdQuotesPrompt(t *testing.T) {
	for _, k := range []Kind{Claude, Codex} {
		for _, p := range []string{"it's broken", "rm -rf /; echo pwned", "use `git log` and $HOME", `say "hi"`} {
			cmd := k.LaunchCmd(p)
			if !strings.HasPrefix(cmd, string(k)+" ") || !strings.HasSuffix(cmd, "'") {
				t.Errorf("%s.LaunchCmd(%q) = %q, want the prompt single-quoted last", k, p, cmd)
			}
			if !strings.Contains(cmd, shellQuote(p)) {
				t.Errorf("%s.LaunchCmd(%q) = %q, want the prompt quoted whole", k, p, cmd)
			}
		}
	}
}

// Every Codex launch runs unsandboxed and with a -c override: without one Codex
// attaches to a running app-server daemon, whose hooks lack the space's env.
func TestCodexCommandsCarryTheirFlags(t *testing.T) {
	for _, cmd := range []string{
		Codex.LaunchCmd("go"),
		Codex.ResumeCmd("", "go"),
		Codex.ResumeCmd("abc", "go"),
		Codex.ResumeCmd("abc", ""),
	} {
		for _, part := range strings.Split(cmd, " || ") {
			if !strings.Contains(part, "--dangerously-bypass-approvals-and-sandbox") || !strings.Contains(part, " -c ") {
				t.Errorf("%q lacks the bypass flag or a -c override", part)
			}
		}
	}
}

// Codex runs an opening message starting with "!" as a shell command, and reads
// one starting with "-" as a flag unless it follows "--".
func TestCodexPromptNeverStartsWithBang(t *testing.T) {
	if cmd := Codex.LaunchCmd("-fix the flag parser"); !strings.HasSuffix(cmd, " -- '-fix the flag parser'") {
		t.Errorf("LaunchCmd = %q, want the prompt after --", cmd)
	}
	cmd := Codex.LaunchCmd("!rm -rf ~")
	if strings.Contains(cmd, "'!") {
		t.Errorf("LaunchCmd = %q hands Codex a prompt starting with '!'", cmd)
	}
	if Claude.LaunchCmd("!ls") != "claude '!ls'" {
		t.Error("Claude's prompt must pass through untouched")
	}
}

// Swapping back resumes the agent's own conversation (so repeated swaps never
// pile up new ones), with a fresh start only when there is none or it no longer
// resumes. Reviving with no prompt sends none.
func TestResumeCmd(t *testing.T) {
	cases := []struct {
		k          Kind
		id, prompt string
		want       string
	}{
		{Claude, "", "", "claude"},
		{Claude, "", "go", "claude 'go'"},
		{Claude, "abc", "", "claude --resume 'abc' || claude"},
		{Claude, "abc", "go", "claude --resume 'abc' 'go' || claude 'go'"},
		{Codex, "", "", "codex " + codexFlags},
		{Codex, "abc", "go", "codex resume " + codexFlags + " 'abc' -- 'go' || codex " + codexFlags + " -- 'go'"},
	}
	for _, c := range cases {
		if got := c.k.ResumeCmd(c.id, c.prompt); got != c.want {
			t.Errorf("%s.ResumeCmd(%q, %q) = %q, want %q", c.k, c.id, c.prompt, got, c.want)
		}
	}
}

// A run atelier starts on the side must not carry the space's identity, or the
// global hooks would rewrite that space's status from it.
func TestWithoutSpace(t *testing.T) {
	got := withoutSpace([]string{"PATH=/bin", "ATELIER_SESSION=s", "ATELIER_SLUG=s", "ATELIER_AGENT=claude", "ATELIER_ROOT=/r"})
	if strings.Join(got, " ") != "PATH=/bin ATELIER_ROOT=/r" {
		t.Errorf("withoutSpace = %v", got)
	}
}
