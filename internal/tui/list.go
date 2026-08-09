package tui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// SimpleItem is a single-select row for list-based pickers. It satisfies
// bubbles/list.DefaultItem (Title + Description + FilterValue) so it renders
// with the standard two-line delegate, plus an ID returned as the selection.
type SimpleItem struct {
	IDStr    string
	TitleStr string
	DescStr  string
	Filter   string // fuzzy-search text; falls back to the title
}

func (i SimpleItem) Title() string       { return i.TitleStr }
func (i SimpleItem) Description() string { return i.DescStr }
func (i SimpleItem) ID() string          { return i.IDStr }
func (i SimpleItem) FilterValue() string {
	if i.Filter != "" {
		return i.Filter
	}
	return i.TitleStr
}

// identifiable is anything a List can report as the accepted selection.
type identifiable interface{ ID() string }

// MaybeStartFilter makes a bubbles/list filter as-you-type (fzf-like): if the
// list isn't already filtering and msg is a bare printable rune, it enters
// filter mode and types that rune. Returns handled=true when it consumed the
// key (the caller should return the cmd and stop). Bubbles' native filter
// needs a leading `/`; this removes that step so typing just filters.
func MaybeStartFilter(l *list.Model, msg tea.KeyMsg) (bool, tea.Cmd) {
	if l.SettingFilter() || l.FilterState() == list.Filtering {
		return false, nil
	}
	if msg.Type != tea.KeyRunes || msg.Alt || len(msg.Runes) == 0 {
		return false, nil
	}
	var c1, c2 tea.Cmd
	*l, c1 = l.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}) // enter filter mode
	*l, c2 = l.Update(msg)                                                // type the rune
	return true, tea.Batch(c1, c2)
}

// KeyAction binds a key to an action token reported via Outcome.Key (e.g. a
// cross-jump to a sibling picker) and contributes to the help bar.
type KeyAction struct {
	Binding key.Binding
	Token   string
}

// Action builds a KeyAction. keys is a bubbletea key string ("alt+n");
// label/desc are the help-bar hint ("M-n", "new workspace").
func Action(token, keys, label, desc string) KeyAction {
	return KeyAction{
		Binding: key.NewBinding(key.WithKeys(keys), key.WithHelp(label, desc)),
		Token:   token,
	}
}

// List is a ready-to-use single-select picker built on bubbles/list: the
// standard themed two-line delegate, native fuzzy filtering (`/`), a live
// help bar, Enter to accept, Esc/Ctrl-C to cancel, and caller-defined action
// keys (cross-jumps). Bespoke surfaces (the live M-s picker) build their own
// model; this covers every simple picker.
type List struct {
	list      list.Model
	actions   []KeyAction
	outcome   Outcome
	cancelled bool
}

// NewList builds a single-select picker with the theme's accent (title chip,
// selection, filter). actions are extra keybinds surfaced in the help bar and
// reported via Outcome.Key.
func NewList(theme Theme, title string, items []list.Item, actions ...KeyAction) *List {
	return NewListWithDelegate(theme, title, items, theme.Delegate(), actions...)
}

// NewListWithDelegate is NewList with a caller-supplied row delegate — used by
// the tool selector, which colors each row by its own per-tool accent.
func NewListWithDelegate(theme Theme, title string, items []list.Item, d list.ItemDelegate, actions ...KeyAction) *List {
	m := &List{actions: actions}
	l := list.New(items, d, 0, 0)
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	// We own cancellation (Esc/Ctrl-C) so it maps to ErrCancelled; disable
	// the list's own quit binding to avoid a silent no-outcome exit.
	l.KeyMap.Quit.SetEnabled(false)
	theme.StyleListChrome(&l)
	if len(actions) > 0 {
		l.AdditionalShortHelpKeys = m.actionBindings
		l.AdditionalFullHelpKeys = m.actionBindings
	}
	m.list = l
	return m
}

func (m *List) actionBindings() []key.Binding {
	bs := make([]key.Binding, len(m.actions))
	for i, a := range m.actions {
		bs[i] = a.Binding
	}
	return bs
}

func (m *List) Init() tea.Cmd { return nil }

func (m *List) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		// While the filter input is focused, every key belongs to it.
		if m.list.SettingFilter() {
			break
		}
		switch {
		case key.Matches(msg, m.list.KeyMap.ForceQuit) || msg.String() == "esc":
			m.cancelled = true
			return m, tea.Quit
		case msg.String() == "enter":
			if it, ok := m.list.SelectedItem().(identifiable); ok {
				m.outcome = Outcome{Selection: it.ID(), Query: m.list.FilterValue()}
				return m, tea.Quit
			}
			m.cancelled = true
			return m, tea.Quit
		default:
			for _, a := range m.actions {
				if key.Matches(msg, a.Binding) {
					m.outcome = Outcome{Key: a.Token, Query: m.list.FilterValue()}
					return m, tea.Quit
				}
			}
			if handled, cmd := MaybeStartFilter(&m.list, msg); handled {
				return m, cmd
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *List) View() string { return m.list.View() }

func (m *List) Outcome() Outcome { return m.outcome }
func (m *List) Cancelled() bool  { return m.cancelled }
