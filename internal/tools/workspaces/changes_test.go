package workspaces

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

func TestBuildChangeRows_SortAndDedup(t *testing.T) {
	// Isolated statestore under a temp cache home.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := &statestore.State{
		Workspaces: []statestore.Workspace{
			{
				SessionName: "ws-a", Title: "A",
				PRs: []statestore.PR{
					{Number: 3, Repo: "o/a", State: "closed", UpdatedAt: 100},
					{Number: 1, Repo: "o/a", State: "open", UpdatedAt: 200},
				},
			},
			{
				SessionName: "ws-b", Title: "B",
				PRs: []statestore.PR{
					{Number: 2, Repo: "o/b", State: "open", UpdatedAt: 300},
					{Number: 1, Repo: "o/a", State: "open", UpdatedAt: 200}, // dup of ws-a's #1
				},
			},
		},
	}
	if err := statestore.Save(st); err != nil {
		t.Fatal(err)
	}
	rows := buildChangeRows()
	// 3 distinct PRs (o/a#1 deduped): open first (newest-updated within state), then closed.
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(rows), rows)
	}
	// Row 0 and 1 are the two open PRs (o/b#2 newer than o/a#1), row 2 is the closed one.
	if rows[0].Repo != "o/b" || rows[0].Number != 2 {
		t.Errorf("row0 = %s#%d, want o/b#2", rows[0].Repo, rows[0].Number)
	}
	if rows[2].Number != 3 {
		t.Errorf("row2 = #%d, want the closed #3 last", rows[2].Number)
	}
}

func TestFormatChangeDisplayAndTitle(t *testing.T) {
	pr := statestore.PR{Number: 42, Repo: "o/r", State: "open", CI: "pass", ReviewDecision: "approved", Comments: 5}
	disp := formatChangeDisplay(pr)
	if !strings.Contains(disp, "o/r") || !strings.Contains(disp, "#42") {
		t.Errorf("display missing repo/number: %q", disp)
	}
	if !strings.Contains(disp, "5") {
		t.Errorf("display missing comment count: %q", disp)
	}
	title := formatChangeTitle("Add the endpoint")
	if !strings.Contains(title, "Add the endpoint") || !strings.HasPrefix(title, "\n") {
		t.Errorf("title line = %q", title)
	}
	// Empty title still reserves a second line.
	if !strings.Contains(formatChangeTitle(""), zeroWidthSpace) {
		t.Error("empty title must reserve height with a zero-width space")
	}
}
