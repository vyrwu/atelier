package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// Every list view budgets its entry text from the same range, so a long title,
// name, or branch cuts at the same place wherever you meet it. The views used to
// disagree — 44 in Spaces and Trash, 80 in PRs, uncapped in Worktrees.
func TestRowEntryBudgetsAgree(t *testing.T) {
	m := Model{w: 200} // wide enough that every view reaches the cap
	long := strings.Repeat("x", 300)
	cut := strings.Repeat("x", maxEntryWidth-1) + "…"

	ws := &core.Workspace{Slug: "brave-otter", Title: long, Created: time.Now()}
	views := map[string]string{
		"spaces":    m.renderRow(row{ws: ws}, false),
		"trash":     m.renderTrashRow(row{ws: &core.Workspace{Slug: "brave-otter", Title: long, RetiredAt: time.Now()}}, false),
		"prs":       m.renderPRRow(prEntry{pr: core.PR{Repo: "o/r", Number: 100, Title: long, State: core.PROpen, CI: core.CIPass}}, false),
		"worktrees": m.renderWorktreeRow(wtEntry{wt: core.Worktree{Repo: "o/r", Branch: long}, okFresh: true}, false),
	}
	for name, line := range views {
		if !strings.Contains(line, cut) {
			t.Errorf("%s: entry not cut to the shared budget of %d", name, maxEntryWidth)
		}
	}
}

// A narrow terminal must still leave a readable entry rather than collapsing it
// to nothing or overflowing the row.
func TestRowEntryBudgetsNarrow(t *testing.T) {
	m := Model{w: 30}
	long := strings.Repeat("x", 300)
	floor := strings.Repeat("x", minEntryWidth-1) + "…"

	ws := &core.Workspace{Slug: "brave-otter", Title: long, Created: time.Now()}
	if line := m.renderRow(row{ws: ws}, false); !strings.Contains(line, floor) {
		t.Errorf("spaces: narrow row not held at the %d-cell floor: %q", minEntryWidth, line)
	}
	if line := m.renderTrashRow(row{ws: ws}, false); !strings.Contains(line, floor) {
		t.Errorf("trash: narrow row not held at the %d-cell floor: %q", minEntryWidth, line)
	}
}

// A space with no title falls back to its slug, in both the Spaces and Trash
// lists — an untitled space must never render as a blank line.
func TestRowFallsBackToSlug(t *testing.T) {
	m := Model{w: 120}
	ws := &core.Workspace{Slug: "brave-otter", Created: time.Now()}
	if line := m.renderRow(row{ws: ws}, false); !strings.Contains(line, "brave-otter") {
		t.Errorf("spaces row lost the slug: %q", line)
	}
	if line := m.renderTrashRow(row{ws: ws}, false); !strings.Contains(line, "brave-otter") {
		t.Errorf("trash row lost the slug: %q", line)
	}
}

// The selected row's band spans exactly the row width in every view — the band
// is painted over a line whose own width the budget is supposed to keep in check.
func TestSelectedRowSpansRowWidth(t *testing.T) {
	m := Model{w: 120}
	ws := &core.Workspace{Slug: "brave-otter", Title: strings.Repeat("x", 300), Created: time.Now()}
	lines := map[string]string{
		"spaces":    m.renderRow(row{ws: ws}, true),
		"trash":     m.renderTrashRow(row{ws: ws}, true),
		"prs":       m.renderPRRow(prEntry{pr: core.PR{Repo: "o/r", Number: 100, Title: "short", State: core.PROpen}}, true),
		"worktrees": m.renderWorktreeRow(wtEntry{wt: core.Worktree{Repo: "o/r", Branch: "feat/x"}, okFresh: true}, true),
	}
	for name, line := range lines {
		if w := visibleWidth(line); w != m.rowWidth() {
			t.Errorf("%s: selected row spans %d cells, want the row width %d", name, w, m.rowWidth())
		}
	}
}

func TestAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		in   time.Time
		want string
	}{
		{now, "now"},
		{now.Add(-90 * time.Second), "1m"},
		{now.Add(-3 * time.Hour), "3h"},
		{now.Add(-50 * time.Hour), "2d"},
	}
	for _, c := range cases {
		if got := age(c.in); got != c.want {
			t.Errorf("age(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Zero counts are omitted so a quiet space shows a quiet row (FR-B1); a count
// that exists renders as "N<glyph>".
func TestSpaceCounts(t *testing.T) {
	m := Model{}
	if got := m.spaceCounts(&core.Workspace{Slug: "s"}); got != "" {
		t.Errorf("no PRs, no worktrees: got %q, want empty", got)
	}
	ws := &core.Workspace{Slug: "s", PRs: []core.PR{
		{State: core.PROpen}, {State: core.PROpen}, {State: core.PRMerged},
	}}
	got := m.spaceCounts(ws)
	if !strings.Contains(got, "2"+iconPROpen) || !strings.Contains(got, "1"+iconPRMerged) {
		t.Errorf("spaceCounts = %q, want 2 open and 1 merged", got)
	}
	if strings.Contains(got, iconPRDraft) || strings.Contains(got, iconPRClosed) {
		t.Errorf("spaceCounts = %q, want zero states omitted", got)
	}
}

func TestPRCounts(t *testing.T) {
	open, draft, merged, closed := prCounts(&core.Workspace{PRs: []core.PR{
		{State: core.PROpen}, {State: core.PRDraft}, {State: core.PRDraft},
		{State: core.PRMerged}, {State: core.PRClosed},
	}})
	if open != 1 || draft != 2 || merged != 1 || closed != 1 {
		t.Errorf("prCounts = %d/%d/%d/%d, want 1/2/1/1", open, draft, merged, closed)
	}
}

// The age is the last thing on a Spaces row, so it has to occupy a fixed cell:
// an unpadded "2d" next to a "23m" shifts that row's count badges by a cell and
// the columns stop lining up down the list.
func TestCountBadgesLineUpAcrossAges(t *testing.T) {
	m := Model{w: 120}
	now := time.Now()
	ages := []time.Time{
		now,                           // "now"
		now.Add(-23 * time.Minute),    // "23m"
		now.Add(-2 * 24 * time.Hour),  // "2d"
		now.Add(-14 * 24 * time.Hour), // "14d"
	}
	col := -1
	for _, created := range ages {
		ws := &core.Workspace{Slug: "s", Title: "space", Created: created, PRs: []core.PR{{State: core.PROpen}}}
		line := m.renderRow(row{ws: ws}, false)
		at := strings.Index(ansiRe.ReplaceAllString(line, ""), "1"+iconPROpen)
		if at < 0 {
			t.Fatalf("badge missing from %q", line)
		}
		if col == -1 {
			col = at
			continue
		}
		if at != col {
			t.Errorf("badge column moved to %d (was %d) for age %q", at, col, age(created))
		}
	}
}

func TestPadAge(t *testing.T) {
	cases := map[string]string{
		"now":   " now",
		"2d":    "  2d",
		"23m":   " 23m",
		"14d":   " 14d",
		"—":     "   —",
		"365d":  "365d",
		"1000d": "1000d", // never truncated — a longer age widens the cell
	}
	for in, want := range cases {
		if got := padAge(in); got != want {
			t.Errorf("padAge(%q) = %q, want %q", in, got, want)
		}
	}
}

// Badge counts occupy a fixed field: a two-digit count used to be a cell wider
// than a one-digit one, and because the badges are a right-aligned block, that
// pushed every icon to its left out of column.
func TestCountBadgesAreFixedWidth(t *testing.T) {
	one := countBadge(1, iconWorktree, cWorktree)
	two := countBadge(13, iconWorktree, cWorktree)
	if visibleWidth(one) != visibleWidth(two) {
		t.Errorf("badge widths differ: %d (%q) vs %d (%q)", visibleWidth(one), one, visibleWidth(two), two)
	}
	if countBadge(0, iconWorktree, cWorktree) != "" {
		t.Error("a zero count should render nothing at all")
	}
	// Three digits widen the row rather than losing a digit.
	if got := visibleWidth(countBadge(100, iconWorktree, cWorktree)); got != visibleWidth(one)+1 {
		t.Errorf("three-digit badge width = %d, want one more than %d", got, visibleWidth(one))
	}
}

// Two rows carrying the same kinds of badge line up, whatever the counts.
func TestBadgeColumnsLineUpAcrossCounts(t *testing.T) {
	m := Model{w: 120, wtCount: map[string]int{"a": 4, "b": 13}}
	col := func(slug string, open int) int {
		ws := &core.Workspace{Slug: slug, Title: "space", Created: time.Now()}
		for i := 0; i < open; i++ {
			ws.PRs = append(ws.PRs, core.PR{State: core.PROpen})
		}
		line := ansiRe.ReplaceAllString(m.renderRow(row{ws: ws}, false), "")
		return strings.Index(line, iconWorktree)
	}
	if a, b := col("a", 1), col("b", 9); a != b {
		t.Errorf("worktree icon at %d vs %d — counts of different width shift the column", a, b)
	}
}
