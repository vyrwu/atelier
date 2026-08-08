package initgen

import (
	"strings"
	"testing"
)

// TestRender_RefreshLoopWatchdog guards the daemon's crash recovery: the
// rendered config must both launch the loop once AND wire a client-attached
// watchdog that re-runs the guarded launch, so a daemon that died mid-session
// respawns on the next attach (the singleton lock makes the re-launch a no-op
// while it's alive). Both modes emit it — the observer isn't a theme feature.
func TestRender_RefreshLoopWatchdog(t *testing.T) {
	for _, theme := range []bool{false, true} {
		var buf strings.Builder
		if _, err := Render(&buf, RenderOptions{IncludeTheme: theme}); err != nil {
			t.Fatalf("Render(IncludeTheme=%v): %v", theme, err)
		}
		out := buf.String()
		if !strings.Contains(out, "run-shell -b 'atelier tools workspaces _refresh-loop'") {
			t.Errorf("IncludeTheme=%v: missing initial daemon launch", theme)
		}
		if !strings.Contains(out, `set-hook -g client-attached 'run-shell -b "atelier tools workspaces _refresh-loop"'`) {
			t.Errorf("IncludeTheme=%v: missing client-attached watchdog re-launch", theme)
		}
	}
}

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
