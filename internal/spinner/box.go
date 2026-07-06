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

	cols, rows, termOK := terminalSize(s.Writer)
	msgLen := utf8.RuneCountInString(s.Message)
	// Bash: box_w = msg_len + 6, min 32, max cols-2.
	boxWidth := msgLen + 6
	if boxWidth < 32 {
		boxWidth = 32
	}
	// Initialise stage tracking BEFORE the fallback branch — both the
	// box path and the inline fallback read s.status / s.stageStart
	// via currentLabel(), so an uninitialised stageStart (zero-time)
	// would surface as a nonsense elapsed suffix on the very first
	// tick of the fallback path.
	s.mu.Lock()
	if s.status == "" {
		s.status = s.Message
	}
	s.stageStart = time.Now()
	s.mu.Unlock()

	if !termOK || cols < boxWidth+4 || rows < 5 {
		// Fallback: inline carriage-return spinner. Passes currentLabel
		// as the live status source so stage updates via SetStatus stay
		// visible; italic-purple LabelStyle marks this as a transient
		// step (matches the tight spinner popup's border color 141).
		return (&Spinner{
			Message:    s.Message,
			Writer:     s.Writer,
			Frames:     s.Frames,
			Interval:   s.Interval,
			Status:     s.currentLabel,
			LabelStyle: "3;38;5;141",
		}).Run(fn)
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
	renderBoxFrame(s.Writer, startRow+1, startCol+1, boxWidth-2, s.Frames[0], s.currentLabel())
	for {
		select {
		case <-done:
			return fnErr
		case <-ticker.C:
			i++
			renderBoxFrame(s.Writer, startRow+1, startCol+1, boxWidth-2, s.Frames[i%len(s.Frames)], s.currentLabel())
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

	fmt.Fprintf(w, "\033[%d;%dH%s%s%s%s",
		row, col, dim, tl, strings.Repeat(h, width-2), tr+reset)
	for r := 1; r < height-1; r++ {
		fmt.Fprintf(w, "\033[%d;%dH%s%s%s\033[%d;%dH%s%s%s",
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
