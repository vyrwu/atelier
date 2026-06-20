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
	for {
		select {
		case <-done:
			fmt.Fprint(s.Writer, "\r\033[K")
			return fnErr
		case <-ticker.C:
			fmt.Fprintf(s.Writer, "\r%s %s", s.Frames[i%len(s.Frames)], s.Message)
			i++
		}
	}
}
