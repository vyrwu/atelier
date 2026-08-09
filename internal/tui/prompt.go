package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PromptConfig configures a single-field text prompt.
type PromptConfig struct {
	Title       string      // accent title chip
	Glyph       string      // leading accent glyph before the input (e.g. "製 ")
	Placeholder string      // dimmed placeholder text
	Initial     string      // pre-filled value
	Header      string      // sub-line under the title (hint or error)
	HeaderError bool        // render Header in the error color
	Actions     []KeyAction // extra keys (cross-jumps, mode toggles)
}

// Prompt is a one-line text-entry surface (bubbles/textinput) with a title,
// an optional header/error line, a help bar, and caller action keys. Enter
// accepts (Outcome.Query = typed text); Esc/Ctrl-C cancels; an action key
// reports Outcome.Key (with the current text in Query). Replaces the fzf
// --print-query prompt used by the clone / creator flows.
type Prompt struct {
	input     textinput.Model
	help      help.Model
	theme     Theme
	cfg       PromptConfig
	outcome   Outcome
	cancelled bool
}

func NewPrompt(theme Theme, cfg PromptConfig) *Prompt {
	a := theme.AccentStyle()
	ti := textinput.New()
	ti.Placeholder = cfg.Placeholder
	ti.SetValue(cfg.Initial)
	ti.CursorEnd()
	ti.Prompt = cfg.Glyph
	ti.PromptStyle = a.Bold(true)
	ti.Cursor.Style = a
	ti.Focus()

	h := help.New()
	h.Styles.ShortKey = a
	h.Styles.FullKey = a
	return &Prompt{input: ti, help: h, theme: theme, cfg: cfg}
}

func (m *Prompt) Init() tea.Cmd { return textinput.Blink }

func (m *Prompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.help.Width = msg.Width
		if w := msg.Width - lipgloss.Width(m.cfg.Glyph) - 4; w > 0 {
			m.input.Width = w
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			m.outcome = Outcome{Query: m.input.Value()}
			return m, tea.Quit
		}
		for _, a := range m.cfg.Actions {
			if key.Matches(msg, a.Binding) {
				m.outcome = Outcome{Key: a.Token, Query: m.input.Value()}
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Prompt) View() string {
	var b strings.Builder
	b.WriteString(m.theme.Title().Render(m.cfg.Title))
	b.WriteString("\n\n")
	if m.cfg.Header != "" {
		hs := SubtleStyle
		if m.cfg.HeaderError {
			hs = ErrorStyle
		}
		b.WriteString(hs.Render(m.cfg.Header))
		b.WriteString("\n\n")
	}
	b.WriteString(m.input.View())
	b.WriteString("\n\n")
	b.WriteString(m.help.ShortHelpView(m.helpBindings()))
	return b.String()
}

func (m *Prompt) helpBindings() []key.Binding {
	bs := []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
	for _, a := range m.cfg.Actions {
		bs = append(bs, a.Binding)
	}
	return bs
}

func (m *Prompt) Outcome() Outcome { return m.outcome }
func (m *Prompt) Cancelled() bool  { return m.cancelled }
