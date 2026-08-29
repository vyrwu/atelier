package textprompt

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// render clears the terminal and draws the prompt: the title, a blank line, the
// prompt glyph + the (soft-wrapped) text, and a footer hint — then positions the
// hardware cursor at the editing point. The tmux popup supplies the outer box;
// this draws the field inside it. Best-effort: rendering errors are ignored (the
// worst case is a cosmetically-off frame, never a hang).
func render(tty *os.File, fd int, ed *editor, opts Options) {
	w, _, _ := term.GetSize(fd)
	if w <= 0 {
		w = 80
	}
	// Leave a small margin so text doesn't kiss the popup border.
	const margin = 2
	textWidth := w - margin - len([]rune(opts.Prompt))
	if textWidth < 8 {
		textWidth = 8
	}

	var b strings.Builder
	b.WriteString("\033[2J\033[H") // clear + home
	// Title (accent, bold), then a blank line.
	if opts.Title != "" {
		fmt.Fprintf(&b, "\033[%d;%dH\033[1;38;5;%sm%s\033[0m", 2, margin+1, opts.Accent, opts.Title)
	}
	// Prompt + wrapped text starting on row 4.
	const startRow = 4
	promptCol := margin + 1
	fmt.Fprintf(&b, "\033[%d;%dH\033[38;5;%sm%s\033[0m", startRow, promptCol, opts.Accent, opts.Prompt)

	lines := wrapRunes(ed.buf, textWidth)
	indent := strings.Repeat(" ", margin+len([]rune(opts.Prompt)))
	for i, ln := range lines {
		if i == 0 {
			b.WriteString(string(ln))
		} else {
			fmt.Fprintf(&b, "\033[%d;1H%s%s", startRow+i, indent, string(ln))
		}
	}
	// Footer hint two rows below the text.
	if opts.Footer != "" {
		footRow := startRow + len(lines) + 2
		fmt.Fprintf(&b, "\033[%d;%dH\033[2;38;5;103m%s\033[0m", footRow, margin+1, opts.Footer)
	}

	// Position the hardware cursor at the edit point.
	crow, ccol := cursorRowCol(ed, textWidth)
	textStartCol := margin + len([]rune(opts.Prompt)) + 1
	if crow == 0 {
		fmt.Fprintf(&b, "\033[%d;%dH", startRow, textStartCol+ccol)
	} else {
		// Continuation rows are indented to align under the text.
		fmt.Fprintf(&b, "\033[%d;%dH", startRow+crow, textStartCol+ccol)
	}
	_, _ = tty.WriteString(b.String())
}

// wrapRunes soft-wraps a rune slice to width w, returning display lines. An
// empty buffer yields one empty line so the cursor has a row to sit on. Pure.
func wrapRunes(rs []rune, w int) [][]rune {
	if w < 1 {
		w = 1
	}
	if len(rs) == 0 {
		return [][]rune{{}}
	}
	var out [][]rune
	for i := 0; i < len(rs); i += w {
		end := i + w
		if end > len(rs) {
			end = len(rs)
		}
		out = append(out, rs[i:end])
	}
	return out
}

// cursorRowCol maps the cursor index to a (row, col) within the wrapped layout
// at width w. Row 0 is the first (prompt) line. Pure.
func cursorRowCol(ed *editor, w int) (row, col int) {
	if w < 1 {
		w = 1
	}
	return ed.cur / w, ed.cur % w
}

// restoreCursor un-hides / resets styling before returning control.
func restoreCursor(tty *os.File) {
	_, _ = tty.WriteString("\033[0m")
}
