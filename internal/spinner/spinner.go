// Package spinner renders a single-line inline spinner while a background
// task runs. Used by tools whose UX has an async Claude/network step
// (atelier-workspaces' Claude-naming flow, recap parsing).
package spinner

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// DefaultFrames is the braille spinner glyph set.
var DefaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// DefaultInterval is the spinner tick rate.
const DefaultInterval = 100 * time.Millisecond

type Spinner struct {
	Message  string
	Writer   io.Writer
	Frames   []string
	Interval time.Duration

	// Status, when set, is called on every tick and its return value is
	// rendered instead of the static Message — used by BoxSpinner's
	// fallback path so stage updates via SetStatus stay visible even
	// when the popup is too small for the boxed layout.
	Status func() string

	// LabelStyle, when non-empty, is an ANSI SGR sequence prepended
	// before the label (e.g. italic + purple) and closed with reset.
	// Empty → plain text. Kept opt-in so plain-terminal callers stay
	// unaffected.
	LabelStyle string
}

// New returns a Spinner that writes to stderr by default. Stderr is chosen
// because tmux popup -E captures stdout for the popup's content — writing
// stderr renders as inline status text.
func New(message string) *Spinner {
	return &Spinner{
		Message:  message,
		Writer:   os.Stderr,
		Frames:   DefaultFrames,
		Interval: DefaultInterval,
	}
}

// Run executes fn in a goroutine and renders a spinner until fn returns.
// The spinner is erased before Run returns. fn's error is propagated.
func (s *Spinner) Run(fn func() error) error {
	if s.Writer == nil {
		s.Writer = os.Stderr
	}
	if len(s.Frames) == 0 {
		s.Frames = DefaultFrames
	}
	if s.Interval == 0 {
		s.Interval = DefaultInterval
	}

	done := make(chan struct{})
	var (
		fnErr error
		once  sync.Once
	)
	go func() {
		fnErr = fn()
		once.Do(func() { close(done) })
	}()

	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	i := 0
	render := func(frame string) {
		label := s.Message
		if s.Status != nil {
			label = s.Status()
		}
		if s.LabelStyle != "" {
			fmt.Fprintf(s.Writer, "\r\033[K%s \033[%sm%s\033[0m",
				frame, s.LabelStyle, label)
		} else {
			fmt.Fprintf(s.Writer, "\r\033[K%s %s", frame, label)
		}
	}
	for {
		select {
		case <-done:
			fmt.Fprint(s.Writer, "\r\033[K")
			return fnErr
		case <-ticker.C:
			render(s.Frames[i%len(s.Frames)])
			i++
		}
	}
}
