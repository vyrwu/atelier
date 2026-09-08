package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/forge"
)

// prsRefreshedMsg carries a fresh PR sweep for one workspace back into the model.
type prsRefreshedMsg struct {
	slug string
	prs  []core.PR
}

// refreshPRsCmd derives PRs from GitHub off the hot path (NFR-P1): the view
// renders the cache immediately; this updates it when it lands.
func refreshPRsCmd(slug string, existing []core.PR, wts []core.Worktree) tea.Cmd {
	return func() tea.Msg {
		return prsRefreshedMsg{slug: slug, prs: forge.Refresh(existing, wts)}
	}
}

// refreshCmd refreshes every workspace's PRs concurrently (M-c is a
// cross-workspace view).
func (m Model) refreshCmd() tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.all {
		w := &m.all[i]
		cmds = append(cmds, refreshPRsCmd(w.Slug, w.PRs, m.wt[w.Slug]))
	}
	return tea.Batch(cmds...)
}

func (m Model) updatePRs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.prRows()
	switch msg.String() {
	case "up", "ctrl+k":
		if m.prSel > 0 {
			m.prSel--
		}
	case "down", "ctrl+j":
		if m.prSel < len(rows)-1 {
			m.prSel++
		}
	case "enter":
		if m.prSel >= 0 && m.prSel < len(rows) {
			_ = forge.Open(rows[m.prSel].pr.URL)
		}
	case "r", "ctrl+r":
		return m, m.refreshCmd()
	case "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) viewPRs() string {
	var b strings.Builder
	rows := m.prRows()
	b.WriteString(titleStyle.Render("Pull requests"))
	b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%d across workspaces", len(rows))))
	b.WriteString("\n\n")

	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("No pull requests yet — they appear when the agent opens them.\n"))
	}
	lastWS := ""
	for i, r := range rows {
		if r.ws.Slug != lastWS {
			b.WriteString(dimStyle.Render(r.ws.Title) + "\n")
			lastWS = r.ws.Slug
		}
		pr := r.pr
		bar := "    "
		if i == m.prSel {
			bar = "  " + selBar.Render("▌") + " "
		}
		head := fmt.Sprintf("%s%s %s  %s %s %s  ",
			bar, repoStyle.Render(pr.Repo), branchStyle.Render(fmt.Sprintf("#%d", pr.Number)),
			prBadge(pr.State), ciGlyph(pr.CI), reviewGlyph(pr.Review))
		t := pr.Title
		if i == m.prSel {
			t = selStyle.Render(t)
		} else {
			t = textStyle.Render(t)
		}
		b.WriteString(head + t + "\n")
	}
	return b.String()
}
