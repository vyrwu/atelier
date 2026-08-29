package workspaces

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFormatAge(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		delta time.Duration
		want  string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{3 * 24 * time.Hour, "3d"},
	}
	for _, c := range cases {
		ts := strconv.FormatInt(now.Add(-c.delta).Unix(), 10)
		if got := formatAge(now, ts); got != c.want {
			t.Errorf("formatAge(-%s) = %q, want %q", c.delta, got, c.want)
		}
	}
	// Empty / zero / unparseable / future → "".
	for _, bad := range []string{"", "0", "notanumber", strconv.FormatInt(now.Add(time.Hour).Unix(), 10)} {
		if got := formatAge(now, bad); got != "" {
			t.Errorf("formatAge(%q) = %q, want empty", bad, got)
		}
	}
}

func TestFormatSummaryLine(t *testing.T) {
	empty := formatSummaryLine("", 6)
	if !strings.HasPrefix(empty, "\n") {
		t.Error("summary line should start with a newline (second row)")
	}
	if !strings.Contains(empty, zeroWidthSpace) {
		t.Error("empty summary must contain the zero-width space so the row reserves height")
	}
	full := formatSummaryLine("all good", 6)
	if !strings.Contains(full, "· all good") {
		t.Errorf("summary line = %q, want the · prefix", full)
	}
	if !strings.HasPrefix(full, "\n") {
		t.Error("summary line should start with a newline")
	}
}

func TestPadVisible(t *testing.T) {
	// "2PR " is 4 visible cells; pad to 5 → one trailing space added.
	got := padVisible("\033[38;5;35m2PR\033[0m ", "2PR ", 5)
	if !strings.HasSuffix(got, " ") {
		t.Errorf("padVisible should pad to width: %q", got)
	}
	// Already at/over width → unchanged.
	if got := padVisible("x", "12345", 3); got != "x" {
		t.Errorf("padVisible over-width should be unchanged, got %q", got)
	}
}

func TestSummaryIndentCells(t *testing.T) {
	if summaryIndentCells() != timeColCells+attnColCells+forgeColCells {
		t.Error("summaryIndentCells must equal the sum of the fixed column widths")
	}
}
