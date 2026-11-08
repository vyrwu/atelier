package main

import (
	"github.com/vyrwu/atelier/internal/core"
	"slices"
	"strings"
	"testing"
)

func TestDestroyGate(t *testing.T) {
	for _, tc := range []struct {
		maitre, yes, tty bool
		input            string
		bad              bool
	}{{true, true, false, "", false}, {false, true, false, "", false}, {false, false, false, "", true}, {false, false, true, "y\n", false}, {false, false, true, "n\n", true}} {
		called := false
		gate := destroyGate(tc.maitre, tc.yes, tc.tty, strings.NewReader(tc.input), func(string) error { called = true; return nil })
		err := gate("Delete?")
		if (err != nil) != tc.bad || called != tc.maitre {
			t.Errorf("%+v: %v called=%v", tc, err, called)
		}
	}
}

func TestOperatorCommandsRejectBadArguments(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"read", nil}, {"send", nil}, {"delete", nil}, {"rename", []string{"x"}},
		{"restore", []string{"x", "extra"}}, {"read", []string{"x", "--lines", "-1"}},
		{"send", []string{"x", "--key", "F1"}}, {"wait", []string{"--timeout", "-1s"}},
		{"spaces", []string{"--typo"}}, {"retire", []string{"maitre"}},
		{"read", []string{"maitre"}}, {"delete", []string{"maitre", "--yes"}},
	} {
		if err := run(tc.command, tc.args); err == nil {
			t.Errorf("%s %v accepted", tc.command, tc.args)
		}
	}
}

func TestGeneratedTitlePreservesRename(t *testing.T) {
	s := &core.State{Workspaces: []core.Workspace{{Slug: "s", Session: "s", Title: "initial"}}}
	if !setGeneratedTitle(s, "s", "initial", "generated") {
		t.Fatal("initial title was not replaced")
	}
	s.Find("s").Title = "human choice"
	if setGeneratedTitle(s, "s", "initial", "late result") {
		t.Fatal("overwrote a renamed title")
	}
	if got := s.Find("s").Title; got != "human choice" {
		t.Fatal(got)
	}
}

func TestSplitSendText(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		text  string
		flags []string
		bad   bool
	}{
		{[]string{"hello", "--no-enter"}, "hello", []string{"--no-enter"}, false},
		{[]string{"-h"}, "-h", []string{}, false},
		{[]string{"--", "--version"}, "--version", []string{}, false},
		{[]string{"--no-enter", "--", "--key Enter"}, "--key Enter", []string{"--no-enter"}, false},
		{[]string{"--key", "Down"}, "", []string{"--key", "Down"}, false},
		{[]string{"hello", "--", "second"}, "", nil, true},
		{[]string{"--"}, "", nil, true},
	} {
		text, flags, err := splitSendText(tc.args)
		if (err != nil) != tc.bad {
			t.Errorf("%q: %v", tc.args, err)
			continue
		}
		if !tc.bad && (text != tc.text || !slices.Equal(flags, tc.flags)) {
			t.Errorf("%q: text=%q flags=%q", tc.args, text, flags)
		}
	}
}

func TestCLIRefusesToStopItsOwnSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMUX_PANE", "")
	if err := core.Update(func(s *core.State) {
		s.Workspaces = []core.Workspace{{Slug: "own", Session: "own-session"}}
	}); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []struct{ slug, session string }{{"own", ""}, {"", "own-session"}} {
		t.Setenv("ATELIER_SLUG", identity.slug)
		t.Setenv("ATELIER_SESSION", identity.session)
		for _, verb := range []string{"retire", "delete"} {
			args := []string{"own"}
			if verb == "delete" {
				args = append(args, "--yes")
			}
			if err := run(verb, args); err == nil || !strings.Contains(err.Error(), "enclosing space") {
				t.Fatalf("%s accepted self-stop with %+v: %v", verb, identity, err)
			}
		}
	}
	if w := core.Load().Find("own"); w == nil || w.Retired {
		t.Fatalf("refused operation changed state: %+v", w)
	}
}
