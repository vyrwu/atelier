package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/tmux"
)

// openDiff shows a PR's changes in a window of the workspace session — a
// program in a window, like the agent or a shell, not a screen atelier draws.
//
// The diff comes from the worktree the PR was built in whenever there is one:
// it is instant, it works offline, and it has no size limit, where GitHub's
// diff endpoint refuses anything past 20k lines (atelier's own v1 PR is 50k).
// Only a PR with no worktree here falls back to asking GitHub.
func (m Model) openDiff(pr core.PR) (tea.Model, tea.Cmd) {
	if m.curWS == nil {
		return m, nil
	}
	cwd, cmd, stale := m.diffCommand(pr)
	if cmd == "" {
		m.err = fmt.Sprintf("no worktree for %s and no way to fetch #%d", pr.Head, pr.Number)
		return m, nil
	}
	note := ""
	if stale {
		note = fmt.Sprintf("#%d moved on GitHub — showing what this worktree has", pr.Number)
	}
	return m.openWindow(diffWindow(pr), cwd, cmd, note)
}

// openWindow runs cmd in a named window of the current space and switches the
// outer client to it, closing the overlay. A window of that name already open is
// returned to rather than started again. note, if set, is shown as a toast —
// not printed into the window, where a pager would swallow it.
func (m Model) openWindow(name, cwd, cmd, note string) (tea.Model, tea.Cmd) {
	session := m.curWS.Session
	if tmux.HasWindow(session, name) {
		_ = tmux.SelectWindow(session, name)
		_ = tmux.SwitchClient(m.outer, session)
		return m, tea.Quit
	}
	if err := tmux.NewWindowCmd(session, cwd, name, cmd, nil); err != nil {
		m.err = err.Error()
		return m, nil
	}
	if note != "" {
		_ = tmux.Notify(m.outer, note)
	}
	_ = tmux.SelectWindow(session, name)
	_ = tmux.SwitchClient(m.outer, session)
	return m, tea.Quit
}

// diffWindow is the window name for a PR's diff. Window names are atelier's
// identity for navigation, so one PR always reopens the same window.
func diffWindow(pr core.PR) string {
	// The repo is part of the name: numbers are per repo, and a space working
	// across two can hold a #12 in each.
	if _, name, ok := strings.Cut(pr.Repo, "/"); ok && name != "" {
		return sanitizeWindow(fmt.Sprintf("pr-%s-%d", name, pr.Number))
	}
	return fmt.Sprintf("pr-%d", pr.Number)
}

// diffCommand builds the diff for a PR: where to run it, what to run, and
// whether the local branch has fallen behind what GitHub has.
//
// Locally it is a three-dot diff against the PR's base, which is what GitHub
// shows — and for a stacked PR that means its own slice of the stack, not the
// whole tower, because the base is the branch below it.
func (m Model) diffCommand(pr core.PR) (cwd, cmd string, stale bool) {
	if m.curWS == nil {
		return "", "", false
	}
	return diffCommandFor(m.curWS, m.wt[m.curWS.Slug], pr)
}

// diffCommandFor is diffCommand for any space, given its worktrees — shared by
// the overlay and the M-d swap from a PR's CI window.
func diffCommandFor(ws *core.Workspace, wts []core.Worktree, pr core.PR) (cwd, cmd string, stale bool) {
	// Without a known base there is nothing correct to diff against locally
	// (a PR cached before bases were recorded, until the next refresh) — GitHub
	// knows it, so ask GitHub.
	if wt, ok := worktreeIn(wts, pr); ok && pr.Base != "" {
		// The base is fetched first: a stacked PR's parent branch may never have
		// reached this clone, and a stale one puts commits in the diff that are
		// not the PR's. Failure to fetch (offline) falls through to whatever the
		// clone has. Piped to the pager rather than left to git's own, so the
		// choice below applies here too — with --no-pager so nothing pages twice,
		// and stderr in the stream so a missing ref shows instead of a blank.
		d := pagerPrelude + fmt.Sprintf(
			"git fetch --quiet origin %s 2>/dev/null; git --no-pager diff %s...HEAD 2>&1 | $p",
			shellQuote(pr.Base), shellQuote("origin/"+pr.Base))
		return wt.Path, d, pr.HeadOID != "" && git.HeadOID(wt.Path) != pr.HeadOID
	}
	// No worktree for this branch: ask GitHub, and pipe it through a pager
	// ourselves since gh has none.
	if pr.Repo == "" || pr.Number == 0 {
		return "", "", false
	}
	// stderr joins the stream: gh refuses a diff past 20k lines, and that refusal
	// has to reach the pager or the window is just blank.
	q := fmt.Sprintf("gh pr diff %d --repo %s 2>&1", pr.Number, shellQuote(pr.Repo))
	return ws.Root(), pagerPrelude + q + " | $p", false
}

// worktreeIn finds the worktree checked out on the PR's head
// branch, in the PR's repo. The branch alone is not enough: a task spanning two
// repos often uses one branch name in both, and matching on it would diff the
// wrong repo. The repo comes from the worktree's origin, only for the worktrees
// whose branch already matched.
func worktreeIn(wts []core.Worktree, pr core.PR) (core.Worktree, bool) {
	if pr.Head == "" {
		return core.Worktree{}, false
	}
	for _, wt := range wts {
		if wt.Branch == pr.Head && strings.EqualFold(git.OriginRepo(wt.Path), pr.Repo) {
			return wt, true
		}
	}
	return core.Worktree{}, false
}

// pagerPrelude picks what the diff is piped into, best first:
//
//   - diffnav — a file tree beside the diff, the way a PR reads on GitHub. It
//     renders through delta itself, so delta config still applies.
//   - delta — the diff alone, with n/N to move between files.
//   - less — no highlighting, but always there.
//
// The choice is made by the shell that runs the command, not by atelier looking
// along its own PATH: the window runs `sh -lc`, a login shell that rebuilds PATH
// from the user's profile, so the two can disagree — and atelier naming a pager
// the window cannot find is a "command not found" where a diff should be.
//
// Nothing here is a dependency; with none installed, less shows the diff.
const pagerPrelude = `if command -v diffnav >/dev/null 2>&1; then p=diffnav; ` +
	`elif command -v delta >/dev/null 2>&1; then p='delta --paging=always'; ` +
	`else p='less -R'; fi; `

// shellQuote wraps s so it survives as one argument in the command tmux runs.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
