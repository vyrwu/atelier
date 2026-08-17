package claudegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstLine(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"\n":             "",
		"feat/foo":       "feat/foo",
		"feat/foo\n":     "feat/foo",
		"feat/foo\nbar":  "feat/foo",
		"  feat/foo  \n": "feat/foo",
		"line1\nline2\n": "line1",
	}
	for in, want := range cases {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q): got %q want %q", in, got, want)
		}
	}
}

func TestNew_Defaults(t *testing.T) {
	g := New()
	if g.Model != DefaultModel {
		t.Fatalf("Model: got %q want %q", g.Model, DefaultModel)
	}
	if g.Timeout != DefaultTimeout {
		t.Fatalf("Timeout: got %v want %v", g.Timeout, DefaultTimeout)
	}
}

// TestRunWithSystemPrompt_PassesFlagsAndPromptViaStdin verifies the CLI flags
// that prevent Claude from cold-starting its full global config (MCP servers
// etc.), that JSON output is requested (for token metering), and that the
// prompt is delivered on stdin rather than as a positional argument.
//
// Bug history: without `--setting-sources project,local`, branch-name
// generation took 30+ seconds and the 30s default timeout fired. And since
// `--tools` became variadic (`--tools <tools...>`), a trailing
// `--tools "" <prompt>` swallows the prompt — so the prompt now goes on
// stdin, where no flag can capture it.
func TestRunWithSystemPrompt_PassesFlagsAndPromptViaStdin(t *testing.T) {
	tmp := t.TempDir()
	argFile := filepath.Join(tmp, "args.log")
	stdinFile := filepath.Join(tmp, "stdin.log")

	// Fake `claude` that records its argv + stdin to files, then prints a
	// JSON result envelope and exits.
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(fakeBin, "claude")
	script := `#!/bin/sh
for a in "$@"; do
  printf '%s\n' "$a" >> ` + argFile + `
done
cat > ` + stdinFile + `
printf '{"result":"feat/stub-name","usage":{"input_tokens":1,"output_tokens":2}}\n'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	g := New()
	out, err := g.RunWithSystemPrompt("system prompt", "user prompt")
	if err != nil {
		t.Fatalf("RunWithSystemPrompt: %v", err)
	}
	if !strings.Contains(out, "feat/stub-name") {
		t.Fatalf("unexpected output: %q", out)
	}
	args, _ := os.ReadFile(argFile)
	argsStr := string(args)
	for _, want := range []string{
		"--setting-sources",
		"project,local",
		"--system-prompt",
		"system prompt",
		"-p",
		"--output-format",
		"json",
		"--tools",
	} {
		if !strings.Contains(argsStr, want) {
			t.Errorf("missing arg %q in:\n%s", want, argsStr)
		}
	}
	// The prompt must NOT be a positional arg (a variadic --tools would eat it).
	if strings.Contains(argsStr, "user prompt") {
		t.Errorf("prompt leaked into argv (should be on stdin):\n%s", argsStr)
	}
	stdinContent, _ := os.ReadFile(stdinFile)
	if strings.TrimSpace(string(stdinContent)) != "user prompt" {
		t.Errorf("expected prompt on stdin, got %q", stdinContent)
	}
	if g.LastUsage.InputTokens != 1 || g.LastUsage.OutputTokens != 2 {
		t.Errorf("LastUsage: got %+v want in=1 out=2", g.LastUsage)
	}
}

func TestParseCLIResult(t *testing.T) {
	t.Run("json envelope extracts text + usage", func(t *testing.T) {
		raw := `{"result":"feat/auth","total_cost_usd":0.02,"usage":{"input_tokens":10,"output_tokens":75,"cache_creation_input_tokens":100,"cache_read_input_tokens":200}}`
		text, u := parseCLIResult(raw)
		if text != "feat/auth" {
			t.Errorf("text: got %q", text)
		}
		want := Usage{InputTokens: 10, OutputTokens: 75, CacheCreationTokens: 100, CacheReadTokens: 200, CostUSD: 0.02}
		if u != want {
			t.Errorf("usage: got %+v want %+v", u, want)
		}
	})
	t.Run("non-json falls back to raw text, zero usage", func(t *testing.T) {
		text, u := parseCLIResult("feat/plain-name\n")
		if text != "feat/plain-name\n" {
			t.Errorf("text: got %q", text)
		}
		if (u != Usage{}) {
			t.Errorf("usage: got %+v want zero", u)
		}
	})
}
