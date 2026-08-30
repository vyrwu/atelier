package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// updateAll drives the M-w "All workspaces" list, including retired ones. Enter
// restores (reactivates) the selection and switches into it.
func (m Model) updateAll(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "ctrl+k":
		if m.sel > 0 {
			m.sel--
		}
		return m, nil
	case "down", "ctrl+j":
		if m.sel < len(m.rows)-1 {
			m.sel++
		}
		return m, nil
	case "enter":
		if r, ok := m.cur(); ok {
			return m.reactivate(*r.ws)
		}
		return m, nil
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.rebuild()
			return m, nil
		}
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.rebuild()
		return m, cmd
	}
}

func (m Model) viewAll() string {
	return m.workspaceList("All workspaces", "no workspaces yet — M-n to start")
}
