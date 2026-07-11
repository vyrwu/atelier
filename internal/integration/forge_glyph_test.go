package integration

import "testing"

// TestForgeGlyph is the single source of truth for the forge badge's
// glyph + color: the picker (ANSI) and the status-line segment (tmux
// #[fg]) both render through it, so a drift here would desync them.
// Every renderable state must resolve to a non-empty glyph + color;
// ForgeNone and unknown states must report ok=false so callers omit
// the slot entirely.
func TestForgeGlyph(t *testing.T) {
	for _, st := range []ForgeState{ForgeOpen, ForgeDraft, ForgeMerged, ForgeClosed} {
		glyph, color, ok := ForgeGlyph(st)
		if !ok {
			t.Errorf("ForgeGlyph(%q): ok=false, want true", st)
		}
		if glyph == "" || color == "" {
			t.Errorf("ForgeGlyph(%q) = (%q, %q); want both non-empty", st, glyph, color)
		}
	}

	for _, st := range []ForgeState{ForgeNone, "", "bogus"} {
		if glyph, color, ok := ForgeGlyph(st); ok || glyph != "" || color != "" {
			t.Errorf("ForgeGlyph(%q) = (%q, %q, %v); want empty + ok=false", st, glyph, color, ok)
		}
	}

	// Colors must be distinct so states are visually distinguishable.
	seen := map[string]ForgeState{}
	for _, st := range []ForgeState{ForgeOpen, ForgeDraft, ForgeMerged, ForgeClosed} {
		_, color, _ := ForgeGlyph(st)
		if prev, dup := seen[color]; dup {
			t.Errorf("colour %s shared by %q and %q — states not distinguishable", color, prev, st)
		}
		seen[color] = st
	}
}
