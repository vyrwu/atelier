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

	"github.com/charmbracelet/bubbles/spinner"
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
	all []core.Workspace
	// wt holds the CURRENT space's worktrees, with branches — the only place a
	// branch name is shown. wtCount is every space's worktree tally, which is all
	// the lists need, and costs a directory walk instead of a git process each.
	wt      map[string][]core.Worktree
	wtCount map[string]int
	status  map[string]core.AgentStatus

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
	prLoading    bool            // a PR sweep is in flight — the rows are the previous one
	wtLoading    bool            // worktree freshness is being computed
	prPollGen    int             // identifies the live PR poll chain
	spin         spinner.Model   // the one activity indicator, shared by the views
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
	// Opening straight onto the PR view queries GitHub at once (see Init), so the
	// header says so from the first frame. Set here, not in newModel: the start
	// screen is chosen above, after the model is built.
	m.prLoading = m.queriesOnOpen()
	// Opening straight onto worktrees starts their sweep in Init, so the header
	// shows it from the first frame — when there is a sweep to show.
	m.wtLoading = m.screen == scrWorktrees && m.freshnessCmd() != nil
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

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(cAccent)

	m := Model{
		outer:     outer,
		cfg:       core.LoadConfig(),
		filter:    fi,
		intent:    ta,
		spin:      sp,
		wt:        map[string][]core.Worktree{},
		wtCount:   map[string]int{},
		status:    map[string]core.AgentStatus{},
		prPending: map[string]bool{},
	}
	m.load()
	// Resolve the current space from the client that opened the overlay, and cache
	// its worktrees + freshness for the space-scoped views.
	m.curWS = m.bySession(tmux.ClientSession(outer))
	m.loadCurrentWorktrees()
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
	m.wtCount = map[string]int{}
	m.status = map[string]core.AgentStatus{}
	for _, w := range m.all {
		// Counting walks the directory; naming the branches would spawn a git
		// process per worktree, and across every space that was seconds of blank
		// popup before the first frame.
		m.wtCount[w.Slug] = git.CountWorktrees(w.Root())
		m.status[w.Session] = agent.Status(w.Session, live[w.Session])
	}
	// load() re-allocates m.all, so re-resolve the current space into the fresh
	// slice — a cached *Workspace would dangle into the old backing array and
	// freeze the space-scoped PR/worktree views after a retire/delete.
	if m.curWS != nil {
		m.curWS = m.bySession(m.curWS.Session)
		m.loadCurrentWorktrees()
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
	// Trash is newest-closed first (§ sort direction). Spaces is the exception:
	// it orders by attention, then newest first within a rank. EXPERIMENT — one
	// revert restores plain creation order.
	sort.SliceStable(m.rows, func(i, j int) bool {
		if m.screen == scrTrash {
			return m.rows[i].ws.RetiredAt.After(m.rows[j].ws.RetiredAt)
		}
		if m.screen == scrActive {
			ri, rj := m.attentionRank(m.rows[i].ws), m.attentionRank(m.rows[j].ws)
			if ri != rj {
				return ri < rj
			}
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

// attentionRank orders the Spaces list by how much a space wants you: blocked
// asks for you now, idle has finished and is waiting to be closed out, working
// needs nothing, and a dead session is last. Ordering is computed once per
// overlay open (statuses are read at load and nothing polls), so rows never
// shuffle under the cursor while you are looking at them.
func (m Model) attentionRank(w *core.Workspace) int {
	switch m.status[w.Session] {
	case core.StatusBlocked:
		return 0
	case core.StatusIdle:
		return 1
	case core.StatusWorking:
		return 2
	default: // gone
		return 3
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

// queriesOnOpen reports whether opening the overlay fires a PR query — the one
// predicate behind both the query (Init) and the "checking github…" marker (Run),
// so the header can never claim a query that wasn't made, or stay silent during
// one that was.
func (m Model) queriesOnOpen() bool { return m.screen == scrPRs && m.curWS != nil }

func (m Model) Init() tea.Cmd {
	// Opening straight onto the PR view always re-queries GitHub; the view renders
	// the last sweep (marked as such) and replaces it when this one lands. The
	// worktree view likewise draws its branches at once and fills in how far each
	// has drifted when that lands.
	switch {
	case m.queriesOnOpen():
		// Run already opened the chain; schedule the generation the model holds,
		// or the first tick arrives stale and the poll dies silently.
		return tea.Batch(textarea.Blink, m.spin.Tick, m.refreshCurrentCmd(), prPollCmd(m.prPollGen))
	case m.screen == scrWorktrees:
		return tea.Batch(textarea.Blink, m.spin.Tick, m.freshnessCmd())
	}
	return textarea.Blink
}

// loading reports whether anything the current view is waiting on is still in
// flight — what keeps the spinner ticking, and what stops it.
func (m Model) loading() bool { return m.prLoading || m.wtLoading }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.intent.SetWidth(min(msg.Width-6, 100))
		m.intent.SetHeight(min(msg.Height-10, 10))
		return m, nil
	case spinner.TickMsg:
		if !m.loading() {
			return m, nil // let the animation stop rather than spin on an idle view
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case prPollMsg:
		// A tick from a retired chain, or one that arrived after you left the
		// view, dies here rather than re-arming.
		if msg.gen != m.prPollGen || m.screen != scrPRs || m.curWS == nil {
			return m, nil
		}
		m.prLoading = true
		return m, tea.Batch(m.spin.Tick, m.refreshCurrentCmd(), prPollCmd(msg.gen))
	case wtFreshMsg:
		m.wtLoading = false
		m.applyFreshness(msg)
		return m, nil
	case prsRefreshedMsg:
		m.prLoading = false
		// Merged into what is on disk now rather than written over it: a PR
		// registered after this query began is not in its result, and must not
		// be erased by it.
		prs := msg.prs
		_ = core.Update(func(s *core.State) {
			if w := s.Find(msg.slug); w != nil {
				w.PRs = keepRegistered(msg.prs, w.PRs)
				w.PRsRefreshed = time.Now()
				prs = w.PRs
			}
		})
		for i := range m.all {
			if m.all[i].Slug == msg.slug {
				m.all[i].PRs = prs
			}
		}
		return m, nil
	case prMutatedMsg:
		delete(m.prPending, prKey(msg.repo, msg.number))
		if msg.err != nil {
			// The command failed (or raced a stale state) — surface it and pull
			// ground truth so the optimistic flip can't leave the row wrong.
			m.err = msg.err.Error()
			m.prLoading = true
			return m, tea.Batch(m.spin.Tick, m.refreshCurrentCmd())
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
			m.prLoading = true
			// Re-pressing M-p re-queries, and restarts the poll from now. The
			// chain is opened before the return so the model that goes back
			// carries the generation its tick will quote.
			poll := m.startPRPoll()
			return m, tea.Batch(m.spin.Tick, m.refreshCurrentCmd(), poll)
		case "alt+w":
			m.wtSel = 0
			m.screen = scrWorktrees
			m.filter.SetValue("")
			m.filter.Focus()
			// Only a sweep that will actually run may raise the spinner: with no
			// worktrees there is no result coming to lower it.
			cmd := m.freshnessCmd()
			m.wtLoading = cmd != nil
			if cmd == nil {
				return m, nil
			}
			return m, tea.Batch(m.spin.Tick, cmd)
		case "alt+h":
			// The popup owns the keyboard while it is open, so the tmux binding
			// can't see this — the overlay has to make the same move itself.
			return m.goHome()
		case "alt+d":
			// M-d is "delete" only in the space lists: soft in Spaces (move to
			// Trash, reversible), hard in Trash (permanent, behind a confirm).
			// Elsewhere it falls through to the screen's own handler.
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
	var body string
	switch m.screen {
	case scrActive:
		body = m.viewActive()
	case scrTrash:
		body = m.viewTrash()
	case scrNew:
		body = m.viewNew()
	case scrPRs:
		body = m.viewPRs()
	case scrWorktrees:
		body = m.viewWorktrees()
	case scrConfirm:
		body = m.viewConfirm()
	}
	footer := m.screenFooter()
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

// screenFooter is the shortcut bar under the current screen. The lists size
// themselves from it (listHeight), so this is the only place it is spelled out.
func (m Model) screenFooter() string {
	switch m.screen {
	case scrActive:
		return m.footer("open", "M-d delete")
	case scrTrash:
		return m.footer("restore", "M-d delete")
	case scrNew:
		return hintStyle.Render("↵ start (background)  ·  ⌥↵ start & open  ·  ^j newline  ·  esc close")
	case scrPRs:
		return m.footer("diff", "M-e checks", "M-b browser", "M-o open", "M-c close", "M-y copy")
	case scrWorktrees:
		return m.footer("shell")
	case scrConfirm:
		return hintStyle.Render("y delete  ·  n cancel")
	}
	return ""
}

// footer is the bottom shortcut bar: the Enter verb and only this view's own
// actions. Cross-view navigation lives in Help (M-h), so each view shows exactly
// its own legend and nothing more.
func (m Model) footer(verb string, extras ...string) string {
	parts := append([]string{"↵ " + verb}, extras...)
	return hintStyle.Render(strings.Join(parts, "  ·  "))
}

// --- actions ---------------------------------------------------------------

// goHome closes the overlay and puts the outer client on the landing splash,
// creating it if it is gone — the same move M-h makes from a space, for when
// M-h is pressed with the overlay up.
func (m Model) goHome() (tea.Model, tea.Cmd) {
	self, err := os.Executable()
	if err != nil {
		self = "atelier"
	}
	dir, _ := os.UserHomeDir()
	if err := tmux.EnsureHome(dir, self+" home"); err != nil {
		m.err = err.Error()
		return m, nil
	}
	_ = tmux.SwitchClient(m.outer, tmux.HomeSession)
	return m, tea.Quit
}

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
	// Derived for this space, not read from the model — removing the directory
	// without removing the worktrees would leave the source repos registering
	// checkouts that no longer exist.
	for _, t := range git.Worktrees(w.Root()) {
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
