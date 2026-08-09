package workspaces

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/tui"
)

// recover_tui.go is the bubbletea M-r "Workspace History" picker: every
// worktree on disk (live or not), with Enter to open, M-x to delete (with a
// confirm banner), and cross-jumps to the sibling pickers.

// recoverItem is one worktree row.
type recoverItem struct{ repo, branch, desc string }

func (i recoverItem) Title() string       { return i.repo + " / " + i.branch }
func (i recoverItem) Description() string { return i.desc }
func (i recoverItem) FilterValue() string { return i.repo + " " + i.branch }
func (i recoverItem) id() string          { return i.repo + "\x00" + i.branch }

type recoverRowsMsg struct{ items []list.Item }

const recoverTitle = "Recover Workspace"

type recoverModel struct {
	h       *tmuxhost.Client
	list    list.Model
	theme   tui.Theme
	mode    sessionsMode
	confirm struct{ repo, branch string }

	outcome   tui.Outcome
	cancelled bool
}

func newRecoverModel(h *tmuxhost.Client, items []list.Item) *recoverModel {
	theme := tui.RecoverTheme()
	m := &recoverModel{h: h, theme: theme}
	l := list.New(items, theme.Delegate(), 0, 0)
	l.Title = recoverTitle
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.KeyMap.Quit.SetEnabled(false)
	theme.StyleListChrome(&l)
	l.AdditionalShortHelpKeys = recoverKeys
	l.AdditionalFullHelpKeys = recoverKeys
	m.list = l
	return m
}

func recoverKeys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("alt+x"), key.WithHelp("M-x", "delete")),
		key.NewBinding(key.WithKeys("alt+s"), key.WithHelp("M-s", "sessions")),
		key.NewBinding(key.WithKeys("alt+n"), key.WithHelp("M-n", "new")),
	}
}

func (m *recoverModel) Init() tea.Cmd { return nil }

func (m *recoverModel) deleteCmd(repo, branch string) tea.Cmd {
	h := m.h
	return func() tea.Msg {
		if err := recoverDeleteRow(h, repo, branch); err != nil {
			debuglog.LogErr("workspaces.recover: delete", err)
		}
		items, _ := recoverListItems()
		return recoverRowsMsg{items: items}
	}
}

func (m *recoverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil
	case recoverRowsMsg:
		return m, m.list.SetItems(msg.items)
	case tea.KeyMsg:
		if m.list.SettingFilter() {
			break
		}
		if m.mode == sessConfirm {
			switch msg.String() {
			case "y", "enter":
				c := m.confirm
				m.setConfirm(false, "", "")
				return m, m.deleteCmd(c.repo, c.branch)
			case "n", "esc", "ctrl+c":
				m.setConfirm(false, "", "")
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(recoverItem); ok {
				m.outcome = tui.Outcome{Selection: it.id()}
				return m, tea.Quit
			}
			m.cancelled = true
			return m, tea.Quit
		case "alt+x":
			if it, ok := m.list.SelectedItem().(recoverItem); ok {
				m.setConfirm(true, it.repo, it.branch)
			}
			return m, nil
		case "alt+s":
			m.outcome = tui.Outcome{Key: "workspaces/sessions"}
			return m, tea.Quit
		case "alt+n":
			m.outcome = tui.Outcome{Key: "workspaces/pick"}
			return m, tea.Quit
		case "alt+;":
			m.outcome = tui.Outcome{Key: "toolselector/select"}
			return m, tea.Quit
		case "alt+u":
			m.outcome = tui.Outcome{Key: "workspaces/clone"}
			return m, tea.Quit
		}
		// A bare rune starts filtering immediately (fzf-like type-to-filter).
		if handled, cmd := tui.MaybeStartFilter(&m.list, msg); handled {
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// setConfirm enters/leaves delete-confirm mode, swapping the title chip to a
// red "delete X? (y/n)" banner.
func (m *recoverModel) setConfirm(on bool, repo, branch string) {
	m.mode = sessNormal
	m.list.Styles.Title = m.theme.Title()
	m.list.Title = recoverTitle
	if on {
		m.mode = sessConfirm
		m.confirm.repo, m.confirm.branch = repo, branch
		m.list.Styles.Title = lipgloss.NewStyle().Foreground(tui.ColBg).Background(tui.ColRed).Bold(true).Padding(0, 1)
		m.list.Title = "delete " + repo + "/" + branch + "? (y/n)"
	}
}

func (m *recoverModel) View() string         { return m.list.View() }
func (m *recoverModel) Outcome() tui.Outcome { return m.outcome }
func (m *recoverModel) Cancelled() bool      { return m.cancelled }
