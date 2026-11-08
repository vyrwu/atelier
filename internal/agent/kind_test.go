package agent

import (
	"strings"
	"testing"
)

// State and config written before there was a choice of agent say nothing, and
// mean Claude; so does anything unrecognised.
func TestOfDefaultsToClaude(t *testing.T) {
	for name, want := range map[string]Kind{"": Claude, "claude": Claude, "codex": Codex, "gpt": Claude, "grok": Grok, "cursor": Claude} {
		if got := Of(name); got != want {
			t.Errorf("Of(%q) = %q, want %q", name, got, want)
		}
	}
	if Valid("cursor") {
		t.Fatal("Cursor must not be an available provider")
	}
	k := Claude
	for _, want := range []Kind{Codex, Grok, Claude} {
		k = k.Next()
		if k != want {
			t.Fatalf("next = %s, want %s", k, want)
		}
	}
}

// The prompt is user-written prose that tmux runs through a shell: quotes,
// backticks, and semicolons in it must stay data, for either agent.
func TestLaunchCmdQuotesPrompt(t *testing.T) {
	for _, k := range Kinds() {
		for _, p := range []string{"it's broken", "rm -rf /; echo pwned", "use `git log` and $HOME", `say "hi"`} {
			cmd := k.LaunchCmd(p)
			prefix := k.Bin() + " "
			if k == Grok {
				prefix = grokCommand + " "
			}
			if !strings.HasPrefix(cmd, prefix) || !strings.HasSuffix(cmd, "'") {
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
		{Grok, "", "", grokCommand},
		{Grok, "abc", "go", grokCommand + " --resume 'abc' -- 'go' || " + grokCommand + " -- 'go'"},
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

func TestRuntimePathsPinInstance(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/test/cache")
	t.Setenv("XDG_STATE_HOME", "/test/state")
	t.Setenv("XDG_CONFIG_HOME", "/test/config")
	t.Setenv("ATELIER_ROOT", "/test/spaces")
	t.Setenv("ATELIER_SOCKET", "test-socket")
	env := Env("test-space", Grok)
	for key, want := range map[string]string{
		"ATELIER_BIN": selfPath(), "ATELIER_WORKSPACE": "/test/spaces/test-space",
		"XDG_CACHE_HOME": "/test/cache", "XDG_STATE_HOME": "/test/state",
		"XDG_CONFIG_HOME": "/test/config", "ATELIER_ROOT": "/test/spaces",
		"ATELIER_SOCKET": "test-socket", "ATELIER_AGENT": "grok",
	} {
		if env[key] != want {
			t.Errorf("%s = %q, want %q", key, env[key], want)
		}
	}
}
