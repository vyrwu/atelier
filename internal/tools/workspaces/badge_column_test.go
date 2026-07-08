package workspaces

import (
	"strings"
	"testing"
)

// visibleWidth counts display cells, ignoring ANSI SGR sequences. Nerd
// Font glyphs used by badges are single-cell, so each counts as 1.
func visibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b { // ESC — skip "\033[...m"
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		r := []rune(s[i:])[0]
		w++
		i += len(string(r))
	}
	return w
}

func TestFormatBadgeColumn(t *testing.T) {
	const openBadge = " \033[38;5;35m\033[0m" // ghpr "open": leading space + glyph

	tests := []struct {
		name   string
		values []string
	}{
		{"no providers", nil},
		{"single empty", []string{""}},
		{"single present", []string{openBadge}},
		{"two mixed", []string{openBadge, ""}},
		{"two present", []string{openBadge, openBadge}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBadgeColumn(tt.values)
			// Every provider contributes exactly a 2-cell slot, so the
			// column width is constant regardless of which badges are
			// set — this is what keeps the icon/name columns aligned.
			if w := visibleWidth(got); w != 2*len(tt.values) {
				t.Errorf("width = %d, want %d (got %q)", w, 2*len(tt.values), got)
			}
			// A present badge is glyph + trailing space, no leading
			// space (the leading space in the stored value is dropped so
			// the badge sits flush after the time column).
			if len(tt.values) == 1 && strings.TrimSpace(tt.values[0]) != "" {
				if strings.HasPrefix(got, " ") {
					t.Errorf("present badge should not keep a leading space: %q", got)
				}
				if !strings.HasSuffix(got, " ") {
					t.Errorf("present badge should have a trailing space: %q", got)
				}
			}
		})
	}
}
