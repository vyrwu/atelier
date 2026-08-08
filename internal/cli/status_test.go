package cli

import (
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

// TestFormatForgeIcon locks in the status-line forge segment: each
// renderable state produces its kernel-owned glyph wrapped in tmux
// #[fg=colourN] codes; ForgeNone/unknown/blank render nothing so the
// slot is simply absent. The expected glyph+color come from
// integration.ForgeGlyph so this test and the renderer can't drift.
func TestFormatForgeIcon(t *testing.T) {
	for _, st := range []integration.ForgeState{
		integration.ForgeOpen, integration.ForgeDraft,
		integration.ForgeMerged, integration.ForgeClosed,
	} {
		glyph, color, _ := integration.ForgeGlyph(st)
		want := " #[fg=colour" + color + "]" + glyph + "#[default]"
		if got := formatForgeIcon(string(st)); got != want {
			t.Errorf("formatForgeIcon(%q) = %q, want %q", st, got, want)
		}
	}
	for _, s := range []string{"", "   ", "bogus", string(integration.ForgeNone)} {
		if got := formatForgeIcon(s); got != "" {
			t.Errorf("formatForgeIcon(%q) = %q, want empty", s, got)
		}
	}
	// Surrounding whitespace is trimmed before the state lookup.
	if got := formatForgeIcon("  open  "); got == "" {
		t.Error(`formatForgeIcon("  open  ") should render (whitespace-trimmed)`)
	}
}

// Popup-name taxonomy (parent parsing, popup-session detection, digit
// extraction) moved to internal/state — see state/taxonomy_test.go for the
// coverage that formerly lived here as TestParsePopupParent_*,
// TestIsPopupSession_*, and TestDigitsOf.
