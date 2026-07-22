package initgen

import (
	"strings"
	"testing"
)

// TestRender_BareModeKeepsPopupCopyMode is the end-to-end regression
// guard for the actual bug: `atelier init --bare` (IncludeTheme false)
// strips the popup prefix via the engine (popup.ApplyStyle sets prefix
// None) but must STILL emit a copy-mode entry so popups aren't left
// unscrollable. Before the fix, C-] lived in ThemeBlock and bare mode
// dropped it — popup copy-mode was dead. Both modes must emit it.
func TestRender_BareModeKeepsPopupCopyMode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		theme bool
	}{
		{"bare", false},
		{"theme", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if _, err := Render(&buf, RenderOptions{IncludeTheme: tc.theme}); err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(buf.String(), "bind -T popup C-] copy-mode") {
				t.Errorf("Render(IncludeTheme=%v) missing popup copy-mode binding; popups would be unscrollable", tc.theme)
			}
		})
	}
}
