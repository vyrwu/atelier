package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

// Status-line marks a background action resolves into.
const (
	okMark   = "#[fg=colour71]✓#[fg=colour252] "
	failMark = "#[fg=colour167]✗#[fg=colour252] "
)

// swapAgent hands a space to its other agent, or to a new conversation with the
// same one (M-x, or `atelier swap <slug> [--to claude|codex]`; the other agent
// by default). It checks the swap can happen, then puts the handoff in
// the space's agent window in place of the agent: stopping the outgoing agent
// first is what makes its transcript final before anything reads it. It returns
// at once; the window shows the handoff as it runs.
func swapAgent(args []string) error {
	slug := arg(args, 0)
	ws := core.Load().Find(slug)
	if ws == nil {
		return fmt.Errorf("no space %q", slug)
	}
	if ws.Retired {
		return fmt.Errorf("%s is in the trash; restore it first", slug)
	}
	from := agent.Of(ws.Agent)
	to := from.Other()
	if t := flagVal(args, "--to"); t != "" {
		if t != string(agent.Claude) && t != string(agent.Codex) {
			return fmt.Errorf("--to %q: want claude or codex", t)
		}
		to = agent.Of(t)
	}
	if _, err := exec.LookPath(to.Bin()); err != nil {
		_ = tmux.Flash(failMark+to.Bin()+" is not installed", 5)
		return fmt.Errorf("%s is not installed", to.Bin())
	}
	// The handoff holds this lock while it runs. Checking it here keeps a second
	// M-x from killing a handoff half-way through.
	unlock, ok, err := core.TryLockFile(swapLock(slug))
	if err != nil {
		return err
	}
	if !ok {
		_ = tmux.Flash(failMark+tmux.EscapeText(spaceName(*ws))+" is already swapping", 4)
		return nil
	}
	unlock()
	return agent.RunAgent(*ws, to, agent.HandoffCmd(slug, to))
}

// swapLock is the lock a space's handoff holds.
func swapLock(slug string) string {
	return filepath.Join(core.CacheDir(), "locks", "swap-"+slug+".lock")
}

// handoff is the swap itself, run in the space's agent window in place of the
// outgoing agent (see swapAgent). It writes out what the outgoing agent did since
// the last swap, records the swap, then becomes the incoming agent at once. That
// agent resumes its own earlier conversation in the space, so swapping back and
// forth never piles up new ones, or starts one, with the handoff as its next
// message.
func handoff(slug, toName string) error {
	err := runHandoff(slug, agent.Of(toName))
	// Reached only on failure: success replaces this process with the agent.
	// Hold the error on screen, then go back to the space's agent rather than
	// exit — this is often the session's last window, and closing it would end
	// the session and detach whoever is in it.
	fmt.Printf("\natelier: the handoff failed: %v\nPress Enter to go back to the space's agent.\n", err)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	ws := core.Load().Find(slug)
	if ws == nil {
		return err
	}
	k := agent.Of(ws.Agent)
	_ = tmux.SetAgentName(ws.Session, k.Label())
	return become(k, k.ResumeCmd(agent.LatestSession(k, ws.Root(), ws.Created), ""))
}

// become replaces the handoff with agent k running cmd, in the same window and
// environment — except that the environment names k, which the window was
// started for the handoff's target (see agent.Env), and raises the age past
// which Claude asks whether to resume a long conversation at all. Swapping back
// after a usage limit resets nearly always passes the default 70 minutes, and
// the question would hold up the handoff, often in a window nobody is looking
// at; resuming is what the swap is for.
func become(k agent.Kind, cmd string) error {
	env := append(envWithout("ATELIER_AGENT", "CLAUDE_CODE_RESUME_THRESHOLD_MINUTES"),
		"ATELIER_AGENT="+string(k), "CLAUDE_CODE_RESUME_THRESHOLD_MINUTES=1000000")
	return syscall.Exec("/bin/sh", []string{"sh", "-lc", cmd}, env)
}

func runHandoff(slug string, to agent.Kind) error {
	ws := core.Load().Find(slug)
	if ws == nil {
		return fmt.Errorf("no space %q", slug)
	}
	// The descriptor is close-on-exec: the lock is released when this process
	// becomes the agent.
	unlock, ok, err := core.TryLockFile(swapLock(slug))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s is already swapping", slug)
	}
	defer unlock()
	from := agent.Of(ws.Agent)
	start := time.Now()
	fmt.Printf("atelier: handing %s from %s to %s\n\n", spaceName(*ws), from.Label(), to.Label())
	finish := startSpinner("swapping " + slug + " → " + to.Label())
	defer finish("")

	note, err := handoffNote(*ws, from, to, start)
	if err != nil {
		return err
	}
	_ = to.Prepare(ws.Root(), slug)
	agent.LinkGuide(ws.Root())
	if err := core.Update(func(s *core.State) {
		if w := s.Find(slug); w != nil {
			w.Agent = string(to)
			w.SwappedAt = start
		}
	}); err != nil {
		return err
	}
	_ = agent.SetStatus(ws.Session, core.StatusWorking)
	_ = tmux.SetStatusMarker(ws.Session, agent.StatusMarker(core.StatusWorking))
	agent.RefreshAttention()

	// The incoming agent resumes its own earlier conversation here; handed to
	// itself, the point is deliberately a new one.
	id := ""
	if to != from {
		id = agent.LatestSession(to, ws.Root(), ws.Created)
	}
	cmd := handoffAgentCmd(from, to, id, note)
	finish("")
	_ = tmux.Flash(okMark+tmux.EscapeText(spaceName(*ws))+" → "+to.Label(), 4)
	return become(to, cmd)
}

// handoffAgentCmd makes the session rule explicit and easy to test: crossing
// agents resumes the target's existing conversation, while handing an agent to
// itself always starts a fresh conversation seeded by the export.
func handoffAgentCmd(from, to agent.Kind, targetSession, note string) string {
	if from == to {
		return to.LaunchCmd(note)
	}
	return to.ResumeCmd(targetSession, note)
}

// handoffNote writes the outgoing agent's conversation since the last swap
// under the space's .atelier/handoff, and returns the message the incoming
// agent opens with: where that is, or, with no conversation to hand over, the
// task. Nothing here waits on a model — the incoming agent starts at once and
// reads what it needs.
func handoffNote(ws core.Workspace, from, to agent.Kind, at time.Time) (string, error) {
	head := fmt.Sprintf("%s %s worked on this space and has handed it to you.", agent.HandoffMark, from.Label())
	if from == to {
		head = fmt.Sprintf("%s A previous %s conversation worked on this space and has handed it to you.", agent.HandoffMark, from.Label())
	}
	text := ""
	if path := agent.Transcript(from, ws.Root(), ws.Created); path != "" {
		fmt.Printf("Exporting %s's conversation…\n", from.Label())
		t, err := agent.Export(from, path, ws.SwappedAt)
		if err != nil {
			return "", fmt.Errorf("export %s's conversation: %w", from.Label(), err)
		}
		text = strings.TrimSpace(t)
	}
	if text == "" {
		fmt.Printf("  %s left no conversation to hand over.\n", from.Label())
		return head + " It left no conversation to hand over. The task: " + ws.Intent, nil
	}
	dir := filepath.Join(ws.Root(), ".atelier", "handoff")
	export := filepath.Join(dir, at.Format("2006-01-02T15-04-05.000000000")+"-"+string(from)+".md")
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		err = os.WriteFile(export, []byte(text+"\n"), 0o644)
	}
	if err != nil {
		return "", fmt.Errorf("save handoff: %w", err)
	}
	return head + fmt.Sprintf(" Its conversation since it took over — the operator's messages, its replies, and the tools it ran, without their output — is in %s; earlier handoffs are beside it.", export) +
		" Read it, check the worktrees' state, then pick up where it left off.", nil
}

// spaceName is how a space is named to the operator: its title, or its slug.
func spaceName(w core.Workspace) string {
	if w.Title != "" {
		return w.Title
	}
	return w.Slug
}
