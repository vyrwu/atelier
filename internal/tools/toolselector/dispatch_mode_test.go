package toolselector

import (
	"testing"

	"github.com/vyrwu/atelier/internal/manifest"
)

// TestDispatchMode locks in the geometry-aware routing introduced for the
// "M-; → Select Workspace overlays / M-; → Claude detaches origin" bug.
// Picker-style targets must exec in place (so an underlying tool popup
// survives); full-style targets must go through OpenOnOuter (so the
// target gets its proper geometry).
func TestDispatchMode(t *testing.T) {
	cases := []struct {
		name  string
		style manifest.Style
		want  DispatchMode
	}{
		{"picker -> exec in place", manifest.StylePicker, dispatchExecInPlace},
		{"full   -> open on outer", manifest.StyleFull, dispatchOpenOnOuter},
		{"empty  -> open on outer (conservative fallback)", manifest.Style(""), dispatchOpenOnOuter},
		{"unknown-> open on outer (conservative fallback)", manifest.Style("weird"), dispatchOpenOnOuter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dispatchMode(tc.style); got != tc.want {
				t.Errorf("dispatchMode(%q) = %v, want %v", tc.style, got, tc.want)
			}
		})
	}
}
