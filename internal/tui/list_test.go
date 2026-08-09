package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func newFakeList() *List {
	items := []list.Item{
		SimpleItem{IDStr: "k1", TitleStr: "one", DescStr: "first"},
		SimpleItem{IDStr: "k2", TitleStr: "two", DescStr: "second"},
	}
	m := NewList(SelectorTheme(), " T ", items, Action("jump", "alt+n", "M-n", "jump"))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	return next.(*List)
}

// TestList_EnterSelectsID: Enter reports the highlighted item's ID.
func TestList_EnterSelectsID(t *testing.T) {
	m := newFakeList()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(*List).Outcome().Selection; got != "k1" {
		t.Errorf("Enter selection = %q, want k1", got)
	}
}

// TestList_ActionKeyReportsToken: a bound action key reports its token
// (used for cross-jumps) rather than a selection.
func TestList_ActionKeyReportsToken(t *testing.T) {
	m := newFakeList()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}, Alt: true})
	out := next.(*List).Outcome()
	if out.Key != "jump" {
		t.Errorf("alt+n Key = %q, want jump", out.Key)
	}
	if out.Selection != "" {
		t.Errorf("alt+n should not set Selection, got %q", out.Selection)
	}
	if cmd == nil {
		t.Error("alt+n should return tea.Quit")
	}
}

// TestList_CancelKeys: Esc and Ctrl-C both cancel (→ ErrCancelled in Run).
func TestList_CancelKeys(t *testing.T) {
	for _, k := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
		m := newFakeList()
		next, _ := m.Update(k)
		if !next.(*List).Cancelled() {
			t.Errorf("%s should cancel", k.String())
		}
	}
}
