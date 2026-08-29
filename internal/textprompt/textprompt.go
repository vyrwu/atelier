// Package textprompt renders atelier's single free-text INPUT — the M-n
// "what are we doing today?" intent box. It is text ENTRY, not a picker: a
// rectangular, soft-wrapping field, not a fuzzy list.
//
// The view is a bubbletea program wrapping bubbles/textarea (see model.go).
// Read runs it on the controlling tty so it works inside a tmux popup where
// stdin/stdout are part of a pipeline. Enter submits, Esc / Ctrl-C cancel.
package textprompt

import (
	"errors"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// ErrCancelled is returned when the user dismisses the prompt (Esc / Ctrl-C),
// or when no controlling terminal is available (tests, non-interactive).
var ErrCancelled = errors.New("textprompt: cancelled")

// Options configure the prompt. There is deliberately NO footer/legend field:
// the box carries no shortcut summary (by design — Enter submits, Esc cancels,
// both discoverable without a hint line).
type Options struct {
	Title       string // heading above the field, e.g. "栽 What are we doing today?"
	Placeholder string // ghost text shown while empty
	Accent      string // color for the heading (default green "35")
}

// Read renders the intent box on the controlling terminal and returns the
// typed text once the user presses Enter. Returns ErrCancelled on Esc /
// Ctrl-C, or when there is no usable tty.
//
// It reads/writes /dev/tty directly (not os.Stdin/os.Stdout) so it survives
// the redirected std streams of a tmux `display-popup -E` pipeline.
func Read(opts Options) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", ErrCancelled
	}
	defer func() { _ = tty.Close() }()
	if !term.IsTerminal(int(tty.Fd())) {
		return "", ErrCancelled
	}

	res, err := tea.NewProgram(
		newModel(opts),
		tea.WithInput(tty),
		tea.WithOutput(tty),
	).Run()
	if err != nil {
		return "", ErrCancelled
	}
	m, ok := res.(model)
	if !ok || m.cancelled {
		return "", ErrCancelled
	}
	return strings.TrimSpace(m.ta.Value()), nil
}
