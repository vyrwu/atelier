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

// TestRunWithSystemPrompt_PassesSettingSourcesAndStdinNil verifies the
// CLI flags that prevent Claude from cold-starting its full global
// config (MCP servers etc.) and from blocking on inherited stdin.
//
// Bug history: without `--setting-sources project,local`, branch-name
// generation took 30+ seconds and the 30s default timeout fired. Without
// stdin redirected, claude could block reading from the popup's pty.
func TestRunWithSystemPrompt_PassesSettingSourcesAndStdinNil(t *testing.T) {
	tmp := t.TempDir()
	argFile := filepath.Join(tmp, "args.log")
	stdinFile := filepath.Join(tmp, "stdin.log")

	// Fake `claude` that records its argv + stdin to files, then prints
	// a valid branch name and exits.
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(fakeBin, "claude")
	script := `#!/bin/sh
# Record argv (one arg per line) + whether stdin is a tty.
for a in "$@"; do
  printf '%s\n' "$a" >> ` + argFile + `
done
# Read up to 1 byte of stdin with a 0.1s timeout — if we read anything,
# stdin was inherited (not /dev/null). If read fails immediately, stdin
# is closed/empty.
if read -r line; then
  printf 'STDIN: %s\n' "$line" > ` + stdinFile + `
else
  printf 'STDIN: empty\n' > ` + stdinFile + `
fi
printf 'feat/stub-name\n'
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
		"user prompt",
		"-p",
		"--output-format",
		"text",
	} {
		if !strings.Contains(argsStr, want) {
			t.Errorf("missing arg %q in:\n%s", want, argsStr)
		}
	}
	stdinContent, _ := os.ReadFile(stdinFile)
	if !strings.Contains(string(stdinContent), "empty") {
		t.Errorf("expected stdin empty (cmd.Stdin = nil), got %q", stdinContent)
	}
}
