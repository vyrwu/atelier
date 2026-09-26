package ui

import (
	"strings"
	"testing"
)

// visibleWidth is what every row's column math is built on: ANSI must not count,
// and a Nerd glyph must count as the one cell it renders as (go-runewidth calls
// the private-use block zero-width, which would silently skew every trailer).
func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"plain", "abc", 3},
		{"ansi-stripped", selBg + "abc" + ansiReset, 3},
		{"nerd-glyph-is-one-cell", iconPROpen, 1},
		{"nerd-glyph-with-text", "3" + iconWorktree, 2},
		{"wide-rune", "日本", 4},
		{"empty", "", 0},
	}
	for _, c := range cases {
		if got := visibleWidth(c.in); got != c.want {
			t.Errorf("visibleWidth(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// The selection band must span the full row: lipgloss ends each styled segment
// with a reset, which also clears the background, so the band is re-asserted
// after every reset and the row is padded out to width.
func TestHighlightRow(t *testing.T) {
	row := "ab" + ansiReset + "cd"
	got := highlightRow(row, 10)

	if !strings.HasPrefix(got, selBg) {
		t.Errorf("band not opened: %q", got)
	}
	if !strings.Contains(got, ansiReset+selBg) {
		t.Errorf("band not re-asserted after the inner reset: %q", got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Errorf("band not closed: %q", got)
	}
	if w := visibleWidth(got); w != 10 {
		t.Errorf("padded width = %d, want 10", w)
	}
	if w := visibleWidth(highlightRow("abcdefghijkl", 4)); w != 12 {
		t.Errorf("over-wide row = %d, want it left intact at 12", w)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exact", 5, "exact"},
		{"truncated", 5, "trun…"},
		{"日本語です", 3, "日本…"}, // cuts on runes, not bytes
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
		if got := truncate(c.in, c.n); len([]rune(got)) > c.n {
			t.Errorf("truncate(%q, %d) = %q, longer than the budget", c.in, c.n, got)
		}
	}
}

// entryWidth is the one budget all four views share (Spaces, Trash, PRs,
// Worktrees) — the reason a title cuts at the same place wherever you see it.
func TestEntryWidth(t *testing.T) {
	cases := []struct{ avail, want int }{
		{200, maxEntryWidth},
		{maxEntryWidth + 1, maxEntryWidth},
		{maxEntryWidth, maxEntryWidth},
		{50, 50},
		{minEntryWidth, minEntryWidth},
		{0, minEntryWidth},
		{-20, minEntryWidth}, // a trailer wider than the row must not go negative
	}
	for _, c := range cases {
		if got := entryWidth(c.avail); got != c.want {
			t.Errorf("entryWidth(%d) = %d, want %d", c.avail, got, c.want)
		}
	}
	if minEntryWidth > maxEntryWidth {
		t.Fatal("entry range is inverted")
	}
}
