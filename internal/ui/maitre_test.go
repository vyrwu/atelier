package ui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/vt"
)

// Native input must survive the Bubble Tea wrapper, including paste boundaries
// and application cursor mode. No external terminal or process is used here.
func TestMaitreTerminalInput(t *testing.T) {
	for _, tc := range []struct {
		name, modes, want string
		keys              []tea.KeyMsg
	}{
		{"escape", "", "\x1b", []tea.KeyMsg{{Type: tea.KeyEsc}}},
		{"open", "", "\x1bm", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'m'}, Alt: true}}},
		{"unicode", "", "hello 世界", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("hello 世界")}}},
		{"paste", "\x1b[?2004h", "\x1b[200~one\ntwo\x1b[201~", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("one\ntwo"), Paste: true}}},
		{"arrows", "\x1b[?1h", "\x1bOA\x1b[1;5D", []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyCtrlLeft}}},
		{"control", "", "\r\t\x03\x7f", []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyTab}, {Type: tea.KeyCtrlC}, {Type: tea.KeyBackspace}}},
		{"shift tab", "", "\x1b[Z", []tea.KeyMsg{{Type: tea.KeyShiftTab}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := vt.NewEmulator(40, 10)
			received := make(chan string, 1)
			go func() { data, _ := io.ReadAll(term); received <- string(data) }()
			_, _ = term.WriteString(tc.modes)
			for _, key := range tc.keys {
				sendTerminalKey(term, key)
			}
			closeTerminal(term)
			if got := <-received; got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestMaitreFrameAndCursor(t *testing.T) {
	for _, size := range [][2]int{{96, 30}, {54, 21}, {40, 15}} {
		m := &maitreView{w: size[0], h: size[1]}
		w, h := m.size()
		m.term = vt.NewEmulator(w, h)
		_, _ = m.term.WriteString("\x1b[31mAgent text\x1b[0m\r\ninput: ")
		before := m.term.Render()
		first, second := m.View(), m.View()
		if first != second || before != m.term.Render() {
			t.Fatal("rendering changed the native terminal or cursor")
		}
		if lipgloss.Height(first) != m.h || lipgloss.Width(first) > m.w {
			t.Fatalf("frame %dx%d does not fit %dx%d", lipgloss.Width(first), lipgloss.Height(first), m.w, m.h)
		}
		if !strings.Contains(strings.Join(strings.Fields(first), " "), "esc close") || !strings.Contains(first, "Maître") {
			t.Fatal("missing title/legend")
		}
		closeTerminal(m.term)
	}
}
