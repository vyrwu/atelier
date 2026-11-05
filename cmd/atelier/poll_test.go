package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// The space you are in polls every few seconds; every other space once a
// minute, so the Spaces counts stay current. Trash never polls.
func TestDueSpaces(t *testing.T) {
	now := time.Now()
	ws := []core.Workspace{
		{Slug: "here-fresh", Session: "here", PRsRefreshed: now.Add(-2 * time.Second)},
		{Slug: "other-fresh", Session: "a", PRsRefreshed: now.Add(-30 * time.Second)},
		{Slug: "other-stale", Session: "b", PRsRefreshed: now.Add(-61 * time.Second)},
		{Slug: "never", Session: "c"},
		{Slug: "retired", Session: "d", Retired: true},
	}
	got := map[string]bool{}
	for _, w := range dueSpaces(ws, "here", now) {
		got[w.Slug] = true
	}
	if got["here-fresh"] || got["other-fresh"] || got["retired"] || !got["other-stale"] || !got["never"] {
		t.Errorf("due = %v, want other-stale and never", got)
	}

	ws[0].PRsRefreshed = now.Add(-5 * time.Second)
	if due := dueSpaces(ws[:1], "here", now); len(due) != 1 {
		t.Error("the focused space is not due after its interval")
	}
	if due := dueSpaces(ws[:1], "elsewhere", now); len(due) != 0 {
		t.Error("an unfocused space polled at the focused rate")
	}
}

// tmux re-runs a status job when it redraws, to the second. A sweep that starts
// a beat early must still count, or a 5s interval quietly becomes 10s.
func TestDueSpacesToleratesTmuxJitter(t *testing.T) {
	now := time.Now()
	ws := []core.Workspace{{Slug: "s", Session: "s", PRsRefreshed: now.Add(-4500 * time.Millisecond)}}
	if len(dueSpaces(ws, "s", now)) != 1 {
		t.Error("a redraw half a second early skipped a whole interval")
	}
}

// The poll rides tmux's status redraws, scoped to the session being drawn, and
// the redraw interval is what bounds the focused space's cadence.
func TestFragmentRunsThePollJob(t *testing.T) {
	frag, _ := bindingsFragment()
	var right, interval bool
	for _, line := range strings.Split(frag, "\n") {
		if strings.HasPrefix(line, "set -g status-right ") {
			right = strings.Contains(line, ` poll '#{session_name}')`)
		}
		if line == "set -g status-interval 5" {
			interval = true
		}
	}
	if !right {
		t.Error("status-right does not run the poll job for the drawn session")
	}
	if !interval {
		t.Error("status-interval is not 5s — the focused space would poll slower")
	}
}
