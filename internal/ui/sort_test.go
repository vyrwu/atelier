package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/vyrwu/atelier/internal/core"
)

func spacesModel(ws []core.Workspace, status map[string]core.AgentStatus) *Model {
	m := &Model{
		screen: scrActive,
		filter: textinput.New(),
		wt:     map[string][]core.Worktree{},
		status: status,
		all:    ws,
	}
	return m
}

func order(m *Model) []string {
	out := make([]string, len(m.rows))
	for i, r := range m.rows {
		out[i] = r.ws.Slug
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EXPERIMENT: Spaces orders by attention, not by creation time — what wants you
// first, what has finished next, what is busy after that, and dead sessions
// last. Every other view keeps its own order.
func TestSpacesSortsByAttention(t *testing.T) {
	base := time.Now()
	ws := []core.Workspace{
		{Slug: "working", Session: "working", Created: base},
		{Slug: "gone", Session: "gone", Created: base.Add(-time.Minute)},
		{Slug: "idle", Session: "idle", Created: base.Add(-2 * time.Minute)},
		{Slug: "blocked", Session: "blocked", Created: base.Add(-3 * time.Minute)},
	}
	m := spacesModel(ws, map[string]core.AgentStatus{
		"working": core.StatusWorking,
		"gone":    core.StatusGone,
		"idle":    core.StatusIdle,
		"blocked": core.StatusBlocked,
	})
	m.rebuild()

	want := []string{"blocked", "idle", "working", "gone"}
	if got := order(m); !eq(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Within one rank, the old rule still holds: newest first.
func TestSpacesSortsNewestFirstWithinARank(t *testing.T) {
	base := time.Now()
	ws := []core.Workspace{
		{Slug: "old-blocked", Session: "old-blocked", Created: base.Add(-time.Hour)},
		{Slug: "new-blocked", Session: "new-blocked", Created: base},
		{Slug: "new-idle", Session: "new-idle", Created: base},
		{Slug: "old-idle", Session: "old-idle", Created: base.Add(-time.Hour)},
	}
	m := spacesModel(ws, map[string]core.AgentStatus{
		"old-blocked": core.StatusBlocked,
		"new-blocked": core.StatusBlocked,
		"new-idle":    core.StatusIdle,
		"old-idle":    core.StatusIdle,
	})
	m.rebuild()

	want := []string{"new-blocked", "old-blocked", "new-idle", "old-idle"}
	if got := order(m); !eq(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// The exception is Spaces only — Trash still orders by when a space was closed,
// whatever its last status was.
func TestTrashKeepsCloseTimeOrder(t *testing.T) {
	base := time.Now()
	ws := []core.Workspace{
		{Slug: "closed-first", Session: "a", Retired: true, RetiredAt: base.Add(-time.Hour)},
		{Slug: "closed-last", Session: "b", Retired: true, RetiredAt: base},
	}
	m := spacesModel(ws, map[string]core.AgentStatus{"b": core.StatusGone, "a": core.StatusBlocked})
	m.screen = scrTrash
	m.rebuild()

	want := []string{"closed-last", "closed-first"}
	if got := order(m); !eq(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// A space with no status yet (never launched, or launched before the hooks ran)
// must still sort somewhere sensible rather than falling out of the list.
func TestSpacesUnknownStatusSortsLast(t *testing.T) {
	base := time.Now()
	ws := []core.Workspace{
		{Slug: "unknown", Session: "unknown", Created: base},
		{Slug: "idle", Session: "idle", Created: base.Add(-time.Hour)},
	}
	m := spacesModel(ws, map[string]core.AgentStatus{"idle": core.StatusIdle})
	m.rebuild()

	if got := order(m); !eq(got, []string{"idle", "unknown"}) {
		t.Errorf("order = %v, want the known-idle space above the unknown one", got)
	}
}
