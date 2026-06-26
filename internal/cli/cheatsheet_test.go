package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderCheatsheet_HasBothSections locks in the consolidated layout
// the user explicitly requested: a single M-? popup carrying both the
// atelier keybindings cheatsheet AND the doctor-style diagnostics. If
// either section header disappears from the render (e.g. someone splits
// them back into separate commands), this test fails.
func TestRenderCheatsheet_HasBothSections(t *testing.T) {
	var buf bytes.Buffer
	renderCheatsheet(&buf)
	out := stripANSI(buf.String())
	for _, header := range []string{"Keybindings", "Diagnostics"} {
		if !strings.Contains(out, header) {
			t.Errorf("cheatsheet output missing %q section. full output:\n%s", header, out)
		}
	}
}

// TestRenderCheatsheet_IncludesAtelierMQuestion ensures the cheatsheet
// row for itself ("M-?") is always present — it's emitted by atelier
// core, not by a plugin manifest, and used to be easy to lose.
func TestRenderCheatsheet_IncludesAtelierMQuestion(t *testing.T) {
	var buf bytes.Buffer
	renderCheatsheet(&buf)
	out := stripANSI(buf.String())
	if !strings.Contains(out, "M-?") {
		t.Errorf("cheatsheet missing M-? row. full output:\n%s", out)
	}
}

// TestRenderCheatsheet_NoMcCleanupAdvert locks the decommission of
// the OLD M-c "clean orphaned popups" affordance. Cleanup is fully
// automatic now (window-unlinked + session-closed hooks + the
// startup sweep wired in RestoreBlock), so advertising a manual
// shortcut for it gives the user a chore for a problem they don't
// have.
//
// (Note: M-c was later reused by the k8s tool as the "switch K9s
// context" chord — that's a legit different binding and is allowed
// to appear. The bans below target the cleanup text only.)
func TestRenderCheatsheet_NoMcCleanupAdvert(t *testing.T) {
	var buf bytes.Buffer
	renderCheatsheet(&buf)
	out := stripANSI(buf.String())
	for _, banned := range []string{"clean orphaned", "clean now"} {
		if strings.Contains(out, banned) {
			t.Errorf("cheatsheet still advertises decommissioned cleanup affordance (%q). full output:\n%s",
				banned, out)
		}
	}
}

// stripANSI removes ANSI escape sequences so assertions don't depend on
// terminal styling.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j - 1
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}
