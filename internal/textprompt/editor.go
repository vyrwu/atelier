// Package textprompt is a small terminal free-text input field — a rectangular
// prompt box for entering a paragraph of text, not a fuzzy picker. It backs the
// M-n "what are we doing today?" workspace-intent input, which is text entry,
// not selection (fzf would be the wrong tool). The editor state machine is pure
// and unit-tested; the raw-terminal I/O + rendering live in textprompt.go.
package textprompt

import "strings"

// editor is the pure text-buffer + cursor state. All key handling mutates this;
// the terminal layer only decodes bytes into these calls and renders the result,
// so the editing logic is unit-testable without a tty.
type editor struct {
	buf []rune // the text
	cur int    // cursor index in [0, len(buf)]
}

func (e *editor) String() string { return string(e.buf) }

// insert adds a rune at the cursor and advances it.
func (e *editor) insert(r rune) {
	e.buf = append(e.buf, 0)
	copy(e.buf[e.cur+1:], e.buf[e.cur:])
	e.buf[e.cur] = r
	e.cur++
}

// backspace deletes the rune before the cursor.
func (e *editor) backspace() {
	if e.cur == 0 {
		return
	}
	e.buf = append(e.buf[:e.cur-1], e.buf[e.cur:]...)
	e.cur--
}

// deleteFwd deletes the rune at the cursor (Delete key).
func (e *editor) deleteFwd() {
	if e.cur >= len(e.buf) {
		return
	}
	e.buf = append(e.buf[:e.cur], e.buf[e.cur+1:]...)
}

// deleteWord deletes the word before the cursor (Ctrl-W): trailing spaces then
// the run of non-spaces.
func (e *editor) deleteWord() {
	i := e.cur
	for i > 0 && e.buf[i-1] == ' ' {
		i--
	}
	for i > 0 && e.buf[i-1] != ' ' {
		i--
	}
	e.buf = append(e.buf[:i], e.buf[e.cur:]...)
	e.cur = i
}

// clear empties the buffer (Ctrl-U).
func (e *editor) clear() {
	e.buf = e.buf[:0]
	e.cur = 0
}

func (e *editor) left() {
	if e.cur > 0 {
		e.cur--
	}
}

func (e *editor) right() {
	if e.cur < len(e.buf) {
		e.cur++
	}
}

func (e *editor) home() { e.cur = 0 }
func (e *editor) end()  { e.cur = len(e.buf) }

// trimmed returns the buffer with surrounding whitespace removed — the value a
// submit yields.
func (e *editor) trimmed() string { return strings.TrimSpace(string(e.buf)) }
