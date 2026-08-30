package workspaces

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/tui"
	"github.com/vyrwu/atelier/internal/workspace"
)

// sessions_tui.go is the bubbletea M-s workspace picker — the native
// replacement for the fzf sessions picker (issue #86). Two things make it
// worth the migration: (1) a live tea.Tick re-reads BuildSessionList and
// diffs rows in place (cursor preserved by identity) so attention dots and
// recaps update WITHOUT reopening M-s — no more fzf --listen HTTP push; and
// (2) rows render from STRUCTURED data via a lipgloss delegate, so the look
// is owned here, not scraped from pre-baked ANSI.

// sessionsRefreshInterval is how often the open picker re-reads workspace
// state. Cheap (one list-windows + local git on TTL-fresh windows), so a
// snappy cadence replaces fzf's throttled HTTP push.
const sessionsRefreshInterval = 2 * time.Second

// sessionItem adapts a SessionRow to bubbles/list.
type sessionItem struct{ row SessionRow }

// key is the stable identity used to preserve the cursor across live reloads
// and to round-trip the accepted / tagged / deleted selection.
func (i sessionItem) key() string { return i.row.Session + "\x00" + i.row.Window }

// FilterValue searches the name/branch/tag, never the recap — mirroring the
// fzf picker's --nth=1 search scope.
func (i sessionItem) FilterValue() string {
	return i.row.DisplayName + " " + i.row.Window + " " + i.row.Tag
}

// sessionDelegate renders the two-line workspace row from structured data.
// accent is the picker's accent (red for M-s) used for the selection bar and
// the current-workspace glyph.
type sessionDelegate struct{ accent lipgloss.Color }

func (sessionDelegate) Height() int                         { return 2 }
func (sessionDelegate) Spacing() int                        { return 0 }
func (sessionDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d sessionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(sessionItem)
	if !ok {
		return
	}
	r := it.row
	selected := index == m.Index()
	width := m.Width()
	if width < 10 {
		width = 10
	}

	// A selected row gets an accent left bar (both lines) and an accent
	// branch name — the idiomatic bubbles selection look.
	bar := "  "
	branchStyle := tui.BoldStyle
	if selected {
		bar = lipgloss.NewStyle().Foreground(d.accent).Render("▎") + " "
		branchStyle = lipgloss.NewStyle().Foreground(d.accent).Bold(true)
	}

	// Column order matches the pre-migration layout: age, status dot, forge
	// badge, tag pill, then the branch/repo name — the badge + tag LEAD (they
	// distinguish parallel workspaces at a glance), the name trails.
	segs := []string{}
	if r.Age != "" {
		segs = append(segs, tui.SubtleStyle.Render(fmt.Sprintf("%3s", r.Age)))
	}
	segs = append(segs, statusDot(r, d.accent))
	if g := forgeGlyph(r.ForgeState); g != "" {
		segs = append(segs, g)
	}
	if r.Tag != "" {
		segs = append(segs, tui.TagPill(r.Tag))
	}
	segs = append(segs, branchStyle.Render(r.Window), tui.SubtleStyle.Render(r.DisplayName))
	line1 := bar + strings.Join(segs, " ")

	line2 := bar
	if r.Recap != "" {
		line2 += tui.SubtleStyle.Italic(true).Render("· " + r.Recap)
	}

	_, _ = io.WriteString(w, ansi.Truncate(line1, width, "…")+"\n"+ansi.Truncate(line2, width, "…"))
}

// statusDot is the colored agent-state glyph: ❯ current (picker accent) ·
// yellow ● blocked (needs you) · cyan ● running · dim ○ idle.
func statusDot(r SessionRow, accent lipgloss.Color) string {
	switch {
	case r.IsCurrent:
		return lipgloss.NewStyle().Foreground(accent).Bold(true).Render("❯")
	case r.State == StateBlocked:
		return lipgloss.NewStyle().Foreground(tui.ColYellow).Render("●")
	case r.State == StateRunning:
		return lipgloss.NewStyle().Foreground(tui.ColCyan).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(tui.ColSubtle).Render("○")
	}
}

// forgeGlyph renders the kernel-owned forge (PR) glyph for a raw @forge_state,
// using the adapter's glyph+color spec. "" when no forge item / no adapter.
func forgeGlyph(state string) string {
	glyph, color, ok := integration.ForgeGlyph(integration.ForgeState(strings.TrimSpace(state)))
	if !ok {
		return ""
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph)
}

type sessionsMode int

const (
	sessNormal  sessionsMode = iota
	sessConfirm              // delete confirmation ("y/n")
)

type confirmTarget struct{ session, window string }

// rowsMsg carries a fresh BuildSessionList result (from the tick or a
// post-delete reload). Built off the event loop.
type rowsMsg struct {
	rows []SessionRow
	err  error
}

type sessTickMsg time.Time

type sessionsModel struct {
	h     *tmuxhost.Client
	list  list.Model
	theme tui.Theme
	forge bool

	pinned  bool
	mode    sessionsMode
	confirm confirmTarget

	outcome   tui.Outcome
	cancelled bool
}

const sessionsTitle = "Select Workspace"

func newSessionsModel(h *tmuxhost.Client, rows []SessionRow, pin string) *sessionsModel {
	theme := tui.SessionsTheme()
	m := &sessionsModel{h: h, theme: theme, forge: forgeActive(), pinned: pin != ""}
	l := list.New(rowsToItems(rows), sessionDelegate{accent: theme.Accent}, 0, 0)
	l.Title = sessionsTitle
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.KeyMap.Quit.SetEnabled(false)
	theme.StyleListChrome(&l)
	l.AdditionalShortHelpKeys = m.extraKeys
	l.AdditionalFullHelpKeys = m.extraKeys
	if pin != "" {
		l.SetFilterText(pin)
	}
	m.list = l
	return m
}

// extraKeys advertises the picker's action keys in the help bar.
func (m *sessionsModel) extraKeys() []key.Binding {
	ks := []key.Binding{
		key.NewBinding(key.WithKeys("alt+x"), key.WithHelp("M-x", "delete")),
		key.NewBinding(key.WithKeys("alt+t"), key.WithHelp("M-t", "tag")),
		key.NewBinding(key.WithKeys("alt+p"), key.WithHelp("M-p", "pin")),
	}
	if m.forge {
		ks = append(ks, key.NewBinding(key.WithKeys("alt+o"), key.WithHelp("M-o", "open PR")))
	}
	return append(ks,
		key.NewBinding(key.WithKeys("alt+n"), key.WithHelp("M-n", "new")),
		key.NewBinding(key.WithKeys("alt+r"), key.WithHelp("M-r", "recover")),
	)
}

func rowsToItems(rows []SessionRow) []list.Item {
	items := make([]list.Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, sessionItem{row: r})
	}
	return items
}

func (m *sessionsModel) Init() tea.Cmd { return sessTick() }

func sessTick() tea.Cmd {
	return tea.Tick(sessionsRefreshInterval, func(t time.Time) tea.Msg { return sessTickMsg(t) })
}

func (m *sessionsModel) reloadCmd() tea.Cmd {
	h := m.h
	return func() tea.Msg {
		rows, err := BuildSessionList(h)
		return rowsMsg{rows: rows, err: err}
	}
}

// deleteCmd runs the in-process delete (same primitives as _delete-row) off
// the event loop, then reloads the list.
func (m *sessionsModel) deleteCmd(session, window string) tea.Cmd {
	h := m.h
	return func() tea.Msg {
		if err := deleteRow(h, session, window); err != nil {
			debuglog.LogErr("workspaces.sessions: delete", err)
		}
		rows, err := BuildSessionList(h)
		return rowsMsg{rows: rows, err: err}
	}
}

func (m *sessionsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil

	case sessTickMsg:
		return m, tea.Batch(m.reloadCmd(), sessTick())

	case rowsMsg:
		if msg.err != nil {
			return m, nil // best-effort: keep the current list on a transient error
		}
		// Preserve the cursor by identity: the pending-delete row while
		// confirming, else the current selection.
		keepKey := ""
		switch m.mode {
		case sessConfirm:
			keepKey = m.confirm.session + "\x00" + m.confirm.window
		default:
			if it, ok := m.list.SelectedItem().(sessionItem); ok {
				keepKey = it.key()
			}
		}
		filter := m.list.FilterValue()
		applied := m.list.FilterState() == list.FilterApplied
		m.list.SetItems(rowsToItems(msg.rows))
		// SetItems nulls the filtered set and re-filters ASYNChronously, which
		// would flash the list empty for one frame under a sticky M-p pin
		// (FilterApplied). Re-apply the filter text synchronously so the
		// visible set is populated in THIS Update, then re-pin below.
		if applied && filter != "" {
			m.list.SetFilterText(filter)
		}
		// Re-pin the cursor to the same identity (the fzf --track contract).
		// Index into VisibleItems so it's correct whether the list is
		// Unfiltered or FilterApplied; only skip while the user is actively
		// TYPING a filter (Filtering), where a tick shouldn't move the cursor.
		if keepKey != "" && !m.list.SettingFilter() {
			for i, raw := range m.list.VisibleItems() {
				if it, ok := raw.(sessionItem); ok && it.key() == keepKey {
					m.list.Select(i)
					break
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		if m.list.SettingFilter() {
			break
		}
		if m.mode == sessConfirm {
			switch msg.String() {
			case "y", "enter":
				t := m.confirm
				m.setConfirm(false, confirmTarget{})
				return m, m.deleteCmd(t.session, t.window)
			case "n", "esc", "ctrl+c":
				m.setConfirm(false, confirmTarget{})
				return m, nil
			}
			return m, nil // swallow other keys while confirming
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(sessionItem); ok {
				m.outcome = tui.Outcome{Selection: it.key()}
				return m, tea.Quit
			}
			m.cancelled = true
			return m, tea.Quit
		case "alt+x":
			if it, ok := m.list.SelectedItem().(sessionItem); ok {
				m.setConfirm(true, confirmTarget{session: it.row.Session, window: it.row.Window})
			}
			return m, nil
		case "alt+t":
			// Tagging needs a nested text prompt the model can't run inline;
			// report it so the RunE loop runs the prompt then reopens.
			if it, ok := m.list.SelectedItem().(sessionItem); ok {
				m.outcome = tui.Outcome{Key: "tag", Selection: it.key()}
				return m, tea.Quit
			}
			return m, nil
		case "alt+p":
			return m, m.toggleScopePin()
		case "alt+o":
			return m, m.openForgeCmd()
		case "alt+q":
			self, err := os.Executable()
			if err != nil || self == "" {
				self = "atelier"
			}
			_ = exec.Command(self, "server", "quit").Start()
			m.outcome = tui.Outcome{Key: "quit"}
			return m, tea.Quit
		case "alt+n":
			m.outcome = tui.Outcome{Key: "workspaces/pick"}
			return m, tea.Quit
		case "alt+r":
			m.outcome = tui.Outcome{Key: "workspaces/recover"}
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
// red "delete X? (y/n)" banner so the prompt is unmistakable.
func (m *sessionsModel) setConfirm(on bool, t confirmTarget) {
	m.mode = sessNormal
	m.list.Styles.Title = m.theme.Title()
	m.list.Title = sessionsTitle
	if on {
		m.mode = sessConfirm
		m.confirm = t
		m.list.Styles.Title = lipgloss.NewStyle().Foreground(tui.ColBg).Background(tui.ColRed).Bold(true).Padding(0, 1)
		m.list.Title = "delete " + t.window + "? (y/n)"
	}
}

// toggleScopePin flips the sticky M-p scope pin. The model mutation
// (pinned flag + filter) happens inline; the tmux write is deferred to a
// tea.Cmd so no I/O runs on the event loop (matching deleteCmd/openForgeCmd).
func (m *sessionsModel) toggleScopePin() tea.Cmd {
	h := m.h
	if m.pinned {
		m.pinned = false
		m.list.ResetFilter()
		return func() tea.Msg { _ = workspace.SetScopePin(h, ""); return nil }
	}
	q := strings.TrimSpace(m.list.FilterValue())
	if q == "" {
		return nil
	}
	m.pinned = true
	return func() tea.Msg { _ = workspace.SetScopePin(h, q); return nil }
}

func (m *sessionsModel) openForgeCmd() tea.Cmd {
	if !m.forge {
		return nil
	}
	it, ok := m.list.SelectedItem().(sessionItem)
	if !ok {
		return nil
	}
	h, session, window := m.h, it.row.Session, it.row.Window
	return func() tea.Msg {
		if forge := integration.Active().Forge; forge != nil {
			_ = openForge(h, forge, session+"\t"+window)
		}
		return nil
	}
}

func (m *sessionsModel) View() string { return m.list.View() }

func (m *sessionsModel) Outcome() tui.Outcome { return m.outcome }
func (m *sessionsModel) Cancelled() bool      { return m.cancelled }
