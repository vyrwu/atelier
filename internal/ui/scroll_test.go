package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/vyrwu/atelier/internal/core"
)

func TestWindowFitsWithoutScrolling(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got, above, below := window(lines, 1, 10)
	if len(got) != 3 || above != 0 || below != 0 {
		t.Errorf("short list scrolled: %v %d %d", got, above, below)
	}
}

// The selection drives the scroll, so it is always on screen — however far down
// the list it is.
func TestWindowKeepsSelectionVisible(t *testing.T) {
	lines := make([]string, 35)
	for i := range lines {
		lines[i] = string(rune('a' + i%26))
	}
	for _, sel := range []int{0, 1, 17, 33, 34} {
		got, above, below := window(lines, sel, 10)
		if len(got) != 10 {
			t.Fatalf("sel %d: window is %d lines, want 10", sel, len(got))
		}
		if above+len(got)+below != len(lines) {
			t.Errorf("sel %d: %d+%d+%d doesn't account for %d lines", sel, above, len(got), below, len(lines))
		}
		if sel < above || sel >= above+len(got) {
			t.Errorf("sel %d scrolled out of view (window covers %d..%d)", sel, above, above+len(got)-1)
		}
	}
}

// At the ends the window stops rather than running past the list.
func TestWindowClampsAtTheEnds(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4"}
	if got, above, below := window(lines, 0, 3); above != 0 || below != 2 || got[0] != "0" {
		t.Errorf("top: %v above=%d below=%d", got, above, below)
	}
	if got, above, below := window(lines, 4, 3); above != 2 || below != 0 || got[2] != "4" {
		t.Errorf("bottom: %v above=%d below=%d", got, above, below)
	}
	if got, _, _ := window(lines, 2, 0); len(got) != 1 {
		t.Errorf("zero height: %v, want one line", got)
	}
}

// 35 retired spaces in a 24-line popup used to render 35 rows, push the title
// and filter off the top, and leave no way back to them.
func TestTrashDoesNotOverflowTheFrame(t *testing.T) {
	var rows []core.Workspace
	for i := 0; i < 35; i++ {
		rows = append(rows, core.Workspace{
			Slug:      "space-" + string(rune('a'+i%26)),
			Title:     "a retired space",
			Retired:   true,
			RetiredAt: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}
	m := Model{w: 100, h: 24, screen: scrTrash, filter: textinput.New(), wt: map[string][]core.Worktree{}, all: rows}
	m.rebuild()

	body := m.viewTrash()
	if n := len(m.rows); n != 35 {
		t.Fatalf("rebuild kept %d rows, want 35", n)
	}
	if got, limit := strings.Count(body, "\n"), m.h-2; got > limit {
		t.Errorf("body is %d lines, more than the %d the frame has", got, limit)
	}
	if !strings.Contains(body, "Trash") {
		t.Error("title pushed out of the body")
	}
	if !strings.Contains(body, "more") {
		t.Error("no indication that the list continues past the window")
	}

	// Selecting the last row scrolls it into view — and the header stays.
	m.sel = 34
	body = m.viewTrash()
	if !strings.Contains(ansiRe.ReplaceAllString(body, ""), m.rows[34].ws.Title) {
		t.Error("last row not reachable")
	}
	if !strings.Contains(body, "Trash") {
		t.Error("title lost when scrolled to the bottom")
	}
	if got, limit := strings.Count(body, "\n"), m.h-2; got > limit {
		t.Errorf("scrolled body is %d lines, more than the %d the frame has", got, limit)
	}
}

// Spaces, PRs, and Worktrees share the same windowing, so none of them can
// outgrow the frame either.
func TestListsFitTheFrame(t *testing.T) {
	var ws []core.Workspace
	var prs []core.PR
	for i := 0; i < 40; i++ {
		ws = append(ws, core.Workspace{Slug: "s" + string(rune('a'+i%26)), Title: "space", Created: time.Now()})
		prs = append(prs, core.PR{Repo: "o/r", Number: i, Title: "a pull request", State: core.PROpen})
	}
	m := Model{w: 100, h: 24, filter: textinput.New(), wt: map[string][]core.Worktree{}, all: ws}

	m.screen = scrActive
	m.rebuild()
	if got, limit := strings.Count(m.viewActive(), "\n"), m.h-2; got > limit {
		t.Errorf("Spaces body is %d lines, more than %d", got, limit)
	}

	m.screen = scrPRs
	m.curWS = &core.Workspace{Slug: "s", Session: "s", PRs: prs}
	if got, limit := strings.Count(m.viewPRs(), "\n"), m.h-2; got > limit {
		t.Errorf("PRs body is %d lines, more than %d", got, limit)
	}
}

// The space lists show how many worktrees a space has, never their names, so
// the model must not resolve branches for every space — that was a git process
// per worktree (166 of them here) before the overlay drew its first frame.
func TestListsUseCountsNotBranches(t *testing.T) {
	m := Model{
		w: 100, h: 24, screen: scrActive, filter: textinput.New(),
		wt:      map[string][]core.Worktree{}, // only the current space is ever in here
		wtCount: map[string]int{"a": 3, "b": 0},
		all: []core.Workspace{
			{Slug: "a", Title: "has worktrees", Created: time.Now()},
			{Slug: "b", Title: "has none", Created: time.Now().Add(-time.Hour)},
		},
	}
	m.rebuild()

	body := ansiRe.ReplaceAllString(m.viewActive(), "")
	if !strings.Contains(body, "3"+iconWorktree) {
		t.Errorf("worktree count missing from the row:\n%s", body)
	}
	if strings.Contains(body, "0"+iconWorktree) {
		t.Error("a space with no worktrees drew a zero badge")
	}
}

// The counters never cover a row. Once the list scrolls, the selection sits on
// an edge of the window, and a counter drawn over that edge hid exactly the row
// being selected — every position must show its selection and stay in height.
func TestWriteListNeverHidesTheSelection(t *testing.T) {
	lines := make([]string, 35)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%02d", i)
	}
	const height = 10
	for sel := range lines {
		var b strings.Builder
		writeList(&b, lines, sel, height)
		out := b.String()
		if !strings.Contains(out, lines[sel]+"\n") {
			t.Errorf("sel %d: selected row not drawn:\n%s", sel, out)
		}
		if n := strings.Count(out, "\n"); n > height {
			t.Errorf("sel %d: %d lines, more than the %d available", sel, n, height)
		}
		if sel > 0 && sel < len(lines)-1 && !strings.Contains(out, "more") {
			t.Errorf("sel %d: truncated list shows no counter", sel)
		}
	}
}
