package ui

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// selBg is the subtle background band painted behind the selected row (a dark
// slate), and ansiReset is the SGR reset lipgloss emits after each styled
// segment.
const (
	selBg     = "\x1b[48;5;237m"
	ansiReset = "\x1b[0m"
)

// highlightRow paints the selection background across a whole row and pads it to
// width. Per-segment styling ends in a full reset (which also clears the
// background), so the band is re-asserted after every reset — otherwise it would
// only cover up to the first icon. Gives the selected entry visual continuity.
func highlightRow(s string, width int) string {
	s = selBg + strings.ReplaceAll(s, ansiReset, ansiReset+selBg)
	if pad := width - visibleWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s + ansiReset
}

// isNerdGlyph reports whether r is in a Nerd Font / private-use range. go-runewidth
// (and thus lipgloss.Width) reports these as zero-width, but the NerdFontMono
// glyphs render as one cell — so any right-alignment math must count them as 1.
func isNerdGlyph(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0xFFFFD)
}

// visibleWidth is the on-screen cell width of s: ANSI stripped, Nerd glyphs
// counted as one cell, everything else via go-runewidth. Use this — not
// lipgloss.Width — whenever the layout must line up columns that contain icons.
func visibleWidth(s string) int {
	s = ansiRe.ReplaceAllString(s, "")
	w := 0
	for _, r := range s {
		if isNerdGlyph(r) {
			w++
			continue
		}
		w += runewidth.RuneWidth(r)
	}
	return w
}
