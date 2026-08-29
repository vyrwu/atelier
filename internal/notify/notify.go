// Package notify renders a transient, auto-dismissing toast notification —
// atelier's user-facing "something happened" affordance for the tmux-popup
// tools (a create failure, a missing prerequisite). It's a small bubbletea
// program on the controlling tty, so it matches the look of the intent prompt
// (internal/textprompt) and the progress box (internal/spinner) rather than a
// bare tmux status-line flash.
package notify

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// Kind selects the toast's icon + accent color.
type Kind int

const (
	Info Kind = iota
	Success
	Error
)

// ttyForRender returns the controlling tty to render into, or (nil,false) when
// none is available (tests, non-interactive) so Show is a no-op. A package var
// so tests can force the no-op path.
var ttyForRender = func() (*os.File, bool) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, false
	}
	if !term.IsTerminal(int(f.Fd())) {
		_ = f.Close()
		return nil, false
	}
	return f, true
}

// Show renders a centered toast on the controlling tty and blocks until it is
// dismissed (any key) or times out. No tty (tests, non-interactive) → no-op,
// so callers can fire it unconditionally.
func Show(kind Kind, message string) {
	tty, ok := ttyForRender()
	if !ok {
		return
	}
	defer func() { _ = tty.Close() }()
	_, _ = tea.NewProgram(
		newToast(kind, message),
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithAltScreen(),
	).Run()
}
