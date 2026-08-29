// Package spinner renders a centered, bordered progress box — a bubbles
// spinner + a stage label — on the controlling tty while a background task
// runs. It's the shared "working…" affordance for the tmux-popup tools
// (workspace create / picker load). When there is no controlling tty (tests,
// non-interactive), Run just executes the task with no UI.
package spinner

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// ttyForRender returns the controlling tty to render into, or (nil,false) when
// none is available (tests, non-interactive) so Run takes the headless path.
// A package var so tests can force the headless path deterministically.
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

// BoxSpinner renders a centered bordered box with a bubbles spinner + message
// while a background task runs.
//
// Multi-stage progress: callers update the displayed label mid-run via
// SetStatus. Each call resets the per-stage elapsed timer; once a stage has
// been running for stageElapsedThreshold the label gains an elapsed-seconds
// suffix (`Asking Claude (12s)...`) so the user can tell "stuck" from "slow".
type BoxSpinner struct {
	Message string

	// Delay, when > 0, suppresses the spinner UI until the task has been
	// running at least this long — a fast task draws nothing, avoiding a
	// flash. Zero renders immediately. Only affects the tty render path.
	Delay time.Duration

	mu         sync.Mutex
	status     string    // current stage label; empty → use Message
	stageStart time.Time // start of current stage; reset by SetStatus
	prog       *tea.Program
}

func NewBox(message string) *BoxSpinner { return &BoxSpinner{Message: message} }

// SetStatus updates the stage label shown next to the spinner glyph. Safe to
// call from the goroutine passed to Run; the next frame renders the new label.
// Resets the per-stage elapsed timer.
func (s *BoxSpinner) SetStatus(label string) {
	now := time.Now()
	s.mu.Lock()
	s.status = label
	s.stageStart = now
	prog := s.prog
	s.mu.Unlock()
	if prog != nil {
		prog.Send(statusMsg{label: label, at: now})
	}
}

// stageElapsedThreshold: stages running longer than this get an inline `(Xs)`
// suffix so the user can tell stuck from slow.
const stageElapsedThreshold = 10 * time.Second

// formatStageLabel renders the current label, appending elapsed seconds when
// the stage has been running long enough to warrant the hint. Pure helper.
func formatStageLabel(label string, elapsed time.Duration) string {
	if elapsed < stageElapsedThreshold {
		return label
	}
	trimmed := strings.TrimRight(label, ".")
	return fmt.Sprintf("%s (%ds)...", trimmed, int(elapsed.Seconds()))
}

// Run executes fn in a goroutine and renders the spinner until it returns,
// propagating fn's error. Headless (no tty) just runs fn.
func (s *BoxSpinner) Run(fn func() error) error {
	now := time.Now()
	s.mu.Lock()
	if s.status == "" {
		s.status = s.Message
	}
	s.stageStart = now
	label := s.status
	s.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- fn() }()

	tty, ok := ttyForRender()
	if !ok {
		return <-done // headless: no UI
	}
	defer func() { _ = tty.Close() }()

	// Delay gate: a task that finishes before Delay draws nothing.
	if s.Delay > 0 {
		select {
		case err := <-done:
			return err
		case <-time.After(s.Delay):
		}
	}

	p := tea.NewProgram(newSpinModel(label, s.stageStartTime()),
		tea.WithInput(tty), tea.WithOutput(tty), tea.WithAltScreen())
	s.mu.Lock()
	s.prog = p
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		errCh <- <-done
		p.Send(doneMsg{})
	}()
	_, _ = p.Run()

	s.mu.Lock()
	s.prog = nil
	s.mu.Unlock()
	return <-errCh
}

func (s *BoxSpinner) stageStartTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stageStart
}

// --- bubbletea model ---

type doneMsg struct{}
type statusMsg struct {
	label string
	at    time.Time
}

type spinModel struct {
	sp            spinner.Model
	label         string
	stageStart    time.Time
	width, height int
}

func newSpinModel(label string, stageStart time.Time) spinModel {
	sp := spinner.New()
	// Braille frames at 10fps — the atelier spinner look, in yellow.
	sp.Spinner = spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    time.Second / 10,
	}
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("221"))
	return spinModel{sp: sp, label: label, stageStart: stageStart}
}

func (m spinModel) Init() tea.Cmd { return m.sp.Tick }

func (m spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg:
		return m, tea.Quit
	case statusMsg:
		m.label = msg.label
		m.stageStart = msg.at
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m spinModel) View() string {
	label := formatStageLabel(m.label, time.Since(m.stageStart))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 2).
		Render(m.sp.View() + " " + label)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}
