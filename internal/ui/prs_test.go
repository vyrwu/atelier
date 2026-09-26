package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
)

// EXPERIMENT: no cache window. A sweep that landed a moment ago must not stop
// the next open from querying — the PR view is meant to be relied on while you
// work, so what it shows is what GitHub just said.
func TestRefreshIsNeverCacheGated(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s", PRsRefreshed: time.Now()}
	m := Model{curWS: ws, wt: map[string][]core.Worktree{}}

	if m.refreshCurrentCmd() == nil {
		t.Error("a refresh seconds after the last one was skipped — the cache window is back")
	}
	ws.PRsRefreshed = time.Now().Add(-time.Hour)
	if m.refreshCurrentCmd() == nil {
		t.Error("no refresh issued at all")
	}
}

// Off a space there is nothing to query.
func TestRefreshNeedsASpace(t *testing.T) {
	m := Model{wt: map[string][]core.Worktree{}}
	if m.refreshCurrentCmd() != nil {
		t.Error("issued a query with no current space")
	}
}

// While the query is out, the rows are the previous sweep. The header says so,
// so a second-old cache can't pass for ground truth.
func TestPRViewMarksAnInFlightQuery(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s", Title: "a space", PRs: []core.PR{
		{Repo: "o/r", Number: 1, Title: "a pr", State: core.PROpen, CI: core.CIPending},
	}}
	m := Model{w: 120, h: 40, screen: scrPRs, curWS: ws, filter: textinput.New(), wt: map[string][]core.Worktree{}}

	m.prLoading = true
	if out := ansiRe.ReplaceAllString(m.viewPRs(), ""); !strings.Contains(out, "checking github") {
		t.Errorf("in-flight query not surfaced:\n%s", out)
	}
	m.prLoading = false
	if out := ansiRe.ReplaceAllString(m.viewPRs(), ""); strings.Contains(out, "checking github") {
		t.Errorf("marker outlived the query:\n%s", out)
	}
}

// The marker clears when the sweep lands, whatever else the message carries.
func TestPRsRefreshedClearsTheMarker(t *testing.T) {
	ws := core.Workspace{Slug: "s", Session: "s"}
	m := Model{all: []core.Workspace{ws}, prLoading: true, wt: map[string][]core.Worktree{}, filter: textinput.New()}
	m.curWS = &m.all[0]

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	next, _ := m.Update(prsRefreshedMsg{slug: "s", prs: []core.PR{{Repo: "o/r", Number: 2}}})
	if next.(Model).prLoading {
		t.Error("marker still set after the sweep landed")
	}
	if got := next.(Model).all[0].PRs; len(got) != 1 || got[0].Number != 2 {
		t.Errorf("sweep not applied: %+v", got)
	}
}

// The marker and the query come off one predicate. They were two conditions
// once, evaluated at different points in startup, and the header stayed silent
// through every query it was supposed to announce.
func TestQueryAndMarkerAgree(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s"}
	cases := []struct {
		name  string
		model Model
		want  bool
	}{
		{"pr view in a space", Model{screen: scrPRs, curWS: ws}, true},
		{"pr view off a space", Model{screen: scrPRs}, false},
		{"spaces view", Model{screen: scrActive, curWS: ws}, false},
	}
	for _, c := range cases {
		c.model.wt = map[string][]core.Worktree{}
		if got := c.model.queriesOnOpen(); got != c.want {
			t.Errorf("%s: queriesOnOpen = %v, want %v", c.name, got, c.want)
		}
		// Whenever it says yes, there is a command to run.
		if c.want && c.model.refreshCurrentCmd() == nil {
			t.Errorf("%s: claims a query but issues none", c.name)
		}
	}
}

// The spinner ticks only while something is in flight. A view that keeps
// ticking on idle burns a redraw every 100ms in a popup that is usually just
// sitting there.
func TestSpinnerStopsWhenNothingIsLoading(t *testing.T) {
	m := Model{spin: spinner.New(), wt: map[string][]core.Worktree{}}
	if _, cmd := m.Update(spinner.TickMsg{}); cmd != nil {
		t.Error("idle view kept the spinner ticking")
	}
	m.prLoading = true
	if _, cmd := m.Update(spinner.TickMsg{}); cmd == nil {
		t.Error("spinner stopped while a PR query was in flight")
	}
	m.prLoading, m.wtLoading = false, true
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

// An open PR view re-queries on its own every minute. The tick carries the
// generation of the chain that scheduled it, so re-entering the view can't
// leave two chains ticking — and a tick from a retired chain dies.
func TestPRPollRearmsOnlyTheLiveChain(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s"}
	m := Model{screen: scrPRs, curWS: ws, filter: textinput.New(), wt: map[string][]core.Worktree{}, prPollGen: 3}

	next, cmd := m.Update(prPollMsg{gen: 3})
	if cmd == nil {
		t.Error("live tick did not re-arm the poll")
	}
	if !next.(Model).prLoading {
		t.Error("live tick did not mark the query in flight")
	}
	if _, cmd := m.Update(prPollMsg{gen: 2}); cmd != nil {
		t.Error("a retired chain kept ticking")
	}
}

// Leaving the PR view ends the poll: the popup is the process, and a tick that
// lands on another screen must not re-arm.
func TestPRPollStopsOffTheView(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s"}
	m := Model{screen: scrActive, curWS: ws, filter: textinput.New(), wt: map[string][]core.Worktree{}, prPollGen: 1}
	if _, cmd := m.Update(prPollMsg{gen: 1}); cmd != nil {
		t.Error("poll kept ticking after leaving the PR view")
	}

	m.screen, m.curWS = scrPRs, nil // on the view, but off a space
	if _, cmd := m.Update(prPollMsg{gen: 1}); cmd != nil {
		t.Error("poll ticked with no space to query")
	}
}

// Re-pressing M-p restarts the chain, and the model it returns carries the
// generation that chain's tick will quote — evaluation order in the return
// statement is not something to rely on.
func TestMPRestartsThePollChain(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s"}
	m := Model{screen: scrActive, curWS: ws, filter: textinput.New(), wt: map[string][]core.Worktree{}, prPollGen: 1}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}, Alt: true})
	got := next.(Model)
	if got.prPollGen != 2 {
		t.Fatalf("generation = %d, want the chain advanced to 2", got.prPollGen)
	}
	// The tick that chain scheduled must be accepted by the returned model.
	if _, cmd := got.Update(prPollMsg{gen: got.prPollGen}); cmd == nil {
		t.Error("the restarted chain's own tick was treated as stale")
	}
}

// The poll interval is a minute. It has been temporarily shortened to watch the
// chain fire during development, and a binary built in that state polls GitHub
// every couple of seconds from every open PR view — pin it.
func TestPRPollIntervalIsAMinute(t *testing.T) {
	if prPollInterval != time.Minute {
		t.Fatalf("prPollInterval = %v, want 1m — a shortened interval must not ship", prPollInterval)
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
func TestRefreshKeepsARegistrationMadeMeanwhile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := core.Update(func(s *core.State) {
		s.Workspaces = append(s.Workspaces, core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
			{Repo: "o/r", Number: 1},
			{Repo: "o/r", Number: 5, Registered: true}, // registered after the query began
		}})
	}); err != nil {
		t.Fatal(err)
	}
	m := Model{all: core.Load().Workspaces, filter: textinput.New(), wt: map[string][]core.Worktree{}}
	m.curWS = &m.all[0]

	next, _ := m.Update(prsRefreshedMsg{slug: "s", prs: []core.PR{{Repo: "o/r", Number: 1, Title: "fresh"}}})

	onDisk := map[int]bool{}
	for _, pr := range core.Load().Find("s").PRs {
		onDisk[pr.Number] = true
	}
	if !onDisk[5] {
		t.Error("the registration made during the refresh was erased from disk")
	}
	inView := map[int]bool{}
	for _, pr := range next.(Model).all[0].PRs {
		inView[pr.Number] = true
	}
	if !inView[1] || !inView[5] {
		t.Errorf("view has %v, want both PRs", inView)
	}
}
