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

func TestParsePopupParent_WorkspaceScoped(t *testing.T) {
	sid, wid, ok := parsePopupParent("_atelier_claude_5_3")
	if !ok || sid != "5" || wid != "3" {
		t.Fatalf("got (%q,%q,%v); want (5,3,true)", sid, wid, ok)
	}
}

func TestParsePopupParent_SessionGlobal(t *testing.T) {
	if _, _, ok := parsePopupParent("_atelier_k8s"); ok {
		t.Fatalf("session-global popups should not yield a parent")
	}
}

func TestParsePopupParent_NonAtelier(t *testing.T) {
	if _, _, ok := parsePopupParent("workspace-name"); ok {
		t.Fatalf("non-atelier sessions should not parse")
	}
}

// TestIsPopupSession_ExcludesAttentionFromPopupWindows locks in the
// rollup-filter fix for the double-attention bug: a single Claude Stop
// hook was producing count=2 because a legacy bash hook stamped
// @needs_attention on the popup-backing window (`@5` in session
// `_atelier_claude_2_2`) in addition to the real workspace window
// (`@2`). The rollup must skip anything sitting on a popup session.
func TestIsPopupSession_ExcludesAttentionFromPopupWindows(t *testing.T) {
	cases := map[string]bool{
		"_atelier_claude_2_2":          true,
		"_atelier_k8s":                 true,
		"_claudepop_3_4":               true,
		"_popup_1_2":                   true,
		"_k8spop_1_2":                  true,
		"_awspop_1_2":                  true,
		"_lazygitpop_1_2":              true,
		"vyrwu/atelier":                false,
		"wawafertility/infrastructure": false,
		"0":                            false,
		"":                             false,
	}
	for name, want := range cases {
		if got := isPopupSession(name); got != want {
			t.Errorf("isPopupSession(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDigitsOf(t *testing.T) {
	if got, want := digitsOf("$12"), "12"; got != want {
		t.Fatalf("digitsOf: got %q want %q", got, want)
	}
	if got, want := digitsOf("@7"), "7"; got != want {
		t.Fatalf("digitsOf: got %q want %q", got, want)
	}
}
