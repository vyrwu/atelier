package textprompt

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// defaultAccent is the heading color when Options.Accent is unset (green).
const defaultAccent = "35"

// model is the bubbletea program behind Read: a heading above a
// bubbles/textarea rendered in its editor styling (line numbers, a left
// gutter bar, an active-line highlight). Enter submits; Shift/Alt+Enter and
// Ctrl-J insert a newline; Esc / Ctrl-C cancel. No footer/legend.
type model struct {
	ta        textarea.Model
	title     string
	accent    string
	submitted bool
	cancelled bool
}

func newModel(opts Options) model {
	if opts.Accent == "" {
		opts.Accent = defaultAccent
	}
	// textarea.New() ships the editor look we want out of the box: line
	// numbers, a ThickBorder left-gutter prompt, and a cursor-line highlight.
	// Keep those defaults rather than flattening them.
	ta := textarea.New()
	ta.Placeholder = opts.Placeholder
	ta.CharLimit = 0 // no limit — an intent can be a paragraph

	// Enter SUBMITS (handled in Update). Newline is an explicit gesture:
	// Shift+Enter (where the terminal reports enhanced keys), Alt+Enter, or
	// Ctrl-J (both always work under tmux). Rebinding InsertNewline off "enter"
	// is what frees Enter to submit.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	ta.Focus()
	return model{ta: ta, title: opts.Title, accent: opts.Accent}
}

func (m model) Init() tea.Cmd { return textarea.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			if msg.Alt {
				break // Alt+Enter → newline: fall through to the textarea
			}
			m.submitted = true
			return m, tea.Quit
		case tea.KeyEsc, tea.KeyCtrlC:
			m.cancelled = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// resize sizes the textarea to the popup, reserving rows for the heading (a
// title line + a blank spacer) and one column of breathing room each side.
func (m *model) resize(w, h int) {
	reserved := 0
	if m.title != "" {
		reserved = 2
	}
	taW := w - 2
	taH := h - reserved
	if taW < 1 {
		taW = 1
	}
	if taH < 1 {
		taH = 1
	}
	m.ta.SetWidth(taW)
	m.ta.SetHeight(taH)
}

func (m model) View() string {
	if m.title == "" {
		return m.ta.View()
	}
	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(m.accent))
	return heading.Render(m.title) + "\n\n" + m.ta.View()
}
