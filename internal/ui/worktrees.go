package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

func (m Model) updateWorktrees(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.wtRows()
	switch msg.String() {
	case "up", "ctrl+k":
		if m.wtSel > 0 {
			m.wtSel--
		}
	case "down", "ctrl+j":
		if m.wtSel < len(rows)-1 {
			m.wtSel++
		}
	case "enter":
		if m.wtSel >= 0 && m.wtSel < len(rows) {
			return m.openWorktreeShell(rows[m.wtSel])
		}
	case "esc":
		return m, tea.Quit
	}
	return m, nil
}

// openWorktreeShell opens (or re-selects) a dedicated persistent shell window for
// a worktree, in its workspace session, then switches in. Reusing the same named
// window per worktree means you can swap between worktree shells with M-t without
// losing background jobs running in them.
func (m Model) openWorktreeShell(r wtRow) (tea.Model, tea.Cmd) {
	session := r.ws.Session
	// Make sure the session exists (revive the agent if it died) so the shell has
	// a home to live in.
	_ = agent.EnsureClaude(session, r.ws.Root(), r.ws.Slug)
	name := wtWindow(r.wt)
	if tmux.HasWindow(session, name) {
		_ = tmux.SelectWindow(session, name)
	} else {
		_ = tmux.NewWindow(session, r.wt.Path, name)
	}
	_ = tmux.SwitchClient(m.outer, session)
	return m, tea.Quit
}

// wtWindow is the stable tmux window name for a worktree's dedicated shell.
func wtWindow(wt core.Worktree) string {
	return sanitizeWindow(wt.Repo + "/" + wt.Branch)
}

// sanitizeWindow makes a token safe as a tmux window name / target (no ':' or
// '/' which confuse target parsing).
func sanitizeWindow(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', ':', ' ', '\t':
			return '-'
		}
		return r
	}, s)
}

func (m Model) viewWorktrees() string {
	var b strings.Builder
	rows := m.wtRows()
	b.WriteString(titleStyle.Render("Worktrees"))
	b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%d across workspaces", len(rows))))
	b.WriteString("\n\n")

	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("No worktrees yet — the agent creates them as it works.\n"))
	}
	lastWS := ""
	for i, r := range rows {
		if r.ws.Slug != lastWS {
			b.WriteString(dimStyle.Render(r.ws.Title) + "\n")
			lastWS = r.ws.Slug
		}
		bar := "    "
		if i == m.wtSel {
			bar = "  " + selBar.Render("▌") + " "
		}
		label := repoStyle.Render(r.wt.Repo) + dimStyle.Render("/") + branchStyle.Render(r.wt.Branch)
		if i == m.wtSel {
			label = selStyle.Render(r.wt.Repo + "/" + r.wt.Branch)
		}
		b.WriteString(bar + label + "\n")
	}
	return b.String()
}
