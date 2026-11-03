package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateNew(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if msg.Alt { // Alt+Enter → start and switch in
			return m.beginCreate(m.intent.Value(), true)
		}
		return m.beginCreate(m.intent.Value(), false) // Enter → start in the background
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyTab:
		m.newAgent = m.newAgent.Other()
		return m, nil
	}
	// Enter is submit; newline is Ctrl-J only (Alt+Enter is start-and-open).
	m.intent.KeyMap.InsertNewline.SetKeys("ctrl+j")
	var cmd tea.Cmd
	m.intent, cmd = m.intent.Update(msg)
	return m, cmd
}

func (m Model) viewNew() string {
	var b strings.Builder
	// The agent is part of the question, so Tab reads as asking the other one.
	b.WriteString(titleStyle.Render("What can ") + agentGlyph(m.newAgent) + " " +
		dimStyle.Bold(true).Render(m.newAgent.Label()) +
		titleStyle.Render(" do for you?"))
	b.WriteString("\n\n")
	b.WriteString(m.intent.View())
	return b.String()
}
