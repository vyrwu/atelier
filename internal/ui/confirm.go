package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		if m.confirmSwap {
			// The handoff runs in the space's agent window, where you can watch it;
			// the popup has nothing left to show.
			spawnDetached("swap", m.focusWS.Slug)
			return m, tea.Quit
		}
		m.screen = m.returnScreen
		if m.focusWS != nil {
			m.deleteWorkspace(*m.focusWS)
		}
		m.focusWS = nil
	case "n", "esc":
		if m.returnScreen == scrConfirm { // opened on the confirm (M-x): close
			return m, tea.Quit
		}
		m.screen = m.returnScreen
		m.focusWS, m.confirmSwap = nil, false
	}
	return m, nil
}

func (m Model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Confirm"))
	b.WriteString("\n\n")
	b.WriteString(textStyle.Render(m.confirm))
	return b.String()
}
