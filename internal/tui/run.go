// Package tui is atelier's bubbletea substrate: the popup run-harness,
// the lipgloss theme (ported from internal/fzfstyle), and the shared
// list models that replace atelier's fzf pickers. Every atelier-rendered
// picker that runs inside a tmux popup goes through Run here so the TTY
// wiring, cancel/exit-code contract, and color profile are handled once.
//
// See issue #86 for the migration rationale (native in-place updates,
// one aesthetic, no external fzf binary for atelier-owned surfaces).
package tui

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// Outcome mirrors internal/fzf.Result so tool code that used to consume a
// fzf selection can consume a bubbletea one with minimal change.
//
//	Key       — an action token the model reports instead of a plain
//	            accept (e.g. a cross-jump sentinel); "" for Enter/accept.
//	Selection — the chosen item's identity string.
//	Query     — the typed filter text, when a model exposes it.
type Outcome struct {
	Key       string
	Selection string
	Query     string
}

// Model is a bubbletea model that a picker exposes to Run. Cancelled
// reports an Esc/Ctrl-C dismissal (→ ErrCancelled → exit 130 so a
// become() chain unwinds); Outcome carries the accept/action result.
type Model interface {
	tea.Model
	Outcome() Outcome
	Cancelled() bool
}

// Run drives a picker model inside the tmux popup and translates the
// final model into an Outcome, mapping any cancellation to
// ErrCancelled. It forces truecolor and binds I/O to the popup TTY;
// see the inline notes for why each is load-bearing.
func Run(m Model, extra ...tea.ProgramOption) (Outcome, error) {
	forceStaticColorProfile()

	// Render in the alternate screen. The popup is a full surface the picker
	// owns, and altscreen gives it a clean, fixed-size buffer: no scroll drift
	// when a live tick re-renders (the inline renderer would march content off
	// the top of the popup over successive ticks), and no leftover frame from a
	// prior surface. bubbletea exits altscreen + restores the terminal before
	// Run returns, so a post-Run ExecReplace cross-jump still hands off cleanly.
	opts, ttyFile := ttyOptions()
	if ttyFile != nil {
		// Close our /dev/tty fd when Run returns. Terminal outcomes
		// (ExecReplace / process exit) would drop it via O_CLOEXEC anyway,
		// but the M-s picker loop reopens Run/Loader without exec'ing, so an
		// unclosed fd would accumulate toward pty exhaustion.
		defer func() { _ = ttyFile.Close() }()
	}
	opts = append(opts, tea.WithAltScreen())
	program := tea.NewProgram(m, append(opts, extra...)...)
	final, err := program.Run()
	if err != nil {
		// A SIGINT delivered to the program (rather than a Ctrl-C key we
		// handled in Update) surfaces as ErrProgramKilled — same intent as
		// a cancel, so unwind the chain identically.
		if errors.Is(err, tea.ErrProgramKilled) {
			return Outcome{}, ErrCancelled
		}
		return Outcome{}, err
	}
	fm, ok := final.(Model)
	if !ok {
		return Outcome{}, fmt.Errorf("tui: final model %T is not a tui.Model", final)
	}
	if fm.Cancelled() {
		return Outcome{}, ErrCancelled
	}
	return fm.Outcome(), nil
}

// ttyOptions binds bubbletea's input AND output to /dev/tty when it is a
// terminal, and returns the opened file so the caller can close it. bubbletea
// re-opens /dev/tty for input on its own when stdin isn't a TTY, but NOT for
// output — and an atelier picker can be reached with a captured stdout (a
// pipe), which would make bubbletea render into the capture instead of the
// popup. Returns (nil, nil) when /dev/tty isn't usable, so a piped test
// harness still runs on stdio. The caller MUST Close the returned file.
func ttyOptions() ([]tea.ProgramOption, *os.File) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil
	}
	if !term.IsTerminal(int(f.Fd())) {
		_ = f.Close()
		return nil, nil
	}
	return []tea.ProgramOption{tea.WithInput(f), tea.WithOutput(f)}, f
}

// forceStaticColorProfile pins lipgloss's color profile AND background so the
// renderer never does a terminal round-trip (OSC 10/11 background query,
// profile detection). Two reasons: (1) inside the popup TERM=tmux-256color
// would otherwise downsample the dracula hex — atelier's tmux config enables
// truecolor passthrough (Tc), so force 24-bit; (2) a tmux `display-popup -E`
// child often does NOT get a reply to the OSC 11 background query, so termenv
// BLOCKS waiting for it and the picker renders blank until the read times out
// (the "can't see any workspaces" symptom). Pinning both makes rendering
// fully static — no query, no block. Dracula is dark.
func forceStaticColorProfile() {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
}

// ExecReplace replaces the current process image with `atelier <args...>`
// via syscall.Exec — the become() equivalent for picker→picker jumps. Safe
// to call after Run returns: bubbletea has already restored the terminal
// (exited altscreen, shown cursor, dropped raw mode) by then, so the popup
// pty hands cleanly to the replacement. Mirrors toolselector.execReplace.
func ExecReplace(args ...string) error {
	self, err := os.Executable()
	if err != nil || self == "" {
		self = "atelier"
	}
	argv := append([]string{self}, args...)
	return syscall.Exec(self, argv, os.Environ())
}
