package tui

import (
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// loaderDelay suppresses the UI for operations that finish quickly, so a
// fast build doesn't flash a spinner. Matches the old BoxSpinner delay gate.
const loaderDelay = 120 * time.Millisecond

// Loader runs fn while showing a themed bubbles spinner + message, and
// returns fn's error. fn receives a status callback to update the label
// mid-run (multi-stage progress). Two graceful degradations preserve the old
// spinner's behavior: if there's no controlling terminal (tests, pipes) fn
// runs with no UI; if fn finishes within loaderDelay no UI is shown at all.
// Replaces internal/spinner.
func Loader(message string, fn func(status func(string)) error) error {
	opts, ttyFile := ttyOptions()
	if ttyFile != nil {
		defer func() { _ = ttyFile.Close() }()
	}

	var mu sync.Mutex
	var prog *tea.Program
	pending := message
	status := func(s string) {
		mu.Lock()
		p := prog
		pending = s
		mu.Unlock()
		if p != nil {
			p.Send(loaderStatusMsg(s))
		}
	}

	done := make(chan error, 1)
	go func() { done <- fn(status) }()

	// No TTY → run headless (the picker/loader can't render anyway).
	if opts == nil {
		return <-done
	}
	// Delay gate: skip the UI entirely for fast operations.
	select {
	case err := <-done:
		return err
	case <-time.After(loaderDelay):
	}

	forceStaticColorProfile()
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(SpinnerStyle))
	mu.Lock()
	m := &loaderModel{spin: sp, label: pending}
	p := tea.NewProgram(m, opts...)
	prog = p
	mu.Unlock()
	go func() { p.Send(loaderDoneMsg{err: <-done}) }()

	final, runErr := p.Run()
	if runErr != nil {
		return runErr
	}
	return final.(*loaderModel).err
}

type loaderStatusMsg string
type loaderDoneMsg struct{ err error }

type loaderModel struct {
	spin  spinner.Model
	label string
	err   error
}

func (m *loaderModel) Init() tea.Cmd { return m.spin.Tick }

func (m *loaderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loaderStatusMsg:
		m.label = string(msg)
		return m, nil
	case loaderDoneMsg:
		m.err = msg.err
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *loaderModel) View() string {
	return "\n  " + m.spin.View() + " " + BoldStyle.Render(m.label) + "\n"
}
