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

// TestFormatRecapLine covers the own-line recap: empty recap keeps the row
// single-line; a present recap starts a new line, indented so the `· ` marker
// sits under the name column, in italic dim-grey.
func TestFormatRecapLine(t *testing.T) {
	if got := formatRecapLine("", 8); got != "" {
		t.Fatalf("empty recap must yield no second line, got %q", got)
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
