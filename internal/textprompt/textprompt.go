package textprompt

import (
	"errors"
	"os"

	"golang.org/x/term"
)

// ErrCancelled is returned when the user dismisses the prompt (Esc / Ctrl-C).
var ErrCancelled = errors.New("textprompt: cancelled")

// Options configure a prompt.
type Options struct {
	Title  string // e.g. "What are we doing today?"
	Prompt string // the leading glyph, e.g. "> "
	Footer string // hint line, e.g. "Enter submit · Esc cancel"
	Accent string // 256-color index for the title/prompt (default green "35")
}

// Read renders a rectangular free-text input field on the controlling terminal
// and returns the typed text once the user presses Enter. Returns ErrCancelled
// on Esc / Ctrl-C. This is a TEXT INPUT, not a picker — the field accepts a
// free-form paragraph (soft-wrapped), with basic line editing.
//
// It reads/writes the controlling tty (/dev/tty), so it works even when stdin/
// stdout are redirected (as in a tmux popup pipeline). If no tty is available
// (tests, non-interactive), it returns ErrCancelled.
func Read(opts Options) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", ErrCancelled
	}
	defer func() { _ = tty.Close() }()

	fd := int(tty.Fd())
	if !term.IsTerminal(fd) {
		return "", ErrCancelled
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return "", ErrCancelled
	}
	defer func() { _ = term.Restore(fd, old) }()

	if opts.Prompt == "" {
		opts.Prompt = "> "
	}
	if opts.Accent == "" {
		opts.Accent = "35"
	}

	ed := &editor{}
	render(tty, fd, ed, opts)

	var esc []byte
	buf := make([]byte, 1)
	for {
		n, err := tty.Read(buf)
		if err != nil || n == 0 {
			return "", ErrCancelled
		}
		b := buf[0]

		// --- escape-sequence handling (arrows / home / end / lone Esc) ---
		if len(esc) > 0 {
			esc = append(esc, b)
			if act, done := decodeEsc(esc); done {
				if act == actCancel {
					restoreCursor(tty)
					return "", ErrCancelled
				}
				applyEsc(ed, act)
				esc = nil
				render(tty, fd, ed, opts)
			}
			// Guard against a runaway sequence.
			if len(esc) > 8 {
				esc = nil
			}
			continue
		}

		switch b {
		case '\r', '\n': // Enter → submit
			restoreCursor(tty)
			return ed.trimmed(), nil
		case 0x1b: // Esc — could start an escape sequence or be a bare cancel
			esc = []byte{b}
			// A bare Esc with nothing following is a cancel; the loop peeks the
			// next byte on the following Read. If it's not '[' or 'O',
			// decodeEsc returns a cancel action.
			continue
		case 0x03: // Ctrl-C → cancel
			restoreCursor(tty)
			return "", ErrCancelled
		case 0x7f, 0x08: // Backspace / Ctrl-H
			ed.backspace()
		case 0x17: // Ctrl-W → delete word
			ed.deleteWord()
		case 0x15: // Ctrl-U → clear
			ed.clear()
		case 0x01: // Ctrl-A → home
			ed.home()
		case 0x05: // Ctrl-E → end
			ed.end()
		default:
			if b >= 0x20 && b != 0x7f {
				// Decode a UTF-8 rune (read continuation bytes as needed).
				r, err := readRune(tty, b)
				if err != nil {
					return "", ErrCancelled
				}
				ed.insert(r)
			}
		}
		render(tty, fd, ed, opts)
	}
}

// decodeEsc interprets a pending escape sequence. Returns (action, done). While
// the sequence is incomplete it returns done=false. A bare Esc (second byte is
// not '[' or 'O') decodes to actCancel.
func decodeEsc(esc []byte) (escAction, bool) {
	if len(esc) < 2 {
		return actNone, false
	}
	if esc[1] != '[' && esc[1] != 'O' {
		return actCancel, true // lone Esc → cancel
	}
	if len(esc) < 3 {
		return actNone, false
	}
	switch esc[2] {
	case 'D':
		return actLeft, true
	case 'C':
		return actRight, true
	case 'H':
		return actHome, true
	case 'F':
		return actEnd, true
	case '3': // Delete is ESC [ 3 ~
		if len(esc) < 4 {
			return actNone, false
		}
		return actDelete, true
	case 'A', 'B': // Up/Down — no-op in a single-field prompt
		return actNoop, true
	default:
		return actNoop, true
	}
}

type escAction int

const (
	actNone escAction = iota
	actNoop
	actLeft
	actRight
	actHome
	actEnd
	actDelete
	actCancel
)

// applyEsc applies a cursor-movement escape action to the editor. Cancel is
// handled by the caller (Read), not here.
func applyEsc(ed *editor, a escAction) {
	switch a {
	case actLeft:
		ed.left()
	case actRight:
		ed.right()
	case actHome:
		ed.home()
	case actEnd:
		ed.end()
	case actDelete:
		ed.deleteFwd()
	}
}

// readRune completes a UTF-8 rune given its first byte, reading continuation
// bytes from the tty. ASCII fast-path when b < 0x80.
func readRune(tty *os.File, b byte) (rune, error) {
	if b < 0x80 {
		return rune(b), nil
	}
	var n int
	switch {
	case b&0xE0 == 0xC0:
		n = 1
	case b&0xF0 == 0xE0:
		n = 2
	case b&0xF8 == 0xF0:
		n = 3
	default:
		return rune(b), nil
	}
	buf := make([]byte, n+1)
	buf[0] = b
	for i := 1; i <= n; i++ {
		one := make([]byte, 1)
		if _, err := tty.Read(one); err != nil {
			return 0, err
		}
		buf[i] = one[0]
	}
	for _, r := range string(buf) {
		return r, nil
	}
	return rune(b), nil
}
