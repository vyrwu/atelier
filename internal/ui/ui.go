// Package ui is atelier's one renderer: a Bubble Tea program shown as a tmux
// popup overlay. It is the only user interface — active/all workspaces,
// new-workspace, pull-requests, worktrees, and confirmations are screens inside
// this one program, so moving between them is internal state (NFR-P3/P4/S5).
//
// It runs fresh in the popup and, on an action that leaves the overlay (switch,
// open shell), performs the tmux side-effect against the OUTER client that
// opened it, then quits. Dismissing just quits, returning you where you were.
package ui

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/tmux"
)

type screen int

const (
	scrActive    screen = iota // M-a — the working set (default)
	scrAll                     // M-w — all workspaces, incl. retired
	scrNew                     // M-n
	scrPRs                     // M-r
	scrWorktrees               // M-t
	scrConfirm
)

// row is one line in a workspace list.
type row struct {
	ws *core.Workspace
}

// prRow / wtRow are flat rows for the cross-workspace PR and worktree views.
type prRow struct {
	ws *core.Workspace
	pr core.PR
}
type wtRow struct {
	ws *core.Workspace
	wt core.Worktree
}

// Model is the whole UI. All screens share its data; a screen is just which
// slice of behaviour is active.
type Model struct {
	outer  string
	cfg    core.Config
	w, h   int
	screen screen

	// data (loaded locally — NFR-P1)
	all    []core.Workspace
	wt     map[string][]core.Worktree // slug → derived worktrees
	status map[string]core.AgentStatus

	// active / all list screens
	filter textinput.Model
	rows   []row // visible (filtered) rows
	sel    int

	// new screen
	intent textarea.Model

	// prs / worktrees / confirm screens
	focusWS      *core.Workspace // set transiently when confirming a delete
	returnScreen screen          // where confirm returns to
	prSel        int
	wtSel        int
	confirm      string // the confirmation prompt

	err string
}

// Run shows the UI in the popup's pty. outer is the client that opened the popup
// (from @atelier_outer). start names the screen to open on.
func Run(outer, start string) error {
	m := newModel(outer)
	switch start {
	case "all":
		m.screen = scrAll
	case "new":
		m.screen = scrNew
	case "prs":
		m.screen = scrPRs
	case "worktrees":
		m.screen = scrWorktrees
	}
	m.rebuild()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func newModel(outer string) Model {
	fi := textinput.New()
	fi.Prompt = ""
	fi.Placeholder = "filter…"
	fi.Focus()

	ta := textarea.New()
	ta.Placeholder = "Describe the task…"
	ta.ShowLineNumbers = false
	ta.Focus()

	m := Model{
		outer:  outer,
		cfg:    core.LoadConfig(),
		filter: fi,
		intent: ta,
		wt:     map[string][]core.Worktree{},
		status: map[string]core.AgentStatus{},
	}
	m.load()
	// First-run / empty: go straight to the new-workspace prompt.
	if len(m.all) == 0 {
		m.screen = scrNew
	}
	return m
}

// load reads workspaces from state, derives worktrees from disk, and resolves
// each agent's status against the live tmux sessions. Local only.
func (m *Model) load() {
	st := core.Load()
	m.all = st.Workspaces
	live := map[string]bool{}
	for _, s := range tmux.ListSessions() {
		live[s] = true
	}
	m.wt = map[string][]core.Worktree{}
	m.status = map[string]core.AgentStatus{}
	for _, w := range m.all {
		m.wt[w.Slug] = git.Worktrees(w.Root())
		m.status[w.Session] = agent.Status(w.Session, live[w.Session])
	}
	m.rebuild()
}

// rebuild flattens the (filtered) workspaces into visible rows — a flat list of
// workspaces. The active view hides retired ones; the all view shows every
// workspace. Worktrees live in their own view (M-t), not nested here.
func (m *Model) rebuild() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.rows = m.rows[:0]
	for i := range m.all {
		w := &m.all[i]
		if m.screen == scrActive && w.Retired {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(w.Title+" "+w.Slug+" "+w.Intent), q) {
			continue
		}
		m.rows = append(m.rows, row{ws: w})
	}
	if m.sel >= len(m.rows) {
		m.sel = len(m.rows) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
}

func (m Model) prRows() []prRow {
	var rows []prRow
	for i := range m.all {
		w := &m.all[i]
		for _, pr := range w.PRs {
			rows = append(rows, prRow{ws: w, pr: pr})
		}
	}
	return rows
}

func (m Model) wtRows() []wtRow {
	var rows []wtRow
	for i := range m.all {
		w := &m.all[i]
		for _, wt := range m.wt[w.Slug] {
			rows = append(rows, wtRow{ws: w, wt: wt})
		}
	}
	return rows
}

// contextWorkspace is the workspace a global action (deactivate/delete) targets.
func (m Model) contextWorkspace() *core.Workspace {
	switch m.screen {
	case scrActive, scrAll:
		if r, ok := m.cur(); ok {
			return r.ws
		}
	case scrPRs:
		rows := m.prRows()
		if m.prSel >= 0 && m.prSel < len(rows) {
			return rows[m.prSel].ws
		}
	case scrWorktrees:
		rows := m.wtRows()
		if m.wtSel >= 0 && m.wtSel < len(rows) {
			return rows[m.wtSel].ws
		}
	}
	return nil
}

func (m Model) Init() tea.Cmd { return textarea.Blink }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.intent.SetWidth(min(msg.Width-6, 100))
		m.intent.SetHeight(min(msg.Height-10, 10))
		return m, nil
	case prsRefreshedMsg:
		for i := range m.all {
			if m.all[i].Slug == msg.slug {
				m.all[i].PRs = msg.prs
			}
		}
		_ = core.Update(func(s *core.State) {
			if w := s.Find(msg.slug); w != nil {
				w.PRs = msg.prs
				w.PRsRefreshed = time.Now()
			}
		})
		return m, nil
	case tea.KeyMsg:
		// Global keys work from every screen: C-c closes; M-a/M-w/M-n/M-r/M-t jump
		// to a view; M-d retires the selection; M-x deletes it (with confirm).
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "alt+a":
			m.screen = scrActive
			m.filter.Focus()
			m.rebuild()
			return m, nil
		case "alt+w":
			m.screen = scrAll
			m.filter.Focus()
			m.rebuild()
			return m, nil
		case "alt+n":
			m.screen = scrNew
			m.intent.Focus()
			return m, textarea.Blink
		case "alt+r":
			m.prSel = 0
			m.screen = scrPRs
			return m, m.refreshCmd()
		case "alt+t":
			m.wtSel = 0
			m.screen = scrWorktrees
			return m, nil
		case "alt+d":
			if w := m.contextWorkspace(); w != nil && !w.Retired {
				m.deactivate(*w)
			}
			return m, nil
		case "alt+x":
			if w := m.contextWorkspace(); w != nil {
				m.focusWS = w
				m.returnScreen = m.screen
				m.confirm = m.deletePrompt(w)
				m.screen = scrConfirm
			}
			return m, nil
		}
		switch m.screen {
		case scrActive:
			return m.updateActive(msg)
		case scrAll:
			return m.updateAll(msg)
		case scrNew:
			return m.updateNew(msg)
		case scrPRs:
			return m.updatePRs(msg)
		case scrWorktrees:
			return m.updateWorktrees(msg)
		case scrConfirm:
			return m.updateConfirm(msg)
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	var body, footer string
	switch m.screen {
	case scrActive:
		body, footer = m.viewActive(), m.footer("open", "M-d retire", "M-x delete")
	case scrAll:
		body, footer = m.viewAll(), m.footer("restore", "M-x delete")
	case scrNew:
		body, footer = m.viewNew(), hintStyle.Render("↵ start (background)  ·  ⌥↵ start & open  ·  ^j newline  ·  esc close")
	case scrPRs:
		body, footer = m.viewPRs(), m.footer("open", "r refresh")
	case scrWorktrees:
		body, footer = m.viewWorktrees(), m.footer("shell")
	case scrConfirm:
		body, footer = m.viewConfirm(), hintStyle.Render("y delete  ·  n cancel")
	}
	// Fixed-size frame with the footer pinned to the bottom. The rounded frame is
	// the only border — the tmux popup border is disabled (-B).
	contentW := m.w - 4
	innerH := m.h - 2
	if footer == "" {
		inner := lipgloss.NewStyle().Width(contentW).Height(innerH).Render(body)
		return frameStyle.Width(m.w - 2).Height(innerH).Render(inner)
	}
	footer = lipgloss.NewStyle().Width(contentW).Render(footer)
	fh := lipgloss.Height(footer)
	bodyBox := lipgloss.NewStyle().Width(contentW).Height(innerH - fh - 1).Render(body)
	inner := bodyBox + "\n" + dimStyle.Render(strings.Repeat("─", contentW)) + "\n" + footer
	return frameStyle.Width(m.w - 2).Height(innerH).Render(inner)
}

// footer is the bottom shortcut bar: the Enter verb, any view-specific actions,
// then the constant nav row.
func (m Model) footer(verb string, extras ...string) string {
	parts := []string{"↵ " + verb}
	parts = append(parts, extras...)
	parts = append(parts, "M-a active", "M-w all", "M-n new", "esc close")
	return hintStyle.Render(strings.Join(parts, "  ·  "))
}

// --- actions ---------------------------------------------------------------

// switchTo moves the outer client to a session and closes the overlay. It
// revives a dead agent session first (a tmux-server restart leaves the record
// but not the session), and selects the agent window so it lands on Claude.
func (m Model) switchTo(session string) (tea.Model, tea.Cmd) {
	if ws := m.bySession(session); ws != nil {
		_ = agent.EnsureClaude(session, ws.Root(), ws.Slug)
	}
	if tmux.SelectWindow(session, agent.ClaudeWindow) != nil {
		_ = tmux.SelectFirstWindow(session) // fallback if the agent window isn't named
	}
	_ = tmux.SwitchClient(m.outer, session)
	return m, tea.Quit
}

func (m Model) bySession(session string) *core.Workspace {
	for i := range m.all {
		if m.all[i].Session == session {
			return &m.all[i]
		}
	}
	return nil
}

// beginCreate fires a detached builder that names, launches, and records the
// workspace in the background, then closes the popup immediately so you keep
// your focus. With switchIn it also switches you in once it's ready.
func (m Model) beginCreate(intent string, switchIn bool) (tea.Model, tea.Cmd) {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return m, nil
	}
	args := []string{"create", intent, "--outer", m.outer}
	if switchIn {
		args = append(args, "--switch")
	}
	spawnDetached(args...)
	_ = tmux.Notify(m.outer, "creating workspace…")
	return m, tea.Quit
}

// deactivate retires a workspace: kill its session, mark it retired (kept on
// disk, restorable from M-w), and refresh the list.
func (m *Model) deactivate(w core.Workspace) {
	_ = tmux.KillSession(w.Session)
	setRetired(w.Slug, true)
	m.load()
}

// reactivate restores a retired workspace and switches into it (resuming Claude).
func (m Model) reactivate(w core.Workspace) (tea.Model, tea.Cmd) {
	setRetired(w.Slug, false)
	return m.switchTo(w.Session)
}

func setRetired(slug string, retired bool) {
	_ = core.Update(func(s *core.State) {
		if w := s.Find(slug); w != nil {
			w.Retired = retired
		}
	})
}

// deleteWorkspace removes worktrees, the session, the directory, and the state
// entry, then refreshes the list in place.
func (m *Model) deleteWorkspace(w core.Workspace) {
	for _, t := range m.wt[w.Slug] {
		_ = git.RemoveWorktree(t.Path)
	}
	_ = tmux.KillSession(w.Session)
	_ = os.RemoveAll(w.Root())
	_ = core.RemoveWorkspace(w.Slug)
	m.load()
}

// spawnDetached runs `atelier <args...>` fully detached so it survives the popup
// closing — used for background workspace creation.
func spawnDetached(args ...string) {
	self, err := os.Executable()
	if err != nil {
		self = "atelier"
	}
	c := exec.Command(self, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = c.Start()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
