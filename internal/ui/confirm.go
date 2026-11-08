package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

func (m *Model) clearConfirmation() {
	m.focusWS = nil
	m.confirmSwap = false
	m.confirm = ""
	m.swapTo = ""
	m.returnScreen = scrActive
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		if m.confirmSwap {
			// The handoff runs in the space's agent window, where you can watch it;
			// the popup has nothing left to show.
			spawnDetached("swap", m.focusWS.Slug, "--to", string(m.swapTo))
			return m, tea.Quit
		}
		m.screen = m.returnScreen
		if m.focusWS != nil {
			m.deleteWorkspace(*m.focusWS)
		}
		m.focusWS = nil
	case "tab":
		// Cycle through agents, including a new conversation with the same one.
		if m.confirmSwap {
			m.swapTo = m.swapTo.Next()
		}
	case "n", "esc":
		if m.returnScreen == scrConfirm { // opened on the confirm (M-x): close
			return m, tea.Quit
		}
		m.screen = m.returnScreen
		m.focusWS, m.confirmSwap = nil, false
	}
	return m, nil
}

// The swap dialog's size, which M-x opens its popup at.
const (
	swapDialogW = 46
	swapDialogH = 11
)

// swapDialog is the small confirm for handing focusWS to another agent, w by
// h cells, border included.
func (m Model) swapDialog(w, h int) string {
	from := agent.Of(m.focusWS.Agent)
	to := m.swapTo
	inner := w - 4 // border and padding
	name, where := to.Label(), "another agent"
	if to == from {
		name = "new " + to.Label()
		where = "a new conversation"
	}
	title := titleStyle.Render("Swap to ") + agentGlyph(to) + " " +
		dimStyle.Bold(true).Render(name) + titleStyle.Render("?")
	center := lipgloss.NewStyle().Width(inner).Align(lipgloss.Center)
	lines := []string{title, "", center.Render(dimStyle.Render("Current conversation is saved,\nand shipped to " + where + "."))}
	if m.status[m.focusWS.Session] == core.StatusWorking {
		lines = append(lines, center.Render(lipglossWarn(from.Label()+" is mid-turn; it stops now.")))
	}
	half := lipgloss.NewStyle().Width(inner / 2).Align(lipgloss.Center)
	lines = append(lines, "", half.Render(textStyle.Render("[y]es"))+half.Render(textStyle.Render("[n]o")),
		dimStyle.Render(strings.Repeat("─", inner)), hintStyle.Render("tab switch agent"))
	return frameStyle.Width(w - 2).Height(h - 2).Render(strings.Join(lines, "\n"))
}

func (m Model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Confirm"))
	b.WriteString("\n\n")
	b.WriteString(textStyle.Render(m.confirm))
	return b.String()
}
