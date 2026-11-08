package main

import (
	"github.com/vyrwu/atelier/internal/core"
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := core.Update(func(s *core.State) { s.Workspaces = []core.Workspace{{Slug: "s", Session: "s", Title: "initial"}} }); err != nil {
		t.Fatal(err)
	}
	if changed, err := setGeneratedTitle("s", "initial", "generated"); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if err := core.Update(func(s *core.State) { s.Find("s").Title = "human choice" }); err != nil {
		t.Fatal(err)
	}
	if changed, err := setGeneratedTitle("s", "initial", "late result"); err != nil || changed {
		t.Fatal(changed, err)
	}
	if got := core.Load().Find("s").Title; got != "human choice" {
		t.Fatal(got)
	}
}
