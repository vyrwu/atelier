package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/tmux"
)

// wtFreshnessTTL caches a worktree's ahead/behind for this long, so reopening the
// worktree view within the window skips re-running git (mirrors the PR cache).
const wtFreshnessTTL = 5 * time.Minute

// wtEntry is a worktree of the current space with its freshness against the
// repo's default branch, computed once (git is not cheap) and cached.
type wtEntry struct {
	wt      core.Worktree
	ahead   int
	behind  int
	okFresh bool
}

// computeWorktrees caches the current space's worktrees with ahead/behind
// freshness, newest first. No-op (empty) off a space.
func (m *Model) computeWorktrees() {
	m.wtEntries = nil
	if m.curWS == nil {
		return
	}
	wts := m.wt[m.curWS.Slug]
	for _, wt := range wts {
		a, b, ok := git.AheadBehindCached(wt.Path, wtFreshnessTTL)
		m.wtEntries = append(m.wtEntries, wtEntry{wt: wt, ahead: a, behind: b, okFresh: ok})
	}
	sort.SliceStable(m.wtEntries, func(i, j int) bool {
		return m.wtEntries[i].wt.Created.After(m.wtEntries[j].wt.Created)
	})
}

// wtGroup is a repo section in the worktree view.
type wtGroup struct {
	repo    string
	entries []wtEntry
}

// visibleWorktrees applies the filter and groups by repo, repo sections ordered
// by their newest worktree (entries already newest-first).
func (m Model) visibleWorktrees() []wtGroup {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	var order []string
	byRepo := map[string][]wtEntry{}
	for _, e := range m.wtEntries {
		if q != "" && !strings.Contains(strings.ToLower(e.wt.Repo+"/"+e.wt.Branch), q) {
			continue
		}
		if _, ok := byRepo[e.wt.Repo]; !ok {
			order = append(order, e.wt.Repo)
		}
		byRepo[e.wt.Repo] = append(byRepo[e.wt.Repo], e)
	}
	groups := make([]wtGroup, 0, len(order))
	for _, r := range order {
		groups = append(groups, wtGroup{repo: r, entries: byRepo[r]})
	}
	return groups
}

// flatWorktrees is the visible entries in display order — the index space wtSel
// navigates.
func (m Model) flatWorktrees() []wtEntry {
	var flat []wtEntry
	for _, g := range m.visibleWorktrees() {
		flat = append(flat, g.entries...)
	}
	return flat
}

func (m Model) updateWorktrees(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	flat := m.flatWorktrees()
	switch msg.String() {
	case "up", "ctrl+k":
		if m.wtSel > 0 {
			m.wtSel--
		}
		return m, nil
	case "down", "ctrl+j":
		if m.wtSel < len(flat)-1 {
			m.wtSel++
		}
		return m, nil
	case "enter":
		if m.wtSel >= 0 && m.wtSel < len(flat) {
			return m.openWorktreeShell(flat[m.wtSel].wt)
		}
		return m, nil
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.wtSel = 0
			return m, nil
		}
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		if m.wtSel >= len(m.flatWorktrees()) {
			m.wtSel = 0
		}
		return m, cmd
	}
}

// openWorktreeShell opens (or re-selects) a dedicated persistent shell window for
// a worktree, in the current space's session, then switches in. The base shell
// (M-c) and each worktree's shell are distinct named windows, so M-w navigates
// between worktree shells without losing background jobs, and M-c always returns
// to the base shell.
func (m Model) openWorktreeShell(wt core.Worktree) (tea.Model, tea.Cmd) {
	if m.curWS == nil {
		return m, tea.Quit
	}
	session := m.curWS.Session
	_ = agent.EnsureClaude(session, m.curWS.Root(), m.curWS.Slug) // ensure the session is alive
	name := wtWindow(wt)
	if tmux.HasWindow(session, name) {
		_ = tmux.SelectWindow(session, name)
	} else {
		_ = tmux.NewWindow(session, wt.Path, name)
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
	b.WriteString(titleStyle.Render("Worktrees"))
	if m.curWS != nil {
		b.WriteString("  " + dimStyle.Render(m.curWS.Title))
	}
	b.WriteString("\n")
	b.WriteString(m.filter.View())
	b.WriteString("\n\n")

	groups := m.visibleWorktrees()
	if len(groups) == 0 {
		b.WriteString(dimStyle.Render("  no worktrees yet — the agent creates them as it works\n"))
	}
	i := 0
	for _, g := range groups {
		b.WriteString(repoStyle.Render(g.repo) + "\n")
		for _, e := range g.entries {
			b.WriteString(m.renderWorktreeRow(e, i == m.wtSel))
			b.WriteString("\n")
			i++
		}
	}
	return b.String()
}

// renderWorktreeRow shows the branch name, then its freshness against the
// default branch (✓ current, else behind/ahead counts).
func (m Model) renderWorktreeRow(e wtEntry, selected bool) string {
	bar := "  "
	if selected {
		bar = selBar.Render("▌") + " "
	}
	branch := e.wt.Branch
	if selected {
		branch = selStyle.Render(branch)
	} else {
		branch = branchStyle.Render(branch)
	}
	line := fmt.Sprintf("%s%s  %s", bar, branch, freshnessGlyph(e))
	if selected {
		return highlightRow(line, m.rowWidth())
	}
	return line
}

// freshnessGlyph renders a worktree's ahead/behind against the default branch: a
// green check when current, else red "N<behind>" and/or teal "N<ahead>", and a
// faint dot when it can't be computed.
func freshnessGlyph(e wtEntry) string {
	if !e.okFresh {
		return faintStyle.Render("·")
	}
	if e.ahead == 0 && e.behind == 0 {
		return lipgloss.NewStyle().Foreground(cOpen).Render(iconCheck)
	}
	var parts []string
	if b := countBadge(e.behind, iconBehind, cClosed); b != "" {
		parts = append(parts, b)
	}
	if a := countBadge(e.ahead, iconAhead, cWorking); a != "" {
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
