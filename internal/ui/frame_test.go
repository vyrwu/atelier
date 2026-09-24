package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/core"
)

// The whole frame fits the popup, whatever the list length, the terminal size,
// or a note or error under the list. Taller than the popup, Bubble Tea drops
// the top lines — the border and the title go first.
func TestFrameFitsThePopup(t *testing.T) {
	var ws []core.Workspace
	var prs []core.PR
	for i := 0; i < 60; i++ {
		ws = append(ws, core.Workspace{Slug: fmt.Sprintf("s%02d", i), Title: "a space", Created: time.Now(),
			Retired: i%2 == 1, RetiredAt: time.Now()})
		prs = append(prs, core.PR{Repo: fmt.Sprintf("o/r%d", i%3), Number: i + 1, Title: "a pull request", State: core.PROpen, Base: "main"})
	}
	sizes := [][2]int{{80, 24}, {120, 40}, {60, 16}, {200, 50}}
	for _, sz := range sizes {
		for _, scr := range []screen{scrActive, scrTrash, scrPRs, scrWorktrees} {
			for _, extra := range []string{"", "note", "err"} {
				m := Model{w: sz[0], h: sz[1], screen: scr, filter: textinput.New(), all: ws,
					wt: map[string][]core.Worktree{}, prPending: map[string]bool{}}
				m.curWS = &core.Workspace{Slug: "cur", Session: "cur", PRs: prs}
				for i := 0; i < 40; i++ {
					m.wtEntries = append(m.wtEntries, wtEntry{wt: core.Worktree{Repo: fmt.Sprintf("r%d", i%4), Branch: fmt.Sprintf("b%02d", i)}})
				}
				switch extra {
				case "note":
					m.note = "copied link · a pull request"
				case "err":
					m.err = "something went wrong"
				}
				m.rebuild()
				if got := lipgloss.Height(m.View()); got > m.h {
					t.Errorf("screen %d at %dx%d with %q: frame is %d lines, popup is %d", scr, sz[0], sz[1], extra, got, m.h)
				}
			}
		}
	}
}
