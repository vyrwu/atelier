package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
)

// Pinned spaces come first whatever wants attention elsewhere; inside each
// section the usual order holds.
func TestPinnedSpacesComeFirst(t *testing.T) {
	base := time.Now()
	ws := []core.Workspace{
		{Slug: "blocked", Session: "blocked", Created: base},
		{Slug: "pinned-old", Session: "pinned-old", Created: base.Add(-time.Hour), Pinned: true},
		{Slug: "pinned-blocked", Session: "pinned-blocked", Created: base.Add(-2 * time.Hour), Pinned: true},
		{Slug: "idle", Session: "idle", Created: base},
	}
	m := spacesModel(ws, map[string]core.AgentStatus{
		"blocked": core.StatusBlocked, "pinned-blocked": core.StatusBlocked,
		"pinned-old": core.StatusIdle, "idle": core.StatusIdle,
	})
	m.rebuild()
	want := []string{"pinned-blocked", "pinned-old", "blocked", "idle"}
	if got := order(m); !eq(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Each section gets a header, and the selection is found among the lines
// that the headers push down. With nothing pinned there are no headers.
func TestSpaceLinesSections(t *testing.T) {
	ws := []core.Workspace{
		{Slug: "a", Session: "a", Title: "Alpha", Pinned: true},
		{Slug: "b", Session: "b", Title: "Beta"},
		{Slug: "c", Session: "c", Title: "Gamma"},
	}
	m := spacesModel(ws, map[string]core.AgentStatus{})
	m.w, m.h = 100, 30
	m.rebuild()
	m.sel = 1 // Beta
	lines, sel := m.spaceLines()
	var plain []string
	for _, l := range lines {
		plain = append(plain, strings.TrimSpace(ansiRe.ReplaceAllString(l, "")))
	}
	if len(plain) != 5 || plain[0] != "pinned" || plain[2] != "others" {
		t.Fatalf("lines = %q; want pinned, Alpha, others, Beta, Gamma", plain)
	}
	if !strings.Contains(plain[sel], "Beta") {
		t.Errorf("selection on line %d (%q); want Beta's", sel, plain[sel])
	}

	m.all[0].Pinned = false
	m.rebuild()
	if lines, _ := m.spaceLines(); len(lines) != 3 {
		t.Errorf("nothing pinned, yet %d lines for 3 rows", len(lines))
	}
	// A filter that leaves only unpinned rows leaves no headers either.
	m.all[0].Pinned = true
	m.filter.SetValue("Beta")
	m.rebuild()
	if lines, _ := m.spaceLines(); len(lines) != 1 {
		t.Errorf("filtered to one unpinned row, yet %d lines", len(lines))
	}
}

// M-f on a row pins it — in the state file, not only on screen — and the
// cursor follows it into its new place; M-f again unpins it.
func TestAltFTogglesPin(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base := time.Now()
	ws := []core.Workspace{
		{Slug: "newest", Session: "newest", Created: base},
		{Slug: "older", Session: "older", Created: base.Add(-time.Hour)},
	}
	if err := core.Update(func(s *core.State) { s.Workspaces = append(s.Workspaces, ws...) }); err != nil {
		t.Fatal(err)
	}
	m := spacesModel(core.Load().Workspaces, map[string]core.AgentStatus{})
	m.rebuild()
	m.sel = 1 // older
	alt := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true}

	next, _ := m.Update(alt)
	got := next.(Model)
	if o := order(&got); !eq(o, []string{"older", "newest"}) || got.sel != 0 {
		t.Fatalf("after M-f: order %v, sel %d; want older pinned on top and selected", o, got.sel)
	}
	if w := core.Load().Find("older"); w == nil || !w.Pinned {
		t.Fatal("pin not saved")
	}

	next, _ = got.Update(alt)
	got = next.(Model)
	if o := order(&got); !eq(o, []string{"newest", "older"}) || got.sel != 1 {
		t.Errorf("after unpinning: order %v, sel %d", o, got.sel)
	}
	if w := core.Load().Find("older"); w == nil || w.Pinned {
		t.Error("unpin not saved")
	}

}

// Pinning a space deleted since the overlay opened is an error, not a pin
// saved for nothing.
func TestSetPinnedOnAMissingSpace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := setPinned("gone", true); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("err = %v", err)
	}
}
