package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
)

// The spinner ticks only while something is in flight. A view that keeps
// ticking on idle burns a redraw every 100ms in a popup that is usually just
// sitting there.
func TestSpinnerStopsWhenNothingIsLoading(t *testing.T) {
	m := Model{spin: spinner.New(), wt: map[string][]core.Worktree{}}
	if _, cmd := m.Update(spinner.TickMsg{}); cmd != nil {
		t.Error("idle view kept the spinner ticking")
	}
	m.wtLoading = true
	if _, cmd := m.Update(spinner.TickMsg{}); cmd == nil {
		t.Error("spinner stopped while freshness was in flight")
	}
}

// Worktree rows are drawn from what the filesystem already gave us; nothing
// shells out on the path that builds the model.
func TestWorktreesListedWithoutFreshness(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s"}
	m := Model{curWS: ws, wt: map[string][]core.Worktree{"s": {
		{Repo: "o/r", Branch: "old", Path: "/tmp/a", Created: time.Now().Add(-time.Hour)},
		{Repo: "o/r", Branch: "new", Path: "/tmp/b", Created: time.Now()},
	}}}
	m.listWorktrees()

	if len(m.wtEntries) != 2 {
		t.Fatalf("entries = %d, want 2", len(m.wtEntries))
	}
	if m.wtEntries[0].wt.Branch != "new" {
		t.Errorf("order = %q first, want newest", m.wtEntries[0].wt.Branch)
	}
	for _, e := range m.wtEntries {
		if e.okFresh {
			t.Errorf("%q claims freshness before it was computed", e.wt.Branch)
		}
	}
}

// A sweep that lands after the overlay moved to another space is dropped rather
// than painted onto the wrong rows.
func TestFreshnessAppliesToTheRightSpace(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s"}
	m := Model{curWS: ws, wt: map[string][]core.Worktree{"s": {{Repo: "o/r", Branch: "b", Path: "/tmp/a"}}}}
	m.listWorktrees()

	m.applyFreshness(wtFreshMsg{slug: "other", fresh: map[string]wtFresh{"/tmp/a": {ahead: 9, ok: true}}})
	if m.wtEntries[0].okFresh {
		t.Error("applied another space's sweep")
	}
	m.applyFreshness(wtFreshMsg{slug: "s", fresh: map[string]wtFresh{"/tmp/a": {ahead: 3, behind: 1, ok: true}}})
	e := m.wtEntries[0]
	if !e.okFresh || e.ahead != 3 || e.behind != 1 {
		t.Errorf("entry = %+v, want 3 ahead / 1 behind", e)
	}
}

// M-h from inside an overlay: the popup owns the keyboard while it is open, so
// the tmux binding never sees the key — the overlay has to make the move
// itself, or M-h is simply dead wherever you actually are.
func TestOverlayHandlesGoHome(t *testing.T) {
	m := Model{screen: scrActive, filter: textinput.New(), wt: map[string][]core.Worktree{}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}, Alt: true})
	// With no tmux to switch (as here), it surfaces why; with one, it quits the
	// overlay. Silence on both counts is the bug: an unhandled key.
	if cmd == nil && next.(Model).err == "" {
		t.Fatal("M-h did nothing and said nothing")
	}
	// Either way it leaves the overlay rather than navigating inside it.
	if s := next.(Model).screen; s != scrActive {
		t.Errorf("screen changed to %v; M-h should close the overlay, not navigate it", s)
	}
}

// The worktree spinner rises only when a sweep will run. With no worktrees no
// result ever arrives to lower it, and "checking branches…" spun forever.
func TestWorktreeSpinnerOnlyWhenASweepRuns(t *testing.T) {
	empty := Model{screen: scrActive, curWS: &core.Workspace{Slug: "s", Session: "s"},
		filter: textinput.New(), wt: map[string][]core.Worktree{}}
	next, _ := empty.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	if next.(Model).wtLoading {
		t.Error("spinner raised for a space with no worktrees — nothing will lower it")
	}

	withOne := empty
	withOne.wt = map[string][]core.Worktree{"s": {{Repo: "r", Branch: "b", Path: t.TempDir()}}}
	withOne.listWorktrees()
	next, cmd := withOne.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	if !next.(Model).wtLoading || cmd == nil {
		t.Error("a space with worktrees should show the sweep in flight")
	}
}

// A PR the agent registers while a refresh is in flight is not in that
// refresh's result. Landing it must not erase the registration from disk.
// The overlay never queries GitHub: the status job keeps the cache fresh, and
// the overlay re-reads it every second. A PR with a state change in flight
// keeps its optimistic state until that change lands.
func TestCacheTickAppliesTheCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := core.Update(func(s *core.State) {
		s.Workspaces = append(s.Workspaces, core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
			{Repo: "o/r", Number: 1, State: core.PROpen, CI: core.CIPass},
			{Repo: "o/r", Number: 2, State: core.PROpen},
			{Repo: "o/r", Number: 3, State: core.PROpen},
		}})
	}); err != nil {
		t.Fatal(err)
	}
	m := Model{all: []core.Workspace{{Slug: "s", Session: "s", PRs: []core.PR{
		{Repo: "o/r", Number: 1, State: core.PROpen, CI: core.CIPending},
		{Repo: "o/r", Number: 2, State: core.PRClosed}, // M-c in flight
	}}}, prPending: map[string]bool{prKey("o/r", 2): true}, filter: textinput.New(), wt: map[string][]core.Worktree{}}
	m.curWS = &m.all[0]

	next, cmd := m.Update(cacheTickMsg{})
	if cmd == nil {
		t.Error("the tick did not re-arm")
	}
	got := map[int]core.PR{}
	for _, pr := range next.(Model).curWS.PRs {
		got[pr.Number] = pr
	}
	if got[1].CI != core.CIPass {
		t.Errorf("#1 not refreshed from the cache: %+v", got[1])
	}
	if got[2].State != core.PRClosed {
		t.Errorf("#2 lost its in-flight state: %+v", got[2])
	}
	if _, ok := got[3]; !ok {
		t.Error("#3, new in the cache, not shown")
	}
}

// The cache can reorder the list under the cursor — a new PR, a state change.
// The selection follows the PR, not the row, so M-c never lands on a neighbour.
func TestCacheTickKeepsTheSelection(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	old := time.Now().Add(-time.Hour)
	if err := core.Update(func(s *core.State) {
		s.Workspaces = append(s.Workspaces, core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
			{Repo: "o/r", Number: 7, State: core.PROpen, CreatedAt: old},
			{Repo: "o/r", Number: 9, State: core.PROpen, CreatedAt: time.Now()}, // new, sorts first
		}})
	}); err != nil {
		t.Fatal(err)
	}
	m := Model{screen: scrPRs, all: []core.Workspace{{Slug: "s", Session: "s", PRs: []core.PR{
		{Repo: "o/r", Number: 7, State: core.PROpen, CreatedAt: old},
	}}}, prPending: map[string]bool{}, filter: textinput.New(), wt: map[string][]core.Worktree{}}
	m.curWS = &m.all[0]

	next, _ := m.Update(cacheTickMsg{})
	got := next.(Model)
	if flat := got.flatPRs(); got.prSel >= len(flat) || flat[got.prSel].Number != 7 {
		t.Errorf("selection moved off #7: prSel=%d over %+v", got.prSel, flat)
	}
}

// A state change's result is saved to the cache, so the next tick shows it.
func TestSavePRWritesTheCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := core.Update(func(s *core.State) {
		s.Workspaces = append(s.Workspaces, core.Workspace{Slug: "s", PRs: []core.PR{{Repo: "o/r", Number: 1, State: core.PROpen}}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := savePR("s", core.PR{Repo: "o/r", Number: 1, State: core.PRClosed}); err != nil {
		t.Fatal(err)
	}
	if got := core.Load().Find("s").PRs[0].State; got != core.PRClosed {
		t.Errorf("state on disk = %q, want closed", got)
	}
}
