package fzfstyle

import (
	"strings"
	"testing"
)

func TestArgs_BaseStructure(t *testing.T) {
	args := Args("⌘ ", "Select Tool", "172")
	flat := strings.Join(args, " ")
	for _, want := range []string{
		"--prompt=⌘ ",
		"--color=prompt:172:bold,pointer:172,query:172,label:103,border:103,footer:103,hl:-1,hl+:-1",
		"--info=inline-right",
		"--height=100%",
		"--reverse",
		"--ansi",
		"--border=rounded",
		"--border-label= Select Tool ",
		"--border-label-pos=0",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing arg %q in:\n%s", want, flat)
		}
	}
}

// TestArgs_GlobalQuitBindPresent locks in the M-q-quits-from-inside-fzf
// contract. Without this fzf bind, tmux's popup-table M-q binding
// never reaches the running fzf process (display-popup -E hands raw
// stdin to the spawned command), so M-q is silently dropped in every
// picker — exactly the bug the user hit. FR-5.3: detach via
// `atelier server quit` rather than kill-server, so background popup
// agents survive across user sessions.
func TestArgs_GlobalQuitBindPresent(t *testing.T) {
	args := Args("⌘ ", "Any", "172")
	flat := strings.Join(args, "\n")
	if !strings.Contains(flat, "--bind=alt-q:execute-silent(atelier server quit)") {
		t.Errorf("missing global M-q bind in:\n%s", flat)
	}
}

func TestArgs_NamedColor(t *testing.T) {
	args := Args("栽 ", "Select Workspace", "red")
	flat := strings.Join(args, " ")
	if !strings.Contains(flat, "prompt:red:bold,pointer:red,query:red") {
		t.Errorf("named color not propagated:\n%s", flat)
	}
}

func TestArgs_WithOptions(t *testing.T) {
	args := Args("胡 ", "Contexts", "blue",
		WithFooter("ctrl-x · delete"),
		WithHeader("pick a context"),
		WithBind("ctrl-x", "transform:do-thing"),
		WithDelimiter("\t"),
		WithNth("2"),
		WithExpect("ctrl-a", "ctrl-b"),
		WithPrintQuery(),
		WithNoClear(),
		WithQuery("initial"),
	)
	flat := strings.Join(args, "\n")
	for _, want := range []string{
		"--footer=ctrl-x · delete",
		"--header=pick a context",
		"--bind=ctrl-x:transform:do-thing",
		"--delimiter=\t",
		"--with-nth=2",
		"--expect=ctrl-a,ctrl-b",
		"--print-query",
		"--no-clear",
		"--query=initial",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("option not propagated %q:\n%s", want, flat)
		}
	}
}

func TestWithCustomColor_ReplacesDefault(t *testing.T) {
	args := Args("栽 ", "Picker", "red",
		WithCustomColor("prompt:green,label:103,hl:red,hl+:red:bold"),
	)
	colorArgs := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "--color=") {
			colorArgs = append(colorArgs, a)
		}
	}
	if len(colorArgs) != 1 {
		t.Fatalf("expected exactly one --color= arg, got %d: %v", len(colorArgs), colorArgs)
	}
	if !strings.Contains(colorArgs[0], "hl:red") {
		t.Fatalf("expected custom color to replace base, got %q", colorArgs[0])
	}
}

func TestColoredText_Numeric(t *testing.T) {
	got := ColoredText("173", "Claude Code")
	want := "\033[38;5;173mClaude Code\033[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestColoredText_Named(t *testing.T) {
	got := ColoredText("red", "Delete")
	want := "\033[31mDelete\033[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestColoredText_Empty(t *testing.T) {
	if got := ColoredText("", "plain"); got != "plain" {
		t.Fatalf("expected unwrapped for empty color, got %q", got)
	}
}

func TestIcon256(t *testing.T) {
	got := Icon256(173, "知")
	want := "\033[38;5;173m知\033[0m"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
