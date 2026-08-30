package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/git"
)

// updateActive drives the M-a "Active workspaces" list: the working set.
func (m Model) updateActive(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
			return m.switchTo(r.ws.Session)
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

// cur returns the selected row.
func (m Model) cur() (row, bool) {
	if m.sel >= 0 && m.sel < len(m.rows) {
		return m.rows[m.sel], true
	}
	return row{}, false
}

// deletePrompt names what a permanent delete will destroy, warning about dirty
// worktrees (warn-and-allow).
func (m Model) deletePrompt(w *core.Workspace) string {
	wts := m.wt[w.Slug]
	var dirty []string
	for _, t := range wts {
		if git.IsDirty(t.Path) {
			dirty = append(dirty, t.Repo+"/"+t.Branch)
		}
	}
	s := fmt.Sprintf("Delete %q permanently — removes %d worktree(s), the session, and the directory.",
		w.Title, len(wts))
	if len(dirty) > 0 {
		s += "\n\n" + lipglossWarn("uncommitted changes in: "+strings.Join(dirty, ", "))
	}
	return s
}

func (m Model) viewActive() string {
	return m.workspaceList("Active workspaces", "nothing active — M-n to start · M-w to restore")
}

// workspaceList renders the shared workspace-list body (used by Active and All).
func (m Model) workspaceList(title, empty string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%d", len(m.rows))))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("filter ") + m.filter.View())
	b.WriteString("\n\n")
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("  " + empty + "\n"))
	}
	for i, r := range m.rows {
		b.WriteString(m.renderRow(r, i == m.sel))
		b.WriteString("\n")
	}
	if m.err != "" {
		b.WriteString("\n" + lipglossWarn(m.err))
	}
	return b.String()
}

func (m Model) renderRow(r row, selected bool) string {
	bar := "  "
	if selected {
		bar = selBar.Render("▌") + " "
	}
	var g string
	if r.ws.Retired {
		g = faintStyle.Render("◦")
	} else {
		g = statusGlyph(m.status[r.ws.Session])
	}
	title := r.ws.Title
	switch {
	case selected:
		title = selStyle.Render(title)
	case r.ws.Retired:
		title = faintStyle.Render(title)
	default:
		title = textStyle.Render(title)
	}
	meta := dimStyle.Render(age(r.ws.Created))
	if n := openPRs(r.ws); n > 0 {
		meta = dimStyle.Render(fmt.Sprintf("%d PR · %s", n, age(r.ws.Created)))
	}
	return fmt.Sprintf("%s%s %s  %s", bar, g, title, meta)
}

// openPRs counts a workspace's open/draft PRs, shown in its row meta.
func openPRs(w *core.Workspace) int {
	n := 0
	for _, pr := range w.PRs {
		if pr.State == core.PROpen || pr.State == core.PRDraft {
			n++
		}
	}
	return n
}

// age renders a compact relative time ("3m", "2h", "5d").
func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
