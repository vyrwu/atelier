package spinner

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// BoxSpinner renders a centered bordered box with a spinner + message
// while a background task runs. Matches the bash `tmux_with_loader` look.
//
// Falls back to inline spinner if the terminal is too small or stdout is
// not a TTY.
//
// Multi-stage progress: callers can update the displayed label mid-run
// via SetStatus. Each call resets the per-stage elapsed timer; once a
// stage has been running for stageElapsedThreshold the renderer prepends
// the elapsed seconds (`Asking Claude (12s)...`) so the user can
// distinguish "stuck" from "slow".
type BoxSpinner struct {
	Message  string
	Frames   []string
	Interval time.Duration
	Writer   io.Writer

	// Delay, when > 0, suppresses ALL rendering until the task has been
	// running for at least this long. If the task finishes first, nothing
	// is drawn — this avoids flashing a full-screen clear + box for an
	// operation that turns out to be fast. Zero (default) renders
	// immediately, so existing callers are unaffected.
	Delay time.Duration

	mu         sync.Mutex
	status     string    // current stage label; empty → use Message
	stageStart time.Time // start of current stage; reset by SetStatus
}

// SetStatus updates the stage label shown next to the spinner glyph.
// Safe to call from the goroutine passed to Run; the next tick renders
// the new label. Resets the per-stage elapsed timer.
func (s *BoxSpinner) SetStatus(label string) {
	s.mu.Lock()
	s.status = label
	s.stageStart = time.Now()
	s.mu.Unlock()
}

// stageElapsedThreshold: stages running longer than this get an inline
// `(Xs)` suffix so the user can tell stuck from slow. Per FR-2.1.
const stageElapsedThreshold = 10 * time.Second

// formatStageLabel renders the current label, prepending elapsed seconds
// when the stage has been running long enough to warrant the hint.
// Pure helper — exported for testing.
func formatStageLabel(label string, elapsed time.Duration) string {
	if elapsed < stageElapsedThreshold {
		return label
	}
	trimmed := strings.TrimRight(label, ".")
	return fmt.Sprintf("%s (%ds)...", trimmed, int(elapsed.Seconds()))
}

func NewBox(message string) *BoxSpinner {
	w := io.Writer(os.Stderr)
	// Prefer /dev/tty so we render even when stdout/stderr are captured
	// (e.g. when invoked under fzf become() inside a $()). Matches bash
	// tmux_with_loader's `exec >/dev/tty`. Only use it when it is an
	// actual terminal — opening /dev/tty in a subprocess without a
	// controlling tty succeeds but blocks on write.
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		if term.IsTerminal(int(f.Fd())) {
			w = f
		} else {
			_ = f.Close()
		}
	}
	return &BoxSpinner{
		Message:  message,
		Frames:   DefaultFrames,
		Interval: DefaultInterval,
		Writer:   w,
	}
}

func (s *BoxSpinner) Run(fn func() error) error {
	if s.Writer == nil {
		s.Writer = os.Stderr
	}
	if len(s.Frames) == 0 {
		s.Frames = DefaultFrames
	}
	if s.Interval == 0 {
		s.Interval = DefaultInterval
	}

	// Initialise stage tracking BEFORE any render path — both the box
	// path and the inline fallback read s.status / s.stageStart via
	// currentLabel(), so an uninitialised stageStart (zero-time) would
	// surface as a nonsense elapsed suffix on the very first tick.
	s.mu.Lock()
	if s.status == "" {
		s.status = s.Message
	}
	s.stageStart = time.Now()
	s.mu.Unlock()

	// Run the task in the background; done closes when it returns. A
	// single goroutine drives the delay gate AND both render loops, so
	// the task is never started twice.
	done := make(chan struct{})
	var (
		fnErr error
		once  sync.Once
	)
	go func() {
		fnErr = fn()
		once.Do(func() { close(done) })
	}()

	// Delay gate: render nothing when the task finishes quickly, so a
	// fast operation doesn't flash a full-screen clear + box.
	if s.Delay > 0 {
		select {
		case <-done:
			return fnErr
		case <-time.After(s.Delay):
		}
	}

	cols, rows, termOK := terminalSize(s.Writer)
	msgLen := utf8.RuneCountInString(s.Message)
	// Bash: box_w = msg_len + 6, min 32, max cols-2.
	boxWidth := msgLen + 6
	if boxWidth < 32 {
		boxWidth = 32
	}

	if !termOK || cols < boxWidth+4 || rows < 5 {
		// Fallback: inline carriage-return spinner. currentLabel keeps
		// stage updates via SetStatus visible; italic-purple marks this
		// as a transient step (matches the tight spinner popup's border
		// color 141).
		s.spin(done, func(frame string) {
			fmt.Fprintf(s.Writer, "\r\033[K%s \033[3;38;5;141m%s\033[0m",
				frame, s.currentLabel())
		}, func() {
			fmt.Fprint(s.Writer, "\r\033[K")
		})
		return fnErr
	}
	if boxWidth > cols-4 {
		boxWidth = cols - 4
	}
	const boxHeight = 3
	startRow := (rows-boxHeight)/2 + 1
	startCol := (cols-boxWidth)/2 + 1

	// Bash tmux_with_loader: full screen clear + hide cursor, restore on exit.
	fmt.Fprint(s.Writer, "\033[2J\033[?25l\033[s")
	defer func() {
		eraseBox(s.Writer, startRow, startCol, boxWidth, boxHeight)
		fmt.Fprint(s.Writer, "\033[u\033[?25h")
	}()

	drawBox(s.Writer, startRow, startCol, boxWidth, boxHeight)
	s.spin(done, func(frame string) {
		renderBoxFrame(s.Writer, startRow+1, startCol+1, boxWidth-2, frame, s.currentLabel())
	}, nil)
	return fnErr
}

// spin renders the first frame immediately, then a new frame on every
// interval tick until done closes, at which point cleanup (if non-nil)
// runs. Shared by the box and inline-fallback paths so both consume the
// single task goroutine started in Run.
func (s *BoxSpinner) spin(done <-chan struct{}, render func(frame string), cleanup func()) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	render(s.Frames[0])
	i := 0
	for {
		select {
		case <-done:
			if cleanup != nil {
				cleanup()
			}
			return
		case <-ticker.C:
			i++
			render(s.Frames[i%len(s.Frames)])
		}
	}
}

// currentLabel returns the label for the current tick — the active stage
// status with elapsed-seconds suffix when applicable.
func (s *BoxSpinner) currentLabel() string {
	s.mu.Lock()
	label := s.status
	elapsed := time.Since(s.stageStart)
	s.mu.Unlock()
	return formatStageLabel(label, elapsed)
}

func drawBox(w io.Writer, row, col, width, height int) {
	const (
		tl = "╭"
		tr = "╮"
		bl = "╰"
		br = "╯"
		h  = "─"
		v  = "│"
	)
	// Border is rendered in a dim gray (colour 240) — close to bash's gray.
	const dim = "\033[38;5;240m"
	const reset = "\033[0m"

	_, _ = fmt.Fprintf(w, "\033[%d;%dH%s%s%s%s",
		row, col, dim, tl, strings.Repeat(h, width-2), tr+reset)
	for r := 1; r < height-1; r++ {
		_, _ = fmt.Fprintf(w, "\033[%d;%dH%s%s%s\033[%d;%dH%s%s%s",
			row+r, col, dim, v, reset,
			row+r, col+width-1, dim, v, reset)
	}
	fmt.Fprintf(w, "\033[%d;%dH%s%s%s%s",
		row+height-1, col, dim, bl, strings.Repeat(h, width-2), br+reset)
}

func eraseBox(w io.Writer, row, col, width, height int) {
	blank := strings.Repeat(" ", width)
	for r := 0; r < height; r++ {
		fmt.Fprintf(w, "\033[%d;%dH%s", row+r, col, blank)
	}
}

func renderBoxFrame(w io.Writer, row, col, width int, frame, message string) {
	const yellow = "\033[38;5;221m"
	const reset = "\033[0m"

	// Compose: " ⠋ message…" left-padded inside the box.
	content := fmt.Sprintf(" %s%s%s %s", yellow, frame, reset, message)
	// Truncate raw content if too long; account for ANSI escapes by counting only runes outside escapes.
	visibleLen := 1 + utf8.RuneCountInString(frame) + 1 + utf8.RuneCountInString(message)
	if visibleLen > width {
		// truncate the message portion
		maxMsg := width - (1 + utf8.RuneCountInString(frame) + 1 + 1) // ... ellipsis
		if maxMsg < 1 {
			maxMsg = 1
		}
		runes := []rune(message)
		if maxMsg < len(runes) {
			content = fmt.Sprintf(" %s%s%s %s…", yellow, frame, reset, string(runes[:maxMsg]))
			visibleLen = width
		}
	}
	pad := width - visibleLen
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(w, "\033[%d;%dH%s%s", row, col, content, strings.Repeat(" ", pad))
}

func terminalSize(w io.Writer) (cols, rows int, ok bool) {
	f, isFile := w.(*os.File)
	if !isFile {
		f = os.Stderr
	}
	c, r, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0, false
	}
	return c, r, true
}
