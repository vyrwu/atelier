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
	"sort"
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
	scrActive    screen = iota // M-s — the working set of spaces (default)
	scrTrash                   // M-t — retired spaces, restorable
	scrNew                     // M-n
	scrPRs                     // M-p
	scrWorktrees               // M-w
	scrHelp                    // M-h
	scrConfirm
)

// Version is the atelier version, set by main at startup; shown in Help.
var Version = "dev"

// row is one line in a workspace list.
type row struct {
	ws *core.Workspace
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

	// curWS is the space the overlay was launched from (the outer client's
	// session), or nil from the home splash. The space-scoped views (PRs,
	// worktrees) render only its data. wtEntries caches its worktrees with
	// freshness so git isn't re-run on every keypress.
	curWS     *core.Workspace
	wtEntries []wtEntry

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
	confirm      string          // the confirmation prompt
	prPending    map[string]bool // PRs (repo#num) with a state change in flight

	err  string
	note string // transient positive feedback (e.g. "copied link")
}

// Run shows the UI in the popup's pty. outer is the client that opened the popup
// (from @atelier_outer). start names the screen to open on.
func Run(outer, start string) error {
	m := newModel(outer)
	switch start {
	case "trash":
		m.screen = scrTrash
	case "new":
		m.screen = scrNew
	case "prs":
		m.screen = scrPRs
	case "worktrees":
		m.screen = scrWorktrees
	case "help":
		m.screen = scrHelp
	}
	// The PR and worktree views are scoped to the current space. Opened off a
	// space (the home splash), there is nothing to show — toast a hint and close
	// instead of presenting an empty overlay.
	if (m.screen == scrPRs || m.screen == scrWorktrees) && m.curWS == nil {
		what := "worktrees"
		if m.screen == scrPRs {
			what = "pull requests"
		}
		_ = tmux.Notify(outer, "no space here  ·  open a space to see its "+what+"  ·  M-n to create")
		return nil
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
		outer:     outer,
		cfg:       core.LoadConfig(),
		filter:    fi,
		intent:    ta,
		wt:        map[string][]core.Worktree{},
		status:    map[string]core.AgentStatus{},
		prPending: map[string]bool{},
	}
	m.load()
	// Resolve the current space from the client that opened the overlay, and cache
	// its worktrees + freshness for the space-scoped views.
	m.curWS = m.bySession(tmux.ClientSession(outer))
	m.computeWorktrees()
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
	// load() re-allocates m.all, so re-resolve the current space into the fresh
	// slice — a cached *Workspace would dangle into the old backing array and
	// freeze the space-scoped PR/worktree views after a retire/delete.
	if m.curWS != nil {
		m.curWS = m.bySession(m.curWS.Session)
		m.computeWorktrees()
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
		// Spaces shows the working set; Trash shows only retired.
		if m.screen == scrActive && w.Retired {
			continue
		}
		if m.screen == scrTrash && !w.Retired {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(w.Title+" "+w.Slug+" "+w.Intent), q) {
			continue
		}
		m.rows = append(m.rows, row{ws: w})
	}
	// Newest first (§ sort direction) — Trash orders by close time, Spaces by
	// creation.
	sort.SliceStable(m.rows, func(i, j int) bool {
		if m.screen == scrTrash {
			return m.rows[i].ws.RetiredAt.After(m.rows[j].ws.RetiredAt)
		}
		return m.rows[i].ws.Created.After(m.rows[j].ws.Created)
	})
	if m.sel >= len(m.rows) {
		m.sel = len(m.rows) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
}

// contextWorkspace is the space a delete (M-d) targets — the selected row in the
// Spaces or Trash list. The other views don't delete spaces.
func (m Model) contextWorkspace() *core.Workspace {
	switch m.screen {
	case scrActive, scrTrash:
		if r, ok := m.cur(); ok {
			return r.ws
		}
	}
	return nil
}

// rowWidth is the width a full-row selection band spans — the popup's usable
// content width (matches the PR trailer's right-align target).
func (m Model) rowWidth() int {
	if m.w < 30 {
		return 24
	}
	return m.w - 6
}

func (m Model) Init() tea.Cmd {
	// Opening straight onto the PR view refreshes its statuses (cache-gated, so a
	// reopen within the TTL is instant); the view renders the cache first and
	// updates when the bounded fetch lands.
	if m.screen == scrPRs {
		return tea.Batch(textarea.Blink, m.refreshCurrentCmd(false))
	}
	return textarea.Blink
}

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
	case prMutatedMsg:
		delete(m.prPending, prKey(msg.repo, msg.number))
		if msg.err != nil {
			// The command failed (or raced a stale state) — surface it and pull
			// ground truth so the optimistic flip can't leave the row wrong.
			m.err = msg.err.Error()
			return m, m.refreshCurrentCmd(true)
		}
		m.err = ""
		// Reconcile to the re-queried ground-truth PR (match by repo+number).
		for i := range m.all {
			if m.all[i].Slug != msg.slug {
				continue
			}
			for j := range m.all[i].PRs {
				if m.all[i].PRs[j].Repo == msg.pr.Repo && m.all[i].PRs[j].Number == msg.pr.Number {
					m.all[i].PRs[j] = msg.pr
				}
			}
		}
		_ = core.Update(func(s *core.State) {
			if w := s.Find(msg.slug); w != nil {
				for j := range w.PRs {
					if w.PRs[j].Repo == msg.pr.Repo && w.PRs[j].Number == msg.pr.Number {
						w.PRs[j] = msg.pr
					}
				}
			}
		})
		return m, nil
	case tea.KeyMsg:
		// Global keys work from every screen: C-c closes; M-s/M-p/M-w/M-t/M-n jump
		// to a view; M-d retires the selection; M-x deletes it (with confirm).
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "alt+s":
			m.screen = scrActive
			m.filter.Focus()
			m.rebuild()
			return m, nil
		case "alt+t":
			m.screen = scrTrash
			m.filter.Focus()
			m.rebuild()
			return m, nil
		case "alt+n":
			m.screen = scrNew
			m.intent.Focus()
			return m, textarea.Blink
		case "alt+p":
			m.prSel = 0
			m.screen = scrPRs
			m.filter.SetValue("")
			m.filter.Focus()
			return m, m.refreshCurrentCmd(true) // re-pressing M-p forces a fresh fetch
		case "alt+w":
			m.wtSel = 0
			m.screen = scrWorktrees
			m.filter.SetValue("")
			m.filter.Focus()
			return m, nil
		case "alt+h":
			m.screen = scrHelp
			return m, nil
		case "alt+d":
			// M-d is "delete" only in the space lists: soft in Spaces (move to
			// Trash, reversible), hard in Trash (permanent, behind a confirm).
			// Elsewhere (e.g. the PR view, where M-d means "draft") it falls
			// through to the screen's own handler.
			switch m.screen {
			case scrActive:
				if w := m.contextWorkspace(); w != nil && !w.Retired {
					m.deactivate(*w)
				}
				return m, nil
			case scrTrash:
				if w := m.contextWorkspace(); w != nil {
					m.focusWS = w
					m.returnScreen = m.screen
					m.confirm = m.deletePrompt(w)
					m.screen = scrConfirm
				}
				return m, nil
			}
		}
		switch m.screen {
		case scrActive:
			return m.updateActive(msg)
		case scrTrash:
			return m.updateTrash(msg)
		case scrNew:
			return m.updateNew(msg)
		case scrPRs:
			return m.updatePRs(msg)
		case scrWorktrees:
			return m.updateWorktrees(msg)
		case scrHelp:
			return m.updateHelp(msg)
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
		body, footer = m.viewActive(), m.footer("open", "M-d delete")
	case scrTrash:
		body, footer = m.viewTrash(), m.footer("restore", "M-d delete")
	case scrNew:
		body, footer = m.viewNew(), hintStyle.Render("↵ start (background)  ·  ⌥↵ start & open  ·  ^j newline  ·  esc close")
	case scrPRs:
		body, footer = m.viewPRs(), m.footer("browser", "M-o open", "M-c close", "M-d draft", "M-y copy")
	case scrWorktrees:
		body, footer = m.viewWorktrees(), m.footer("shell")
	case scrHelp:
		body, footer = m.viewHelp(), ""
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

// footer is the bottom shortcut bar: the Enter verb and only this view's own
// actions. Cross-view navigation lives in Help (M-h), so each view shows exactly
// its own legend and nothing more.
func (m Model) footer(verb string, extras ...string) string {
	parts := append([]string{"↵ " + verb}, extras...)
	return hintStyle.Render(strings.Join(parts, "  ·  "))
}

// --- actions ---------------------------------------------------------------

// switchTo moves the outer client to a session and closes the overlay. It
// revives a dead agent session first (a tmux-server restart leaves the record
// but not the session), and selects the agent window so it lands on Claude.
func (m Model) switchTo(session string) (tea.Model, tea.Cmd) {
	if ws := m.bySession(session); ws != nil {
		_ = agent.EnsureClaude(session, ws.Root(), ws.Slug)
		_ = tmux.SetTitle(session, ws.Title) // keep the status line correct (survives revive)
		// Entering a blocked workspace acknowledges it: clear the attention so the
		// badge is "used up" the moment you land (the agent re-flags via its next
		// Notification if it still needs you).
		if agent.Status(session, true) == core.StatusBlocked {
			_ = agent.SetStatus(session, core.StatusIdle)
		}
		// Backfill the live status marker so the bar is right on landing, even for
		// a session that hasn't fired a hook since this build (event-driven push).
		_ = tmux.SetStatusMarker(session, agent.StatusMarker(agent.Status(session, true)))
		agent.RefreshAttention()
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
	// No toast here — the detached builder fires one once the workspace is named.
	return m, tea.Quit
}

// deactivate retires a workspace: kill its session, mark it retired (kept on
// disk, restorable from M-w), and refresh the list.
func (m *Model) deactivate(w core.Workspace) {
	m.moveOuterOff(w.Session)
	_ = tmux.KillSession(w.Session)
	setRetired(w.Slug, true)
	agent.RefreshAttention() // a killed session may have been blocked
	name := w.Title
	if name == "" {
		name = w.Slug
	}
	// Top-right notice, mirroring the creation check-mark: coloured icon + white
	// text (the trash Octicon in the Trash yellow, then the name in white).
	_ = tmux.Flash("#[fg=colour179]"+iconTrash+"#[fg=colour252] "+tmux.EscapeText(name)+" → Trash", 4)
	m.load()
}

// moveOuterOff swaps the outer client to another running workspace (or home) if
// it is currently attached to session — so killing that session doesn't strand
// the client. The overlay (a popup) stays open, so M-a remains visible.
func (m Model) moveOuterOff(session string) {
	if tmux.ClientSession(m.outer) == session {
		_ = tmux.SwitchClient(m.outer, m.fallbackSession(session))
	}
}

// fallbackSession is a live workspace other than exclude, else the home splash.
func (m Model) fallbackSession(exclude string) string {
	for i := range m.all {
		w := &m.all[i]
		if w.Session == exclude || w.Retired {
			continue
		}
		if m.status[w.Session] != core.StatusGone {
			return w.Session
		}
	}
	return "home"
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
			if retired {
				w.RetiredAt = time.Now()
			} else {
				w.RetiredAt = time.Time{}
			}
		}
	})
}

// deleteWorkspace removes worktrees, the session, the directory, and the state
// entry, then refreshes the list in place.
func (m *Model) deleteWorkspace(w core.Workspace) {
	m.moveOuterOff(w.Session)
	for _, t := range m.wt[w.Slug] {
		_ = git.RemoveWorktree(t.Path)
	}
	_ = tmux.KillSession(w.Session)
	_ = os.RemoveAll(w.Root())
	_ = core.RemoveWorkspace(w.Slug)
	agent.RefreshAttention() // a deleted session may have been blocked
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
