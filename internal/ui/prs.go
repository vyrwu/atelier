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

// keepRegistered adds to a refresh result any registered PR on disk that the
// result doesn't have — one the agent registered while the query was running.
func keepRegistered(refreshed, onDisk []core.PR) []core.PR {
	have := map[string]bool{}
	for _, pr := range refreshed {
		have[prKey(pr.Repo, pr.Number)] = true
	}
	out := refreshed
	for _, pr := range onDisk {
		if pr.Registered && !have[prKey(pr.Repo, pr.Number)] {
			out = append(out, pr)
		}
	}
	return out
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
	return strings.ToLower(repo) + "#" + strconv.Itoa(number)
}

// refreshPRsCmd derives PRs from GitHub off the hot path (NFR-P1): the view
// renders the cache immediately; this updates it when it lands.
//
// The cached PRs are read from disk when the query runs, not taken from when the
// overlay opened: the agent registers PRs through the MCP server while the view
// sits open and polls, and a snapshot from the open would leave those out.
func refreshPRsCmd(slug string, wts []core.Worktree) tea.Cmd {
	return func() tea.Msg {
		var existing []core.PR
		if w := core.Load().Find(slug); w != nil {
			existing = w.PRs
		}
		return prsRefreshedMsg{slug: slug, prs: forge.Refresh(existing, wts)}
	}
}

// prPollInterval is how often an open PR view re-queries GitHub on its own.
// Nothing else in atelier polls (status is pushed by the Claude hooks), and this
// only runs while you are looking at the view: the popup is the process, so
// closing it ends the poll.
const prPollInterval = time.Minute

// prPollMsg is one tick of that poll. gen identifies the chain that scheduled
// it, so re-entering the view can't leave two chains ticking at once.
type prPollMsg struct{ gen int }

// prPollCmd schedules the next tick of chain gen.
func prPollCmd(gen int) tea.Cmd {
	return tea.Tick(prPollInterval, func(time.Time) tea.Msg { return prPollMsg{gen: gen} })
}

// startPRPoll begins a fresh poll chain, retiring any previous one.
func (m *Model) startPRPoll() tea.Cmd {
	m.prPollGen++
	return prPollCmd(m.prPollGen)
}

// refreshCurrentCmd refreshes the current space's PRs (the PR view is
// space-scoped). EXPERIMENT: there is no cache window — every open queries
// GitHub, so what the view shows is what GitHub says, and the cache on disk is
// only what fills the screen for the second the query is in flight.
func (m Model) refreshCurrentCmd() tea.Cmd {
	if m.curWS == nil {
		return nil
	}
	return refreshPRsCmd(m.curWS.Slug, m.wt[m.curWS.Slug])
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
	repo    string
	entries []prEntry
}

// groupRank is a repo section's most active PR state.
func groupRank(g prGroup) int {
	best := prStateRank(core.PRClosed)
	for _, e := range g.entries {
		if r := prStateRank(e.pr.State); r < best {
			best = r
		}
	}
	return best
}

// prEntry is one row: the PR plus its place in a stack. gutter is the glyph
// drawn in the stack column — empty for a PR that stands alone.
type prEntry struct {
	pr     core.PR
	gutter string
}

// Stack gutter glyphs: a line opening at the base of the stack, closing at its
// tip, with each PR in between marked by a dot. Every member sits at the same
// indent — the line carries the relationship, so nothing is staircased.
const (
	stackTop = "╭"
	stackMid = "●"
	stackEnd = "╰"
)

// stackOrder arranges one repo's PRs so that a stack reads as a unit: base
// first, each PR above the one it targets, and the whole run contiguous. A PR
// sits on another when its base is that one's head branch — derived here, never
// stored, so it follows GitHub without bookkeeping (a merged base retargets its
// children and the stack flattens on its own).
//
// Units — stacks and standalone PRs alike — are ordered by state first (open,
// draft, merged, closed) and newest-first within a state. A stack takes the
// state of its most active member, so it stays contiguous instead of being torn
// apart by a merged base sitting under an open tip.
func stackOrder(prs []core.PR) []prEntry {
	byHead := map[string]core.PR{}
	for _, pr := range prs {
		if pr.Head != "" {
			byHead[pr.Head] = pr
		}
	}
	children := map[string][]core.PR{}
	var roots []core.PR
	for _, pr := range prs {
		if _, stacked := byHead[pr.Base]; stacked && pr.Base != pr.Head {
			children[pr.Base] = append(children[pr.Base], pr)
			continue
		}
		roots = append(roots, pr)
	}
	for base := range children {
		sort.SliceStable(children[base], func(i, j int) bool {
			return children[base][i].CreatedAt.After(children[base][j].CreatedAt)
		})
	}

	// Depth-first from each root. Emitted PRs are tracked by identity, not by
	// head ref: a PR cached before head and base were recorded has neither, and
	// keying on the ref let every one of those through twice.
	emitted := map[string]bool{}
	var walk func(pr core.PR) []core.PR
	walk = func(pr core.PR) []core.PR {
		k := prKey(pr.Repo, pr.Number)
		if emitted[k] {
			return nil // already placed, or a base cycle led back here
		}
		emitted[k] = true
		out := []core.PR{pr}
		for _, c := range children[pr.Head] {
			out = append(out, walk(c)...)
		}
		return out
	}

	units := make([][]core.PR, 0, len(roots))
	for _, root := range roots {
		if u := walk(root); len(u) > 0 {
			units = append(units, u)
		}
	}
	// Anything the roots didn't reach is in a base cycle, which leaves no root to
	// start from. Walk those too rather than dropping the PRs from the view.
	for _, pr := range prs {
		if emitted[prKey(pr.Repo, pr.Number)] {
			continue
		}
		if u := walk(pr); len(u) > 0 {
			units = append(units, u)
		}
	}
	sort.SliceStable(units, func(i, j int) bool {
		if ri, rj := bestRank(units[i]), bestRank(units[j]); ri != rj {
			return ri < rj
		}
		return newestIn(units[i]).After(newestIn(units[j]))
	})

	var out []prEntry
	for _, u := range units {
		for i, pr := range u {
			out = append(out, prEntry{pr: pr, gutter: gutterFor(i, len(u))})
		}
	}
	return out
}

// gutterFor is the stack glyph for position i of a run of n.
func gutterFor(i, n int) string {
	switch {
	case n == 1:
		return "" // stands alone
	case i == 0:
		return stackTop
	case i == n-1:
		return stackEnd
	default:
		return stackMid
	}
}

// prStateRank is the order the PR view reads in: what you can still act on
// first, what is finished last.
func prStateRank(st core.PRState) int {
	switch st {
	case core.PROpen:
		return 0
	case core.PRDraft:
		return 1
	case core.PRMerged:
		return 2
	default: // closed
		return 3
	}
}

// bestRank is a unit's most active state — the one its whole run sorts by.
func bestRank(prs []core.PR) int {
	best := prStateRank(core.PRClosed)
	for _, pr := range prs {
		if r := prStateRank(pr.State); r < best {
			best = r
		}
	}
	return best
}

// newestIn is a unit's most recent PR — what orders it against the others.
func newestIn(prs []core.PR) time.Time {
	var t time.Time
	for _, pr := range prs {
		if pr.CreatedAt.After(t) {
			t = pr.CreatedAt
		}
	}
	return t
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
		groups = append(groups, prGroup{repo: r, entries: stackOrder(byRepo[r])})
	}
	// Repo sections follow the same rule as the rows inside them: the repo with
	// something still open comes before one that is all merged.
	sort.SliceStable(groups, func(i, j int) bool {
		ri, rj := groupRank(groups[i]), groupRank(groups[j])
		if ri != rj {
			return ri < rj
		}
		return groups[i].entries[0].pr.CreatedAt.After(groups[j].entries[0].pr.CreatedAt)
	})
	return groups
}

// flatPRs is the visible PRs in display order — the index space prSel navigates.
func (m Model) flatPRs() []core.PR {
	var flat []core.PR
	for _, g := range m.visiblePRs() {
		for _, e := range g.entries {
			flat = append(flat, e.pr)
		}
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
		// Reading the change is the common move; GitHub is the exception.
		if pr, ok := sel(); ok {
			return m.openDiff(pr)
		}
		return m, nil
	case "alt+b":
		if pr, ok := sel(); ok {
			_ = forge.Open(pr.URL)
		}
		return m, nil
	case "alt+e":
		if pr, ok := sel(); ok {
			return m.openChecks(pr)
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
	// While the query is out, the rows on screen are the last sweep — say so,
	// rather than letting a second-old cache pass for ground truth.
	if m.prLoading {
		b.WriteString("  " + m.spin.View() + " " + faintStyle.Render("checking github…"))
	}
	b.WriteString("\n")
	b.WriteString(m.filter.View())
	b.WriteString("\n\n")

	groups := m.visiblePRs()
	if len(groups) == 0 {
		b.WriteString(dimStyle.Render("  no pull requests yet — they appear when the agent opens them\n"))
	}
	var lines []string
	selLine, i := 0, 0
	for _, g := range groups {
		lines = append(lines, repoStyle.Render(g.repo))
		for _, e := range g.entries {
			if i == m.prSel {
				selLine = len(lines)
			}
			lines = append(lines, m.renderPRRow(e, i == m.prSel))
			i++
		}
	}
	writeList(&b, lines, selLine, m.listHeight())
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

// prTrailer is the compact CI · review · comments cluster (absent glyphs
// collapse, no holes), plus an ellipsis while a state change is in flight.
func (m Model) prTrailer(pr core.PR) string {
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
	return strings.Join(parts, " ")
}

// renderPRRow: stack gutter · state Octicon · #number · title → base  …  CI ·
// review · comments. The base rides with the title rather than in a column of
// its own, so the row reads as one phrase. It is always shown — a PR that
// targets another branch merges into that branch, and the row should never
// leave that to be guessed.
func (m Model) renderPRRow(e prEntry, selected bool) string {
	pr := e.pr
	bar := "  "
	if selected {
		bar = selBar.Render("▌") + " "
	}
	gutter := "  "
	if e.gutter != "" {
		gutter = dimStyle.Render(e.gutter) + " "
	}
	state := prStateGlyph(pr.State)
	num := dimStyle.Render(fmt.Sprintf("#%d", pr.Number))

	right := m.prTrailer(pr)
	base := ""
	if pr.Base != "" {
		base = " " + dimStyle.Render("→ "+pr.Base)
	}

	w := m.rowWidth()
	// Budget the title so the row fits on one line and the right block stays put.
	maxTitle := entryWidth(w - visibleWidth(bar) - visibleWidth(gutter) - visibleWidth(state) - visibleWidth(num) - visibleWidth(base) - visibleWidth(right) - 5)
	title := truncate(pr.Title, maxTitle)
	if selected {
		title = selStyle.Render(title)
	} else {
		title = textStyle.Render(title)
	}

	left := fmt.Sprintf("%s%s%s %s %s%s", bar, gutter, state, num, title, base)
	line := left
	if right != "" {
		pad := w - visibleWidth(left) - visibleWidth(right)
		if pad < 2 {
			pad = 2
		}
		line = left + strings.Repeat(" ", pad) + right
	}
	if selected {
		return highlightRow(line, w)
	}
	return line
}
