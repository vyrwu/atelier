package workspaces

import (
	"regexp"
	"strings"
	"testing"
)

// TestRecapIndentCells locks the indent math: the recap line sits under the
// workspace name, so its indent equals the visible span of the fixed left
// columns — time + icon, plus the forge-badge slot only when a forge
// integration is active.
func TestRecapIndentCells(t *testing.T) {
	if got := recapIndentCells(false); got != timeColCells+iconColCells {
		t.Errorf("no-forge indent = %d, want %d", got, timeColCells+iconColCells)
	}
	if got := recapIndentCells(true); got != timeColCells+iconColCells+badgeColCells {
		t.Errorf("forge indent = %d, want %d", got, timeColCells+iconColCells+badgeColCells)
	}
}

// TestFormatRecapLine covers the own-line recap: empty recap reserves a blank
// second line so every row is a uniform two-line height (#43); a present recap
// starts a new line, indented so the `· ` marker sits under the name column, in
// italic dim-grey.
func TestFormatRecapLine(t *testing.T) {
	if got, want := formatRecapLine("", 8), "\n        "+zeroWidthSpace; got != want {
		t.Fatalf("empty recap must reserve a blank second line, got %q want %q", got, want)
	}

	got := formatRecapLine("did the thing", 8)
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("recap line must begin a new line, got %q", got)
	}
	visible := regexp.MustCompile(`\033\[[0-9;]*m`).ReplaceAllString(got, "")
	if visible != "\n        · did the thing" {
		t.Fatalf("visible recap line = %q", visible)
	}
	if !strings.Contains(got, "\033[3;38;5;103m") {
		t.Fatalf("recap must be italic dim-grey, got %q", got)
	}
}

// TestFormatRecapLine_EmptyReservesRenderableLine locks the invariant the #43
// fix depends on: the reserved (empty-recap) second line must NOT be
// whitespace-only, or fzf trims it under --ansi and collapses the row back to a
// single line — bringing the height oscillation straight back. unicode.IsSpace
// (used by TrimSpace) mirrors fzf's trimmer here: it treats a non-breaking
// space as whitespace (fzf collapses it) but a zero-width space as not (fzf
// keeps it), so this guard also rejects a nbsp "fix".
func TestFormatRecapLine_EmptyReservesRenderableLine(t *testing.T) {
	got := formatRecapLine("", recapIndentCells(false))
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("empty recap must still start a second line, got %q", got)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatalf("reserved line is whitespace-only; fzf will trim it and collapse the row: %q", got)
	}
}
