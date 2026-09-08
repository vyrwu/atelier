package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		m.screen = m.returnScreen
		if m.focusWS != nil {
			m.deleteWorkspace(*m.focusWS)
		}
		m.focusWS = nil
	case "n", "esc":
		m.screen = m.returnScreen
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
