package workspaces

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/vyrwu/atelier/internal/tui"
)

// Key-message helpers mirroring what bubbletea delivers for real keys.
func altKey(r rune) tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true} }
func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func row(session, window, display string) SessionRow {
	return SessionRow{Session: session, Window: window, DisplayName: display}
}

func newTestModel(rows ...SessionRow) *sessionsModel {
	// nil client is fine: these tests drive only the pure Update state
	// transitions and never execute the returned I/O Cmds.
	m := newSessionsModel(nil, rows, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(*sessionsModel)
}

func step(m *sessionsModel, msg tea.Msg) *sessionsModel {
	next, _ := m.Update(msg)
	return next.(*sessionsModel)
}

// TestSessionsModel_ConfirmDeleteStateMachine covers the inline delete
// confirmation that replaces the fzf $FZF_PROMPT "Confirm?" hack: M-x arms
// the confirm, n/esc cancels it, y/enter commits (returning a delete Cmd).
func TestSessionsModel_ConfirmDeleteStateMachine(t *testing.T) {
	m := newTestModel(row("vyrwu/demo", "feat-x", "feat-x"))

	m = step(m, altKey('x'))
	if m.mode != sessConfirm {
		t.Fatalf("M-x: mode = %v, want sessConfirm", m.mode)
	}
	if m.confirm.window != "feat-x" || m.confirm.session != "vyrwu/demo" {
		t.Fatalf("confirm target = %+v", m.confirm)
	}

	m = step(m, runeKey('n'))
	if m.mode != sessNormal {
		t.Fatalf("n: mode = %v, want sessNormal", m.mode)
	}

	m = step(m, altKey('x'))
	next, cmd := m.Update(runeKey('y'))
	m = next.(*sessionsModel)
	if m.mode != sessNormal {
		t.Fatalf("y: mode = %v, want sessNormal", m.mode)
	}
	if cmd == nil {
		t.Fatalf("y: expected a delete Cmd, got nil")
	}
}

// TestSessionsModel_ReloadPreservesCursorByIdentity is the live-tick
// contract: a rows refresh re-pins the cursor to the same (session,window)
// even when rows reorder — the bubbletea equivalent of fzf --track.
func TestSessionsModel_ReloadPreservesCursorByIdentity(t *testing.T) {
	m := newTestModel(
		row("s", "a", "a"), row("s", "b", "b"), row("s", "c", "c"),
	)
	m.list.Select(1) // b

	m = step(m, rowsMsg{rows: []SessionRow{
		row("s", "c", "c"), row("s", "b", "b"), row("s", "a", "a"),
	}})

	sel, ok := m.list.SelectedItem().(sessionItem)
	if !ok || sel.row.Window != "b" {
		t.Fatalf("cursor not preserved on b after reorder, got %+v", sel)
	}
}

// TestSessionsModel_ReloadKeepsConfirmTarget: a tick mid-confirm must not
// reset the mode and must keep the highlight on the row being deleted.
func TestSessionsModel_ReloadKeepsConfirmTarget(t *testing.T) {
	m := newTestModel(row("s", "a", "a"), row("s", "b", "b"))
	m.list.Select(1) // b
	m = step(m, altKey('x'))
	if m.mode != sessConfirm {
		t.Fatalf("precondition: not in confirm mode")
	}

	// Reorder so b moves to index 0; confirm must survive and follow b.
	m = step(m, rowsMsg{rows: []SessionRow{row("s", "b", "b"), row("s", "a", "a")}})
	if m.mode != sessConfirm {
		t.Fatalf("tick cleared confirm mode")
	}
	if sel, ok := m.list.SelectedItem().(sessionItem); !ok || sel.row.Window != "b" {
		t.Fatalf("cursor drifted off confirm target, got %+v", sel)
	}
}

// TestSessionsModel_CrossJumpTokens: the M-n/M-r/M-u/M-; keys report the
// jump token the RunE turns into a tui.ExecReplace (become() equivalent).
func TestSessionsModel_CrossJumpTokens(t *testing.T) {
	cases := []struct {
		key  tea.KeyMsg
		want string
	}{
		{altKey('n'), "workspaces/pick"},
		{altKey('r'), "workspaces/recover"},
		{altKey('u'), "workspaces/clone"},
		{altKey(';'), "toolselector/select"},
	}
	for _, tc := range cases {
		m := newTestModel(row("s", "a", "a"))
		next, cmd := m.Update(tc.key)
		m = next.(*sessionsModel)
		if m.Outcome().Key != tc.want {
			t.Errorf("%s → Key %q, want %q", tc.key.String(), m.Outcome().Key, tc.want)
		}
		if cmd == nil {
			t.Errorf("%s: expected tea.Quit cmd", tc.key.String())
		}
	}
}

// TestSessionsModel_CancelAndAccept: Esc cancels (→ ErrCancelled upstream);
// Enter accepts and round-trips the selected identity ("session\x00window").
func TestSessionsModel_CancelAndAccept(t *testing.T) {
	m := newTestModel(row("vyrwu/x", "main", "x"))
	if !step(m, tea.KeyMsg{Type: tea.KeyEsc}).Cancelled() {
		t.Error("Esc should cancel")
	}

	m = newTestModel(row("vyrwu/x", "main", "x"))
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.Outcome().Selection; got != "vyrwu/x\x00main" {
		t.Errorf("Enter selection = %q, want vyrwu/x\\x00main", got)
	}
}

// TestSessionsModel_TagReports: M-t reports Key="tag" with the selected
// identity so the RunE loop can run the nested tag prompt then reopen.
func TestSessionsModel_TagReports(t *testing.T) {
	m := newTestModel(row("vyrwu/x", "main", "x"))
	m = step(m, altKey('t'))
	if got := m.Outcome().Key; got != "tag" {
		t.Errorf("M-t Key = %q, want %q", got, "tag")
	}
	if got := m.Outcome().Selection; got != "vyrwu/x\x00main" {
		t.Errorf("M-t Selection = %q, want vyrwu/x\\x00main", got)
	}
}

// TestSessionItem_FilterValue locks the search scope: the picker searches the
// branch (window), the display name (owner/repo), and the tag — but NEVER the
// recap (mirroring the old fzf --nth=1 name-field scope).
func TestSessionItem_FilterValue(t *testing.T) {
	it := sessionItem{row: SessionRow{
		Session:     "vyrwu/demo",
		Window:      "feat-login",
		DisplayName: "vyrwu/demo",
		Tag:         "billing",
		Recap:       "authenticate the user",
	}}
	fv := it.FilterValue()
	for _, want := range []string{"feat-login", "vyrwu/demo", "billing"} {
		if !strings.Contains(fv, want) {
			t.Errorf("FilterValue %q missing %q", fv, want)
		}
	}
	if strings.Contains(fv, "authenticate") {
		t.Errorf("FilterValue must not include the recap: %q", fv)
	}
}

// TestSessionDelegate_Render checks the two-line row renders the branch and
// recap from structured fields, and marks the selected row with the accent bar.
func TestSessionDelegate_Render(t *testing.T) {
	it := sessionItem{row: SessionRow{
		Session:     "vyrwu/demo",
		Window:      "feat-x",
		DisplayName: "vyrwu/demo",
		Recap:       "recap text",
	}}
	l := list.New([]list.Item{it}, sessionDelegate{accent: tui.SessionsTheme().Accent}, 80, 4)
	var buf bytes.Buffer
	sessionDelegate{accent: tui.SessionsTheme().Accent}.Render(&buf, l, 0, it)

	plain := ansi.Strip(buf.String())
	if !strings.Contains(plain, "feat-x") {
		t.Errorf("row missing branch name: %q", plain)
	}
	if !strings.Contains(plain, "recap text") {
		t.Errorf("row missing recap: %q", plain)
	}
	// index 0 == list index 0 → selected → accent left bar on both lines.
	if !strings.Contains(buf.String(), "▎") {
		t.Errorf("selected row missing accent bar: %q", plain)
	}
}

// TestSessionsModel_PinnedReloadPreservesCursor is the FilterApplied analog of
// TestSessionsModel_ReloadPreservesCursorByIdentity: under a sticky M-p pin
// (pin != "" → the list is FilterApplied), a live reload must still re-pin the
// cursor to the same workspace by identity AND must not flash the list empty
// (the visible set stays populated in the same Update).
func TestSessionsModel_PinnedReloadPreservesCursor(t *testing.T) {
	// Window names carry "feat" so the pin filters to feat-a/feat-b.
	m := newSessionsModel(nil, []SessionRow{
		row("s", "feat-a", "s"), row("s", "feat-b", "s"), row("s", "other", "s"),
	}, "feat")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*sessionsModel)

	m.list.Select(0)
	if sel, ok := m.list.SelectedItem().(sessionItem); !ok || sel.row.Window != "feat-a" {
		t.Fatalf("precondition: want feat-a selected, got %+v", sel)
	}

	// Reload with feat-a moved to the end of the (reordered) rows.
	m = step(m, rowsMsg{rows: []SessionRow{
		row("s", "other", "s"), row("s", "feat-b", "s"), row("s", "feat-a", "s"),
	}})

	if len(m.list.VisibleItems()) == 0 {
		t.Fatal("visible items empty after pinned reload — the list flashed empty")
	}
	if sel, ok := m.list.SelectedItem().(sessionItem); !ok || sel.row.Window != "feat-a" {
		t.Fatalf("pinned cursor not preserved on feat-a, got %+v", sel)
	}
}
