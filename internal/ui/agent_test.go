package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

// Tab on the new-space prompt flips which agent the space starts with, rather
// than reaching the text.
func TestNewSpaceTabTogglesAgent(t *testing.T) {
	m := Model{newAgent: agent.Claude, intent: textarea.New()}
	next, _ := m.updateNew(tea.KeyMsg{Type: tea.KeyTab})
	if got := next.(Model).newAgent; got != agent.Codex {
		t.Errorf("after Tab agent = %q, want codex", got)
	}
	if v := ansiRe.ReplaceAllString(next.(Model).viewNew(), ""); !strings.Contains(v, "What can "+glyphCodex+" Codex do for you?") {
		t.Errorf("prompt = %q, want it to ask Codex", v)
	}
	next, _ = next.(Model).updateNew(tea.KeyMsg{Type: tea.KeyTab})
	if got := next.(Model).newAgent; got != agent.Claude {
		t.Errorf("after Tab twice agent = %q, want claude", got)
	}
}

// A Spaces row shows which agent the space runs; a space from before there was
// a choice runs Claude.
func TestRowShowsTheAgent(t *testing.T) {
	m := Model{w: 120}
	for a, glyph := range map[string]string{"": glyphClaude, "claude": glyphClaude, "codex": glyphCodex} {
		ws := &core.Workspace{Slug: "brave-otter", Agent: a, Created: time.Now()}
		if line := m.renderRow(row{ws: ws}, false); !strings.Contains(line, glyph) {
			t.Errorf("agent %q: row %q lacks %s", a, line, glyph)
		}
	}
}

// M-x on a Spaces row asks before swapping that space's agent, naming both
// agents; cancelling returns to the list.
func TestSwapAsksFromASpacesRow(t *testing.T) {
	ws := core.Workspace{Slug: "brave-otter", Session: "brave-otter", Title: "Fix login", Agent: "codex"}
	m := Model{w: 120, h: 40, screen: scrActive, all: []core.Workspace{ws}, status: map[string]core.AgentStatus{}}
	m.rebuild()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}, Alt: true})
	got := next.(Model)
	if got.screen != scrConfirm || !got.confirmSwap {
		t.Fatalf("M-x on a row: screen %v, confirmSwap %v; want the swap confirm", got.screen, got.confirmSwap)
	}
	if v := ansiRe.ReplaceAllString(got.View(), ""); !strings.Contains(v, "Swap to "+glyphClaude+" Claude Code?") {
		t.Errorf("confirm = %q, want it to name the agent it swaps to", v)
	}
	next, _ = got.updateConfirm(tea.KeyMsg{Type: tea.KeyEsc})
	if back := next.(Model); back.screen != scrActive || back.confirmSwap {
		t.Errorf("cancel left screen %v, confirmSwap %v", back.screen, back.confirmSwap)
	}
}

// Tab in the swap dialog flips between the other agent and a new conversation
// with the same one, and Enter swaps to whichever is shown.
func TestSwapDialogTabTogglesTheTarget(t *testing.T) {
	ws := core.Workspace{Slug: "brave-otter", Session: "brave-otter"}
	m := Model{w: swapDialogW, h: swapDialogH, returnScreen: scrConfirm, status: map[string]core.AgentStatus{}}
	m.askSwap(&ws)
	if m.swapTo != agent.Codex {
		t.Fatalf("swap from Claude defaults to %q, want codex", m.swapTo)
	}
	next, _ := m.updateConfirm(tea.KeyMsg{Type: tea.KeyTab})
	got := next.(Model)
	if got.swapTo != agent.Claude {
		t.Errorf("after Tab swapTo = %q, want claude", got.swapTo)
	}
	if v := ansiRe.ReplaceAllString(got.View(), ""); !strings.Contains(v, "new Claude Code?") || !strings.Contains(v, "a new conversation") || !strings.Contains(v, "tab switch agent") {
		t.Errorf("same-agent dialog = %q", v)
	}
	for _, status := range []core.AgentStatus{core.StatusIdle, core.StatusWorking} {
		got.status[ws.Session] = status
		if h := lipgloss.Height(got.View()); h > swapDialogH {
			t.Errorf("status %q: dialog is %d lines, popup is %d", status, h, swapDialogH)
		}
	}
	next, _ = got.updateConfirm(tea.KeyMsg{Type: tea.KeyTab})
	if next.(Model).swapTo != agent.Codex {
		t.Error("a second Tab must return to the other harness")
	}
}

// M-x anywhere but a Spaces row does nothing — in particular it doesn't type an
// "x" into a filter.
func TestSwapKeyIsSwallowedElsewhere(t *testing.T) {
	m := Model{screen: scrTrash, filter: textinput.New(), status: map[string]core.AgentStatus{}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}, Alt: true})
	got := next.(Model)
	if got.screen != scrTrash || got.filter.Value() != "" {
		t.Errorf("M-x in Trash: screen %v, filter %q", got.screen, got.filter.Value())
	}
}
