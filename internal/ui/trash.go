package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// updateTrash drives the M-t Trash list of retired spaces. Enter restores the
// selection (reactivating the agent and resuming its conversation); M-d
// (handled globally) permanently deletes it behind a confirm.
func (m Model) updateTrash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) viewTrash() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Trash"))
	b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%d", len(m.rows))))
	b.WriteString("\n")
	b.WriteString(m.filter.View())
	b.WriteString("\n\n")
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("  trash is empty — deleted spaces land here, restorable with ↵\n"))
	}
	lines := make([]string, len(m.rows))
	for i, r := range m.rows {
		lines[i] = m.renderTrashRow(r, i == m.sel)
	}
	writeList(&b, lines, m.sel, m.listHeight())
	if m.err != "" {
		b.WriteString("\n" + lipglossWarn(m.err))
	}
	return b.String()
}

// renderTrashRow leads with the time since the space was closed (distinct
// yellow), then its name and the same PR/worktree overview as a Spaces row.
func (m Model) renderTrashRow(r row, selected bool) string {
	bar := "  "
	if selected {
		bar = selBar.Render("▌") + " "
	}
	when := "—"
	if !r.ws.RetiredAt.IsZero() {
		when = age(r.ws.RetiredAt)
	}
	when = trashTimeStyle.Render(padAge(when))
	right := m.spaceCounts(r.ws) // right-aligned stats column (collapsed), like Spaces
	w := m.rowWidth()
	// Budget the name to match the Spaces view (the 2 is the when/name gap).
	maxName := entryWidth(w - visibleWidth(bar) - visibleWidth(when) - 2 - visibleWidth(right) - 4)
	name := r.ws.Title
	if name == "" {
		name = r.ws.Slug
	}
	name = truncate(name, maxName)
	if selected {
		name = selStyle.Render(name)
	} else {
		name = textStyle.Render(name)
	}
	left := fmt.Sprintf("%s%s  %s", bar, when, name)
	if right == "" {
		if selected {
			return highlightRow(left, w)
		}
		return left
	}
	pad := w - visibleWidth(left) - visibleWidth(right)
	if pad < 2 {
		pad = 2
	}
	line := left + strings.Repeat(" ", pad) + right
	if selected {
		return highlightRow(line, w)
	}
	return line
}
