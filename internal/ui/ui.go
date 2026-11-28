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
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/gc"
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

// Version is the atelier version, set by main at startup; shown on the splash.
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
	// branch name is shown. wtCount is the worktree tally of each space a list
	// has shown, which is all the lists need; counting is the spaces it lacks
	// that Trash is counting (countCmd).
	wt       map[string][]core.Worktree
	wtCount  map[string]int
	counting map[string]bool
	status   map[string]core.AgentStatus

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
	intent   textarea.Model
	newAgent agent.Kind // the agent the new space starts with (Tab cycles)

	// prs / worktrees / confirm screens
	focusWS      *core.Workspace // set transiently when confirming a delete
	returnScreen screen          // where confirm returns to
	prSel        int
	wtSel        int
	wtLoading    bool            // worktree freshness is being computed
	spin         spinner.Model   // the one activity indicator, shared by the views
	confirm      string          // the confirmation prompt
	confirmSwap  bool            // the confirm is for a swap of focusWS, not a delete
	swapTo       agent.Kind      // the agent the swap hands focusWS to (Tab cycles)
	prPending    map[string]bool // PRs (repo#num) with a state change in flight

	err  string
	note string // transient positive feedback (e.g. "copied link")
}

// Run shows the UI in the popup's pty. outer is the client that opened the popup
// (from @atelier_outer). start names the screen to open on.
func Run(outer, start string) error {
	first := map[string]screen{"trash": scrTrash, "new": scrNew, "prs": scrPRs,
		"worktrees": scrWorktrees, "swap": scrConfirm}[start]
	m := newModel(outer, first)
	switch start {
	case "swap":
		// M-x: straight onto the swap confirm for the space it was pressed in.
		if m.curWS == nil {
			_ = tmux.Notify(outer, "no space here  ·  open a space to swap its agent")
			return nil
		}
		m.askSwap(m.curWS)
		// Nothing to go back to: cancelling closes the popup (see updateConfirm).
		m.returnScreen = scrConfirm
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
	// Opening straight onto worktrees starts their sweep in Init, so the header
	// shows it from the first frame — when there is a sweep to show.
	m.wtLoading = m.screen == scrWorktrees && m.freshnessCmd() != nil
	m.rebuild()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// newModel loads the model for the screen it opens on, so a view that lists no
// spaces doesn't count their worktrees.
func newModel(outer string, first screen) Model {
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

	cfg := core.LoadConfig()
	m := Model{
		outer:     outer,
		cfg:       cfg,
		filter:    fi,
		intent:    ta,
		newAgent:  agent.Of(cfg.Agent),
		spin:      sp,
		wt:        map[string][]core.Worktree{},
		wtCount:   map[string]int{},
		counting:  map[string]bool{},
		status:    map[string]core.AgentStatus{},
		prPending: map[string]bool{},
		screen:    first,
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
	m.status = map[string]core.AgentStatus{}
	for _, w := range m.all {
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
// workspaces. Spaces hides retired ones; Trash shows only those. Worktrees
// live in their own view (M-w), not nested here.
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
	// pinned spaces first, then by attention, then newest first within a rank.
	// EXPERIMENT (the attention rank) — reverting it leaves pins, then plain
	// creation order.
	sort.SliceStable(m.rows, func(i, j int) bool {
		if m.screen == scrTrash {
			return m.rows[i].ws.RetiredAt.After(m.rows[j].ws.RetiredAt)
		}
		if m.screen == scrActive {
			if pi, pj := m.rows[i].ws.Pinned, m.rows[j].ws.Pinned; pi != pj {
				return pi
			}
			ri, rj := m.attentionRank(m.rows[i].ws), m.attentionRank(m.rows[j].ws)
			if ri != rj {
				return ri < rj
			}
		}
		return m.rows[i].ws.Created.After(m.rows[j].ws.Created)
	})
	// Spaces counts its rows' worktrees before it draws: they are few, and its
	// badges never pop in. Trash, which can run to hundreds, counts its own off
	// the draw path (countCmd). Counting walks the directory and reads no
	// branches. A count lasts the popup: only a delete here removes worktrees,
	// and it drops its space's.
	if m.screen == scrActive {
		if m.wtCount == nil {
			m.wtCount = map[string]int{}
		}
		for _, r := range m.rows {
			if _, ok := m.wtCount[r.ws.Slug]; !ok {
				m.wtCount[r.ws.Slug] = git.CountWorktrees(r.ws.Root())
			}
		}
	}
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
// shuffle under the cursor while you are looking at them — only a pin, which
// the cursor follows, moves one.
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

func (m Model) Init() tea.Cmd {
	// PRs render from the cache, which the overlay re-reads every second. The
	// worktree view draws its branches at once and fills in how far each has
	// drifted when that lands.
	if m.screen == scrWorktrees {
		return tea.Batch(textarea.Blink, cacheTickCmd(), m.spin.Tick, m.freshnessCmd())
	}
	return tea.Batch(textarea.Blink, cacheTickCmd(), m.countCmd())
}

// countsMsg is worktree counts by space, counted off the draw path.
type countsMsg map[string]int

// countCmd counts the worktrees of the Trash rows not counted yet, so Trash
// draws at once and its badges fill in: counting a Trash of hundreds in turn
// took most of a second. The walks wait on the filesystem, so several run at
// once.
func (m Model) countCmd() tea.Cmd {
	if m.screen != scrTrash || m.counting == nil {
		return nil
	}
	roots := map[string]string{}
	for _, r := range m.rows {
		if _, ok := m.wtCount[r.ws.Slug]; !ok && !m.counting[r.ws.Slug] {
			roots[r.ws.Slug] = r.ws.Root()
			m.counting[r.ws.Slug] = true
		}
	}
	if len(roots) == 0 {
		return nil
	}
	return func() tea.Msg {
		out := countsMsg{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for slug, root := range roots {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				n := git.CountWorktrees(root)
				<-sem
				mu.Lock()
				out[slug] = n
				mu.Unlock()
			}()
		}
		wg.Wait()
		return out
	}
}

// loading reports whether anything the current view is waiting on is still in
// flight — what keeps the spinner ticking, and what stops it.
func (m Model) loading() bool { return m.wtLoading }

// Update handles msg, then has Trash count any rows it lacks, whichever path
// (opening, filtering, a delete or restore) put them on the list.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if counts, ok := msg.(countsMsg); ok {
		// A count a delete has since dropped (counting cleared) is stale.
		for slug, n := range counts {
			if m.counting[slug] {
				m.wtCount[slug] = n
				delete(m.counting, slug)
			}
		}
		return m, nil
	}
	next, cmd := m.update(msg)
	if nm, ok := next.(Model); ok {
		return nm, tea.Batch(cmd, nm.countCmd())
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case cacheTickMsg:
		// The cache can reorder the list; the cursor stays on its PR.
		flat := m.flatPRs()
		var held string
		if m.prSel >= 0 && m.prSel < len(flat) {
			held = prKey(flat[m.prSel].Repo, flat[m.prSel].Number)
		}
		m.applyCache(core.Load())
		for i, pr := range m.flatPRs() {
			if prKey(pr.Repo, pr.Number) == held {
				m.prSel = i
			}
		}
		return m, cacheTickCmd()
	case wtFreshMsg:
		m.wtLoading = false
		m.applyFreshness(msg)
		return m, nil
	case prMutatedMsg:
		delete(m.prPending, prKey(msg.repo, msg.number))
		if msg.err != nil {
			// The command failed (or raced a stale state) — surface it; the next
			// cache tick puts the row back to what GitHub last said.
			m.err = msg.err.Error()
			return m, nil
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
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "alt+s", "alt+t", "alt+n", "alt+p", "alt+w", "alt+h":
			m.clearConfirmation()
		}
		// Global keys work from every screen: C-c closes; M-s/M-p/M-w/M-t/M-n jump
		// to a view; M-h goes home; M-d retires the selection in Spaces and deletes
		// it (with confirm) in Trash; M-x swaps the selection's agent in Spaces.
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
			return m, nil
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
		if msg.String() == "alt+x" {
			// M-x swaps an agent only from a Spaces row. Everywhere else it is
			// swallowed, or a text field would take it as an "x".
			if w := m.contextWorkspace(); w != nil && m.screen == scrActive && !w.Retired {
				m.askSwap(w)
				m.returnScreen = scrActive
			}
			return m, nil
		}
		if msg.String() == "alt+P" && m.screen != scrActive {
			// M-P focuses a Spaces row; anywhere else a text field would type "P".
			return m, nil
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
	if m.screen == scrConfirm && m.confirmSwap {
		// Opened by M-x, the popup is sized to the dialog and is all of it;
		// from a Spaces row, the dialog sits in the middle of the overlay.
		if m.returnScreen == scrConfirm {
			return m.swapDialog(m.w, m.h)
		}
		inner := lipgloss.Place(m.w-4, m.h-2, lipgloss.Center, lipgloss.Center,
			m.swapDialog(min(m.w-4, swapDialogW), min(m.h-2, swapDialogH)))
		return frameStyle.Width(m.w - 2).Height(m.h - 2).Render(inner)
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
		// One label for both ways: a footer that changed with the row could wrap
		// on one row and not the next, and the list's height would jump with it.
		return m.footer("open", "M-P focus", "M-x swap agent", "M-d delete")
	case scrTrash:
		return m.footer("restore", "M-d delete")
	case scrNew:
		return hintStyle.Render("↵ start (background)  ·  ⌥↵ start & open  ·  tab switch agent  ·  ^j newline  ·  esc close")
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
// actions. Cross-view navigation lives on the splash (M-h), so each view shows exactly
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
// but not the session), and selects the agent window so it lands on the agent.
func (m Model) switchTo(session string) (tea.Model, tea.Cmd) {
	if ws := m.bySession(session); ws != nil {
		_ = agent.EnsureAgent(*ws)
		_ = tmux.SetTitle(session, ws.Title) // keep the status line correct (survives revive)
		_ = tmux.SetAgentName(session, agent.Of(ws.Agent).Label())
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
	if tmux.SelectWindow(session, agent.Window) != nil {
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
	args := []string{"create", intent, "--outer", m.outer, "--agent", string(m.newAgent)}
	if switchIn {
		args = append(args, "--switch")
	}
	spawnDetached(args...)
	// No toast here — the detached builder fires one once the workspace is named.
	return m, tea.Quit
}

// askSwap puts up the confirm for handing w to another agent.
func (m *Model) askSwap(w *core.Workspace) {
	m.focusWS = w
	m.swapTo = agent.Of(w.Agent).Next()
	m.confirmSwap = true
	m.screen = scrConfirm
}

// deactivate retires a workspace: kill its session, mark it retired (kept on
// disk, restorable from Trash, M-t), and refresh the list.
func (m *Model) deactivate(w core.Workspace) {
	m.moveOuterOff(w.Session)
	_ = tmux.KillSession(w.Session)
	if err := setRetired(w.Slug, true); err != nil {
		m.err = err.Error()
	}
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

// reactivate restores a retired workspace and switches into it (resuming its agent).
func (m Model) reactivate(w core.Workspace) (tea.Model, tea.Cmd) {
	if err := setRetired(w.Slug, false); err != nil {
		m.err = err.Error()
		m.load()
		return m, nil
	}
	return m.switchTo(w.Session)
}

// setPinned pins or unpins a space in the state file.
func setPinned(slug string, pinned bool) error {
	found := false
	err := core.Update(func(s *core.State) {
		if w := s.Find(slug); w != nil {
			found, w.Pinned = true, pinned
		}
	})
	if err == nil && !found {
		err = fmt.Errorf("space %q no longer exists", slug)
	}
	return err
}

func setRetired(slug string, retired bool) error {
	found := false
	var statusErr error
	err := core.Update(func(s *core.State) {
		if w := s.Find(slug); w != nil {
			found = true
			if !retired {
				if statusErr = agent.SetStatus(w.Session, core.StatusIdle); statusErr != nil {
					return
				}
			}
			w.Retired = retired
			if retired {
				w.RetiredAt = time.Now()
			} else {
				w.RetiredAt = time.Time{}
			}
		}
	})
	if err != nil {
		return err
	}
	if statusErr != nil {
		return statusErr
	}
	if !found {
		return fmt.Errorf("space %q no longer exists", slug)
	}
	return nil
}

// deleteWorkspace deletes w for good (gc.Delete), then refreshes the list in
// place.
func (m *Model) deleteWorkspace(w core.Workspace) {
	m.moveOuterOff(w.Session)
	// A delete that fails partway leaves fewer worktrees: count them again.
	delete(m.wtCount, w.Slug)
	delete(m.counting, w.Slug)
	if err := gc.Delete(w); err != nil {
		m.err = "delete " + w.Slug + ": " + err.Error()
	}
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
	// #nosec G204 G702 -- Starts this executable with an argument array, without a shell.
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
