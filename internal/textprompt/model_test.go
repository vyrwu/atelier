package textprompt

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// feed drives the model through a sequence of messages, mirroring the
// bubbletea runtime, so the pure Update logic is testable without a tty.
func feed(m model, msgs ...tea.Msg) model {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func TestModel_TypeThenEnterSubmits(t *testing.T) {
	m := feed(newModel(Options{Title: "What are we doing today?"}),
		tea.WindowSizeMsg{Width: 40, Height: 10},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix the flaky test")},
		tea.KeyMsg{Type: tea.KeyEnter},
	)
	if !m.submitted {
		t.Fatal("Enter should submit")
	}
	if m.cancelled {
		t.Fatal("submit must not also cancel")
	}
	if got := strings.TrimSpace(m.ta.Value()); got != "fix the flaky test" {
		t.Fatalf("value = %q, want %q", got, "fix the flaky test")
	}
}

func TestModel_EscCancels(t *testing.T) {
	m := feed(newModel(Options{}),
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")},
		tea.KeyMsg{Type: tea.KeyEsc},
	)
	if !m.cancelled {
		t.Fatal("Esc should cancel")
	}
	if m.submitted {
		t.Fatal("cancel must not also submit")
	}
}

func TestModel_CtrlCCancels(t *testing.T) {
	m := feed(newModel(Options{}), tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.cancelled {
		t.Fatal("Ctrl-C should cancel")
	}
}

// Plain Enter is submit-only: it must never insert a literal newline (Enter is
// intercepted before the textarea, and it's not in the InsertNewline binding).
func TestModel_EnterNeverInsertsNewline(t *testing.T) {
	m := feed(newModel(Options{}),
		tea.WindowSizeMsg{Width: 40, Height: 10},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one")},
		tea.KeyMsg{Type: tea.KeyEnter},
	)
	if strings.Contains(m.ta.Value(), "\n") {
		t.Fatalf("value should carry no newline: %q", m.ta.Value())
	}
	if !m.submitted {
		t.Fatal("Enter should submit")
	}
}

// Alt+Enter inserts a newline and does NOT submit — the multi-line gesture.
func TestModel_AltEnterInsertsNewline(t *testing.T) {
	m := feed(newModel(Options{}),
		tea.WindowSizeMsg{Width: 40, Height: 10},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")},
		tea.KeyMsg{Type: tea.KeyEnter, Alt: true},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")},
	)
	if m.submitted {
		t.Fatal("Alt+Enter must not submit")
	}
	if !strings.Contains(m.ta.Value(), "\n") {
		t.Fatalf("Alt+Enter should insert a newline: %q", m.ta.Value())
	}
}

// Ctrl-J is the always-works newline (tmux can't always distinguish
// Shift+Enter, so Ctrl-J and Alt+Enter are the reliable fallbacks).
func TestModel_CtrlJInsertsNewline(t *testing.T) {
	m := feed(newModel(Options{}),
		tea.WindowSizeMsg{Width: 40, Height: 10},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")},
		tea.KeyMsg{Type: tea.KeyCtrlJ},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")},
	)
	if m.submitted {
		t.Fatal("Ctrl-J must not submit")
	}
	if !strings.Contains(m.ta.Value(), "\n") {
		t.Fatalf("Ctrl-J should insert a newline: %q", m.ta.Value())
	}
}

// The view shows the heading and carries NO legend/shortcut summary.
func TestModel_ViewHasTitleAndNoLegend(t *testing.T) {
	m := feed(newModel(Options{Title: "What are we doing today?", Accent: "35"}),
		tea.WindowSizeMsg{Width: 40, Height: 10},
	)
	v := m.View()
	if !strings.Contains(v, "What are we doing today?") {
		t.Fatalf("view should show the heading, got:\n%s", v)
	}
	for _, banned := range []string{"Enter", "Esc", "cancel", "submit", "⌃"} {
		if strings.Contains(v, banned) {
			t.Fatalf("view must carry no legend, found %q in:\n%s", banned, v)
		}
	}
}
