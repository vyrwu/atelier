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
	return m.workspaceList("Spaces", "nothing active — M-n to start · M-t to restore from trash")
}

// workspaceList renders the shared workspace-list body (used by Active and All).
func (m Model) workspaceList(title, empty string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%d", len(m.rows))))
	b.WriteString("\n")
	b.WriteString(m.filter.View()) // placeholder is "filter…"; no separate label
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
	// Right block: PR/worktree counts (collapsed) then age, right-aligned so the
	// stats form a clean column down the edge (like the PR view).
	right := dimStyle.Render(age(r.ws.Created))
	if c := m.spaceCounts(r.ws); c != "" {
		right = c + "  " + right
	}
	// The referential slug stays with the title on the left, as a dim handle.
	slug := ""
	showSlug := r.ws.Title != ""
	if showSlug {
		slug = "  " + dimStyle.Render(r.ws.Slug)
	}
	// Budget the title so the row fits on one line and the right block stays put.
	w := m.rowWidth()
	maxTitle := w - visibleWidth(bar) - 2 - visibleWidth(slug) - visibleWidth(right) - 4
	if maxTitle > 44 {
		maxTitle = 44
	}
	if maxTitle < 12 {
		maxTitle = 12
	}
	name := r.ws.Title
	if name == "" {
		name = r.ws.Slug
	}
	name = truncate(name, maxTitle)
	switch {
	case selected:
		name = selStyle.Render(name)
	case r.ws.Retired:
		name = faintStyle.Render(name)
	default:
		name = textStyle.Render(name)
	}
	left := fmt.Sprintf("%s%s %s%s", bar, g, name, slug)
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

// spaceCounts renders a workspace's PR (open/draft/merged/closed) and worktree
// counts as coloured "N<octicon>" badges, zeros omitted.
func (m Model) spaceCounts(w *core.Workspace) string {
	open, draft, merged, closed := prCounts(w)
	var parts []string
	for _, b := range []string{
		countBadge(open, iconPROpen, cOpen),
		countBadge(draft, iconPRDraft, cDraft),
		countBadge(merged, iconPRMerged, cMerged),
		countBadge(closed, iconPRClosed, cClosed),
		countBadge(len(m.wt[w.Slug]), iconWorktree, cWorktree),
	} {
		if b != "" {
			parts = append(parts, b)
		}
	}
	return strings.Join(parts, " ")
}

// prCounts tallies a workspace's PRs by state.
func prCounts(w *core.Workspace) (open, draft, merged, closed int) {
	for _, pr := range w.PRs {
		switch pr.State {
		case core.PROpen:
			open++
		case core.PRDraft:
			draft++
		case core.PRMerged:
			merged++
		default: // closed
			closed++
		}
	}
	return
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
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
