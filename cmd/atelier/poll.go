package main

import (
	"time"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/forge"
	"github.com/vyrwu/atelier/internal/git"
)

// Poll intervals: the space a client is looking at, and every other one. The
// focused rate is bounded below by status-interval, which drives the job.
const (
	focusedEvery = 5 * time.Second
	otherEvery   = time.Minute
	// pollSlack absorbs tmux scheduling: it re-runs a job on redraw, to the
	// second, so a sweep can start a beat before its interval is up.
	pollSlack = time.Second
)

// poll is tmux's status job for PR state (`atelier poll <session>`): it sweeps
// GitHub for every space whose cache is due, in one query, and prints nothing.
// tmux runs it per attached client on every status redraw, so sweeps take turns
// under core.LockPRs, and the state is read under it: a client waiting on
// another's sweep sees what it wrote and sweeps only its own focused space.
// Detached, nothing runs.
func poll(args []string) error {
	unlock, err := core.LockPRs()
	if err != nil {
		return err
	}
	defer unlock()

	due := dueSpaces(core.Load().Workspaces, arg(args, 0), time.Now())
	if len(due) == 0 {
		return nil
	}
	in := make([]forge.Space, len(due))
	for i, w := range due {
		in[i] = forge.Space{PRs: w.PRs, Worktrees: git.Worktrees(w.Root())}
	}
	// Stamped with when the sweep began, so its duration doesn't stretch the
	// interval. A failed sweep counts too: retrying sooner wouldn't help.
	began := time.Now()
	out := forge.RefreshAll(in)
	return core.Update(func(s *core.State) {
		for i, w := range due {
			if ws := s.Find(w.Slug); ws != nil {
				ws.PRs = forge.KeepRegistered(out[i], ws.PRs)
				ws.PRsRefreshed = began
			}
		}
	})
}

// dueSpaces is the active spaces whose PR cache is older than their interval:
// focusedEvery for the one in session focused, otherEvery for the rest.
func dueSpaces(ws []core.Workspace, focused string, now time.Time) []core.Workspace {
	var out []core.Workspace
	for _, w := range ws {
		if w.Retired {
			continue
		}
		every := otherEvery
		if w.Session == focused {
			every = focusedEvery
		}
		if now.Sub(w.PRsRefreshed) >= every-pollSlack {
			out = append(out, w)
		}
	}
	return out
}
