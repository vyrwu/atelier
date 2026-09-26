package ui

import (
	"fmt"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/tmux"
)

// PRView is one of the two windows a PR opens in from the PR view.
type PRView int

const (
	DiffView   PRView = iota // ↵ in the PR view, M-d from its checks: the PR's changes
	ChecksView               // M-e in the PR view or from its diff: runs, jobs, logs
)

// SwitchPRView moves between a PR's two windows without going back through the
// PR view — M-e in its diff window goes to its checks, M-d in its checks window
// back to the diff — opening the other one if it isn't open yet. ok is false
// when window isn't the right view of any PR in the space (a worktree shell
// whose repo happens to start "pr-", say), so the key can reach the program.
func SwitchPRView(session, window string, to PRView) (ok bool, err error) {
	ws := core.Load().FindBySession(session)
	if ws == nil {
		return false, nil
	}
	name, cwd, cmd, stale, ok := prViewSwap(ws, func() []core.Worktree { return git.Worktrees(ws.Root()) }, window, to)
	if !ok {
		return false, nil
	}
	if cmd == "" {
		return true, fmt.Errorf("no way to show %s", name)
	}
	if !tmux.HasWindow(session, name) {
		if err := tmux.NewWindowCmd(session, cwd, name, cmd, nil); err != nil {
			return true, err
		}
		if stale {
			_ = tmux.Notify("", "this PR moved on GitHub — showing what the worktree has")
		}
	}
	return true, tmux.SelectWindow(session, name)
}

// prViewSwap finds the PR whose other view window is and returns the view asked
// for: its name, where to run it, and what to run. Worktrees are resolved only
// when the diff is the one being opened, since that costs a git call each.
func prViewSwap(ws *core.Workspace, worktrees func() []core.Worktree, window string, to PRView) (name, cwd, cmd string, stale, ok bool) {
	for _, pr := range ws.PRs {
		switch {
		case to == ChecksView && window == diffWindow(pr):
			return checksWindow(pr), ws.Root(), checksCommand(pr), false, true
		case to == DiffView && window == checksWindow(pr):
			cwd, cmd, stale := diffCommandFor(ws, worktrees(), pr)
			return diffWindow(pr), cwd, cmd, stale, true
		}
	}
	return "", "", "", false, false
}
