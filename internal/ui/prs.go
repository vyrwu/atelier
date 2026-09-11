package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/forge"
)

// prsRefreshedMsg carries a fresh PR sweep for one workspace back into the model.
type prsRefreshedMsg struct {
	slug string
	prs  []core.PR
}

// prMutatedMsg carries the result of an M-o/M-c/M-d state change back into the
// model so the row re-renders in place.
type prMutatedMsg struct {
	slug   string
	repo   string
	number int
	pr     core.PR
	err    error
}

// prKey identifies a PR across the model (repo#number).
func prKey(repo string, number int) string {
	return repo + "#" + strconv.Itoa(number)
}

// refreshPRsCmd derives PRs from GitHub off the hot path (NFR-P1): the view
// renders the cache immediately; this updates it when it lands.
func refreshPRsCmd(slug string, existing []core.PR, wts []core.Worktree) tea.Cmd {
	return func() tea.Msg {
		return prsRefreshedMsg{slug: slug, prs: forge.Refresh(existing, wts)}
	}
}

// prCacheTTL is how long a PR sweep stays fresh: opening the PR view again
// within this window reuses the cache instead of re-hitting gh (the ~4s query).
const prCacheTTL = 5 * time.Minute

// refreshCurrentCmd refreshes the current space's PRs (the PR view is
// space-scoped). Unless force is set, a sweep newer than prCacheTTL is reused —
// so consecutive opens are instant. force (re-pressing M-p) always re-fetches.
func (m Model) refreshCurrentCmd(force bool) tea.Cmd {
	if m.curWS == nil {
		return nil
	}
	if !force && time.Since(m.curWS.PRsRefreshed) < prCacheTTL {
		return nil
	}
	return refreshPRsCmd(m.curWS.Slug, m.curWS.PRs, m.wt[m.curWS.Slug])
}

// setPRStateCmd applies a state change on GitHub, then re-queries the PR.
func setPRStateCmd(slug string, pr core.PR, target core.PRState) tea.Cmd {
	return func() tea.Msg {
		fresh, err := forge.SetPRState(pr, target)
		return prMutatedMsg{slug: slug, repo: pr.Repo, number: pr.Number, pr: fresh, err: err}
	}
}

// prGroup is a repo section in the PR view.
type prGroup struct {
	repo string
	prs  []core.PR
}

// visiblePRs applies the filter and groups the current space's PRs by repo, repo
// sections and entries newest-first by PR creation time.
func (m Model) visiblePRs() []prGroup {
	if m.curWS == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	var order []string
	byRepo := map[string][]core.PR{}
	for _, pr := range m.curWS.PRs {
		if q != "" && !strings.Contains(strings.ToLower(fmt.Sprintf("%s #%d %s", pr.Repo, pr.Number, pr.Title)), q) {
			continue
		}
		if _, ok := byRepo[pr.Repo]; !ok {
			order = append(order, pr.Repo)
		}
		byRepo[pr.Repo] = append(byRepo[pr.Repo], pr)
	}
	groups := make([]prGroup, 0, len(order))
	for _, r := range order {
		prs := byRepo[r]
		sort.SliceStable(prs, func(i, j int) bool { return prs[i].CreatedAt.After(prs[j].CreatedAt) })
		groups = append(groups, prGroup{repo: r, prs: prs})
	}
	// Order repo sections by their newest PR.
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].prs[0].CreatedAt.After(groups[j].prs[0].CreatedAt)
	})
	return groups
}

// flatPRs is the visible PRs in display order — the index space prSel navigates.
func (m Model) flatPRs() []core.PR {
	var flat []core.PR
	for _, g := range m.visiblePRs() {
		flat = append(flat, g.prs...)
	}
	return flat
}

func (m Model) updatePRs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	flat := m.flatPRs()
	sel := func() (core.PR, bool) {
		if m.prSel >= 0 && m.prSel < len(flat) {
			return flat[m.prSel], true
		}
		return core.PR{}, false
	}
	if msg.String() != "alt+y" {
		m.note = "" // clear stale copy feedback on the next action
	}
	switch msg.String() {
	case "alt+y":
		if pr, ok := sel(); ok {
			if err := copyToClipboard(prLink(pr)); err != nil {
				m.err = "copy failed: " + err.Error()
			} else {
				m.err = ""
				m.note = "copied link · " + truncate(pr.Title, 48)
			}
		}
		return m, nil
	case "up", "ctrl+k":
		if m.prSel > 0 {
			m.prSel--
		}
		return m, nil
	case "down", "ctrl+j":
		if m.prSel < len(flat)-1 {
			m.prSel++
		}
		return m, nil
	case "enter":
		if pr, ok := sel(); ok {
			_ = forge.Open(pr.URL)
		}
		return m, nil
	case "alt+o":
		if pr, ok := sel(); ok {
			return m.mutatePR(pr, core.PROpen)
		}
		return m, nil
	case "alt+c":
		if pr, ok := sel(); ok {
			return m.mutatePR(pr, core.PRClosed)
		}
		return m, nil
	case "alt+d":
		if pr, ok := sel(); ok {
			return m.mutatePR(pr, core.PRDraft)
		}
		return m, nil
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.prSel = 0
			return m, nil
		}
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		if m.prSel >= len(m.flatPRs()) {
			m.prSel = 0
		}
		return m, cmd
	}
}

// mutatePR fires a state change. Merged PRs are terminal (toast, no-op).
// Otherwise it flips the icon optimistically and marks the row in-flight; the
// re-query reconciles it (or reverts) when prMutatedMsg lands.
func (m Model) mutatePR(pr core.PR, target core.PRState) (tea.Model, tea.Cmd) {
	if pr.State == core.PRMerged {
		m.err = fmt.Sprintf("#%d is merged — its state can't be changed", pr.Number)
		return m, nil
	}
	if pr.State == target {
		return m, nil // already there
	}
	m.err = ""
	m.setPRStateLocal(pr.Repo, pr.Number, target) // optimistic: flip now
	m.prPending[prKey(pr.Repo, pr.Number)] = true
	return m, setPRStateCmd(m.curWS.Slug, pr, target)
}

// setPRStateLocal flips a PR's state in the in-memory model (optimistic update),
// leaving persistence to the ground-truth reconcile in prMutatedMsg.
func (m Model) setPRStateLocal(repo string, number int, state core.PRState) {
	for i := range m.all {
		for j := range m.all[i].PRs {
			if m.all[i].PRs[j].Repo == repo && m.all[i].PRs[j].Number == number {
				m.all[i].PRs[j].State = state
			}
		}
	}
}

func (m Model) viewPRs() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Pull requests"))
	if m.curWS != nil {
		b.WriteString("  " + dimStyle.Render(m.curWS.Title))
	}
	b.WriteString("\n")
	b.WriteString(m.filter.View())
	b.WriteString("\n\n")

	groups := m.visiblePRs()
	if len(groups) == 0 {
		b.WriteString(dimStyle.Render("  no pull requests yet — they appear when the agent opens them\n"))
	}
	i := 0
	for _, g := range groups {
		b.WriteString(repoStyle.Render(g.repo) + "\n")
		for _, pr := range g.prs {
			b.WriteString(m.renderPRRow(pr, i == m.prSel))
			b.WriteString("\n")
			i++
		}
	}
	if m.note != "" {
		b.WriteString("\n" + noteStyle.Render("✓ "+m.note))
	}
	if m.err != "" {
		b.WriteString("\n" + lipglossWarn(m.err))
	}
	return b.String()
}

// prLink formats a PR as a Markdown hyperlink — the title as the link text — so
// pasting it yields a titled link, not a bare URL.
func prLink(pr core.PR) string {
	return fmt.Sprintf("[%s](%s)", pr.Title, pr.URL)
}

// renderPRRow: state Octicon · #number · title  …  CI · review · comments. The
// CI/review/comment cluster is compact (absent glyphs collapse, no holes) and
// right-aligned so it forms a clean column down the right edge.
func (m Model) renderPRRow(pr core.PR, selected bool) string {
	bar := "  "
	if selected {
		bar = selBar.Render("▌") + " "
	}
	state := prStateGlyph(pr.State)
	num := dimStyle.Render(fmt.Sprintf("#%d", pr.Number))

	var parts []string
	if m.prPending[prKey(pr.Repo, pr.Number)] {
		parts = append(parts, faintStyle.Render("…")) // state change in flight
	}
	if g := ciGlyph(pr.CI); g != "" {
		parts = append(parts, g)
	}
	if g := reviewGlyph(pr.Review); g != "" {
		parts = append(parts, g)
	}
	if pr.Comments > 0 {
		parts = append(parts, dimStyle.Render(fmt.Sprintf("%d%s", pr.Comments, iconComment)))
	}
	trailer := strings.Join(parts, " ")

	w := m.w - 6
	if w < 24 {
		w = 24
	}
	// Budget the title so the row fits on one line and the trailer stays put.
	maxTitle := w - visibleWidth(bar) - visibleWidth(state) - visibleWidth(num) - visibleWidth(trailer) - 5
	if maxTitle > 80 {
		maxTitle = 80
	}
	if maxTitle < 10 {
		maxTitle = 10
	}
	title := truncate(pr.Title, maxTitle)
	if selected {
		title = selStyle.Render(title)
	} else {
		title = textStyle.Render(title)
	}

	left := fmt.Sprintf("%s%s %s %s", bar, state, num, title)
	line := left
	if trailer != "" {
		pad := w - visibleWidth(left) - visibleWidth(trailer)
		if pad < 2 {
			pad = 2
		}
		line = left + strings.Repeat(" ", pad) + trailer
	}
	if selected {
		return highlightRow(line, m.rowWidth())
	}
	return line
}
