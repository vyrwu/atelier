package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
)

// gh-enhance is found as a binary first — a package manager puts it on PATH
// without registering it with gh, where `gh enhance` is an unknown command —
// then as a gh extension, and otherwise not at all. This runs the real snippet
// against a PATH of stubs.
func TestChecksPreludeFindsGhEnhance(t *testing.T) {
	stub := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	choose := func(dir string) string {
		t.Helper()
		// PATH keeps the system dirs for grep; the stub dir comes first.
		t.Setenv("PATH", dir+":/usr/bin:/bin")
		out, err := exec.Command("/bin/sh", "-c", checksPrelude+`printf '%s' "$c"`).CombinedOutput()
		if err != nil {
			t.Fatalf("prelude failed: %v (%s)", err, out)
		}
		return string(out)
	}

	none := t.TempDir()
	stub(none, "gh", "exit 0") // gh with no extensions
	if got := choose(none); got != "" {
		t.Errorf("nothing installed: %q, want no gh-enhance", got)
	}

	ext := t.TempDir()
	stub(ext, "gh", `[ "$1 $2" = "extension list" ] && echo "gh enhance	dlvhdr/gh-enhance	v0.7.2"`)
	if got := choose(ext); got != "gh enhance" {
		t.Errorf("registered extension: %q, want 'gh enhance'", got)
	}

	bin := t.TempDir()
	stub(bin, "gh", "exit 0")
	stub(bin, "gh-enhance", "exit 0")
	if got := choose(bin); got != "gh-enhance" {
		t.Errorf("binary on PATH: %q, want 'gh-enhance'", got)
	}
}

func TestChecksCommand(t *testing.T) {
	pr := core.PR{Repo: "o/r", Number: 7, URL: "https://github.com/o/r/pull/7"}
	cmd := checksCommand(pr)
	if !strings.HasPrefix(cmd, checksPrelude) {
		t.Errorf("does not find gh-enhance itself: %q", cmd)
	}
	if !strings.Contains(cmd, `$c 'https://github.com/o/r/pull/7'`) {
		t.Errorf("gh-enhance not given the PR: %q", cmd)
	}
	// The fallback watches the checks and waits once they finish.
	if !strings.Contains(cmd, "gh pr checks 7 --repo 'o/r' --watch") || !strings.Contains(cmd, "read _") {
		t.Errorf("fallback: %q", cmd)
	}
	// With no URL cached, one is built from the repo and number.
	if cmd := checksCommand(core.PR{Repo: "o/r", Number: 7}); !strings.Contains(cmd, "'https://github.com/o/r/pull/7'") {
		t.Errorf("no URL built: %q", cmd)
	}
}

func TestChecksWindowIsPerPR(t *testing.T) {
	if got := checksWindow(core.PR{Repo: "vyrwu/atelier", Number: 100}); got != "ci-atelier-100" {
		t.Errorf("window = %q, want ci-atelier-100", got)
	}
}

// M-e in the PR view goes to the checks window. With no tmux here it reports
// instead of switching, which still proves the key reaches openChecks.
func TestMEOpensChecks(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
		{Repo: "o/r", Number: 7, Title: "a pr", State: core.PROpen},
	}}
	m := Model{w: 120, screen: scrPRs, curWS: ws, filter: textinput.New(),
		wt: map[string][]core.Worktree{}, prPending: map[string]bool{}}
	next, cmd := m.updatePRs(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}, Alt: true})
	if cmd == nil && next.(Model).err == "" {
		t.Error("M-e did nothing")
	}
	if next.(Model).screen != scrPRs {
		t.Error("M-e navigated inside the overlay instead of opening a window")
	}
}

// M-d no longer converts a PR to draft: the PR view keeps only open and close.
// It must not start a state change, and must not leave the view.
func TestMDNoLongerDraftsInThePRView(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
		{Repo: "o/r", Number: 7, Title: "a pr", State: core.PROpen},
	}}
	m := Model{w: 120, screen: scrPRs, curWS: ws, filter: textinput.New(),
		wt: map[string][]core.Worktree{}, prPending: map[string]bool{}}
	next, _ := m.updatePRs(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}, Alt: true})
	got := next.(Model)
	if len(got.prPending) != 0 {
		t.Errorf("M-d started a state change: %v", got.prPending)
	}
	if got.screen != scrPRs || got.curWS.PRs[0].State != core.PROpen {
		t.Errorf("M-d changed something: screen=%v state=%v", got.screen, got.curWS.PRs[0].State)
	}
	if strings.Contains(ansiRe.ReplaceAllString(got.screenFooter(), ""), "draft") {
		t.Error("the footer still offers draft")
	}
}
