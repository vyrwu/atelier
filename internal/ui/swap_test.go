package ui

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/core"
)

func swapSpace() *core.Workspace {
	return &core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
		{Repo: "o/api", Number: 7, URL: "https://github.com/o/api/pull/7", Head: "feat", Base: "main"},
		{Repo: "o/site.dk", Number: 12, URL: "https://github.com/o/site.dk/pull/12", Head: "x", Base: "main"},
	}}
}

// M-e in a PR's diff window opens that PR's checks — the same PR, found back
// from the window's name. No worktree lookup is needed for it.
func TestSwapDiffToChecks(t *testing.T) {
	ws := swapSpace()
	noWorktrees := func() []core.Worktree { t.Fatal("resolved worktrees for the checks view"); return nil }
	name, cwd, cmd, _, ok := prViewSwap(ws, noWorktrees, "pr-api-7", ChecksView)
	if !ok || name != "ci-api-7" || cwd != ws.Root() {
		t.Fatalf("got %q in %q (ok=%v), want ci-api-7 in the space root", name, cwd, ok)
	}
	if !strings.Contains(cmd, "'https://github.com/o/api/pull/7'") {
		t.Errorf("checks command is not for #7: %q", cmd)
	}
}

// M-d in a PR's checks window opens its diff — for a repo whose name had to be
// sanitized into the window name too.
func TestSwapChecksToDiff(t *testing.T) {
	ws := swapSpace()
	name, _, cmd, _, ok := prViewSwap(ws, func() []core.Worktree { return nil }, "ci-site-dk-12", DiffView)
	if !ok || name != "pr-site-dk-12" {
		t.Fatalf("got %q (ok=%v), want pr-site-dk-12", name, ok)
	}
	if !strings.Contains(cmd, "gh pr diff 12 --repo 'o/site.dk'") {
		t.Errorf("diff command is not for #12: %q", cmd)
	}
}

// A window that is not the matching view of any PR is left alone, so the key
// reaches the program instead. The direction matters: M-d in a diff window, or
// M-e in a checks window, is not a swap.
func TestSwapLeavesOtherWindowsAlone(t *testing.T) {
	ws := swapSpace()
	none := func() []core.Worktree { return nil }
	for _, c := range []struct {
		window string
		to     PRView
	}{
		{"pr-bot-main", ChecksView}, // a worktree shell whose repo starts "pr-"
		{"ci-scripts-v2", DiffView}, // likewise "ci-"
		{"pr-api-7", DiffView},      // already the diff
		{"ci-api-7", ChecksView},    // already the checks
		{"pr-api-99", ChecksView},   // a PR this space doesn't have
		{"claude", ChecksView},
	} {
		if _, _, _, _, ok := prViewSwap(ws, none, c.window, c.to); ok {
			t.Errorf("%q toward %d was treated as a swap", c.window, c.to)
		}
	}
}
