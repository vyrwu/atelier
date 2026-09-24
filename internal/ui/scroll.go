package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The body's fixed chrome above the rows: the title line, the filter line, and
// the blank line between them and the list.
const listChrome = 3

// listHeight is how many row lines the current screen can show. The frame is a
// fixed size (see View) and nothing inside it truncates: a body taller than its
// box pushes the frame past the popup, and Bubble Tea drops the top lines — the
// border and title first. So this counts what View will actually draw.
func (m Model) listHeight() int {
	inner := m.h - 2 // the frame's border
	if footer := m.screenFooter(); footer != "" {
		// The footer as View draws it — wrapped to the content width — and the
		// rule above it.
		inner -= lipgloss.Height(lipgloss.NewStyle().Width(m.w-4).Render(footer)) + 1
	}
	// The header lines, and the empty line the list's last newline leaves.
	h := inner - listChrome - 1
	// A note or an error under the list is a blank line and then itself.
	if m.err != "" {
		h -= 2
	}
	if m.note != "" && m.screen == scrPRs {
		h -= 2
	}
	if h < 1 {
		return 1
	}
	return h
}

// window returns the slice of lines to draw so that the selected line stays
// visible, plus how many lines fell off each end. Selection drives the scroll —
// there is no separate scroll offset to keep in sync with it.
func window(lines []string, sel, height int) (visible []string, above, below int) {
	if height < 1 {
		height = 1
	}
	if len(lines) <= height {
		return lines, 0, 0
	}
	// Keep the selection in view, scrolling by the minimum needed.
	start := 0
	if sel >= height {
		start = sel - height + 1
	}
	if sel < start {
		start = sel
	}
	if max := len(lines) - height; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	return lines[start : start+height], start, len(lines) - start - height
}

// writeList renders lines into b as a scrolled window around sel, with a
// counter above or below when there is more in that direction — so the list
// never claims to be complete when it isn't. The counters take lines of their
// own: the selection always sits on an edge of the window once it scrolls, and
// a counter drawn over that edge would hide exactly the row being selected.
func writeList(b *strings.Builder, lines []string, sel, height int) {
	emit := func(s string) { b.WriteString(s + "\n") }
	if len(lines) <= height || height < 3 {
		visible, _, _ := window(lines, sel, height)
		for _, l := range visible {
			emit(l)
		}
		return
	}
	visible, above, below := window(lines, sel, height-1)
	if above > 0 && below > 0 {
		visible, above, below = window(lines, sel, height-2)
	}
	if above > 0 {
		emit(moreLine("↑", above))
	}
	for _, l := range visible {
		emit(l)
	}
	if below > 0 {
		emit(moreLine("↓", below))
	}
}

func moreLine(arrow string, n int) string {
	return faintStyle.Render(fmt.Sprintf("  %s %d more", arrow, n))
}
