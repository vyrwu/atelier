package ui

import (
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/git"
)

// wtFreshMsg carries computed ahead/behind counts back into the model, keyed by
// worktree path.
type wtFreshMsg struct {
	slug  string
	fresh map[string]wtFresh
}

type wtFresh struct {
	ahead, behind int
	ok            bool
}

// loadCurrentWorktrees derives the current space's worktrees, branches included,
// and builds its rows. Scoped to one space on purpose: this is the only view
// that names a branch, and the cost is a git process per worktree.
func (m *Model) loadCurrentWorktrees() {
	if m.curWS == nil {
		return
	}
	m.wt[m.curWS.Slug] = git.Worktrees(m.curWS.Root())
	m.listWorktrees()
}

// listWorktrees fills the worktree rows from what the filesystem already told
// us — repo, branch, path — with no freshness yet. This runs on the path that
// builds the model, so it must not shell out: comparing each branch against its
// default branch costs a git process per worktree, which used to be paid before
// the overlay drew its first frame, on every screen.
func (m *Model) listWorktrees() {
	m.wtEntries = nil
	if m.curWS == nil {
		return
	}
	for _, wt := range m.wt[m.curWS.Slug] {
		m.wtEntries = append(m.wtEntries, wtEntry{wt: wt})
	}
	sort.SliceStable(m.wtEntries, func(i, j int) bool {
		return m.wtEntries[i].wt.Created.After(m.wtEntries[j].wt.Created)
	})
}

// freshnessCmd computes how far each of the current space's worktrees has
// drifted from its default branch, off the hot path (NFR-P1).
func (m Model) freshnessCmd() tea.Cmd {
	if m.curWS == nil || len(m.wtEntries) == 0 {
		return nil
	}
	slug := m.curWS.Slug
	paths := make([]string, 0, len(m.wtEntries))
	for _, e := range m.wtEntries {
		paths = append(paths, e.wt.Path)
	}
	return func() tea.Msg {
		fresh := make(map[string]wtFresh, len(paths))
		for _, p := range paths {
			a, b, ok := git.AheadBehindCached(p, wtFreshnessTTL)
			fresh[p] = wtFresh{ahead: a, behind: b, ok: ok}
		}
		return wtFreshMsg{slug: slug, fresh: fresh}
	}
}

// applyFreshness merges a completed sweep into the rows on screen, ignoring one
// that belongs to a space the overlay has since moved off.
func (m *Model) applyFreshness(msg wtFreshMsg) {
	if m.curWS == nil || m.curWS.Slug != msg.slug {
		return
	}
	for i := range m.wtEntries {
		if f, ok := msg.fresh[m.wtEntries[i].wt.Path]; ok {
			m.wtEntries[i].ahead, m.wtEntries[i].behind, m.wtEntries[i].okFresh = f.ahead, f.behind, f.ok
		}
	}
}
