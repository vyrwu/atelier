// Package agent launches Claude Code in a workspace and tracks each session's
// runtime status. Claude is the one agent atelier drives, so there is no
// adapter abstraction here.
//
// Status is per-session runtime state — "is this agent working, blocked on the
// user, idle, or gone?" — stored as a tiny file under CacheDir()/agents/<session>
// whose body is "<status>\n<unix-ts>". It is deliberately NOT in the durable
// state file and NOT in tmux: it is ephemeral and rebuilt from the live agent's
// lifecycle hooks (see hooks.go).
package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

// ClaudeWindow is the tmux window name of the agent (Claude) within a workspace
// session, so it can always be found and respawned regardless of window index.
const ClaudeWindow = "claude"

// EnsureClaude makes sure the workspace's Claude window is running and ready to
// select: it revives the whole session if it's gone, or respawns just the
// "claude" window if you exited Claude but left other windows (shells) open —
// so M-c always brings Claude back. Resumes the prior conversation.
func EnsureClaude(session, root, slug string) error {
	env := map[string]string{"ATELIER_SESSION": slug, "ATELIER_SLUG": slug}
	if !tmux.HasSession(session) {
		if err := tmux.NewSession(session, root, env, ResumeCmd()); err != nil {
			return err
		}
		return tmux.RenameWindow(session, ClaudeWindow)
	}
	if !tmux.HasWindow(session, ClaudeWindow) {
		return tmux.NewWindowCmd(session, root, ClaudeWindow, ResumeCmd(), env)
	}
	return nil
}

// LaunchCmd returns the shell command that starts Claude Code in the workspace
// with intent as its opening prompt. Claude Code accepts an initial prompt as a
// positional argument, so the command is `claude <shell-quoted-intent>`. The
// tmux layer runs the returned string.
func LaunchCmd(intent string) string {
	return "claude " + shellQuote(intent)
}

// ResumeCmd starts Claude resuming the workspace's most recent conversation,
// falling back to a fresh session if there is none. Used to revive a workspace
// whose agent session died (e.g. the tmux server was restarted) so switching to
// it picks up where the agent left off instead of re-running the intent.
func ResumeCmd() string {
	return "claude --continue || claude"
}

// GenerateTitle asks Claude for a short human title for intent (used to name the
// workspace and its directory). It shells out to `claude -p` with an optional
// model override and returns "" on any failure or an implausible answer, so the
// caller can fall back to a deterministic title. Blocking: the caller runs it
// off the UI's update loop (as a tea.Cmd).
func GenerateTitle(intent, model string) string {
	// --no-session-persistence: a print-mode session is otherwise saved under the
	// directory it ran in — here, whatever space the overlay was opened from — and
	// would become the conversation `claude --continue` resumes there.
	args := []string{"-p", "--no-session-persistence",
		"Reply with ONLY a 3-6 word title for this task, no trailing punctuation:\n\n" + intent}
	if model != "" {
		args = append([]string{"--model", model}, args...)
	}
	out, err := exec.Command("claude", args...).Output()
	if err != nil {
		return ""
	}
	// Be forgiving of minor chattiness: take the first non-empty line and drop
	// any wrapping quotes, rather than rejecting the whole answer.
	t := strings.TrimSpace(string(out))
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	t = strings.Trim(t, `"'`)
	if t == "" || len(t) > 80 {
		return ""
	}
	return t
}

// statusFile is the path of the status file for session.
func statusFile(session string) string {
	return filepath.Join(core.CacheDir(), "agents", session)
}

// SetStatus records st as session's current runtime status, creating the
// agents directory if needed. The file body is "<status>\n<unix-ts>".
func SetStatus(session string, st core.AgentStatus) error {
	path := statusFile(session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("%s\n%d\n", st, time.Now().Unix())
	return os.WriteFile(path, []byte(body), 0o644)
}

// workingStaleAfter bounds how long a "working" status survives without a fresh
// signal. Real turns keep it fresh via PreToolUse/PostToolUse; a status still
// "working" past this had its turn die without a Stop hook (crash, kill, hang),
// so it settles to idle rather than claiming to run forever. Generous on purpose
// — a single long tool must not trip it — and self-correcting, since the next
// tool event restores "working" at once.
const workingStaleAfter = 20 * time.Minute

// Status reports session's runtime status. A session whose tmux window is gone
// (live == false) is always StatusGone. For a live session it returns the
// stored status, defaulting to StatusIdle when no status file exists yet, and
// decaying a stale "working" (see workingStaleAfter) to idle.
func Status(session string, live bool) core.AgentStatus {
	if !live {
		return core.StatusGone
	}
	st, ts := readStatus(session)
	if st == "" {
		return core.StatusIdle
	}
	if st == core.StatusWorking && !ts.IsZero() && time.Since(ts) > workingStaleAfter {
		return core.StatusIdle
	}
	return st
}

// readStatus parses a session's status file ("<status>\n<unix-ts>"). It returns
// an empty status when the file is missing or blank, and a zero time when no
// parsable timestamp is present.
func readStatus(session string) (core.AgentStatus, time.Time) {
	data, err := os.ReadFile(statusFile(session))
	if err != nil {
		return "", time.Time{}
	}
	lines := strings.SplitN(string(data), "\n", 3)
	st := core.AgentStatus(strings.TrimSpace(lines[0]))
	var ts time.Time
	if len(lines) > 1 {
		if sec, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
			ts = time.Unix(sec, 0)
		}
	}
	return st, ts
}

// StatusMarker renders st as a pre-coloured tmux marker for the status-left
// (#{@atelier_status}, pushed by SetStatusMarker). Colours mirror the overlay
// glyphs: teal ◐ working, terracotta ● blocked (the one attention colour),
// grey ○ idle. It resets fg to the bar default (colour252) so the rest of the
// line keeps its colour. Gone has no marker (a dead session shows no bar).
func StatusMarker(st core.AgentStatus) string {
	switch st {
	case core.StatusBlocked:
		return "#[fg=colour173,bold]●#[nobold,fg=colour252] "
	case core.StatusWorking:
		return "#[fg=colour73]◐#[fg=colour252] "
	case core.StatusGone:
		return ""
	default: // idle
		return "#[fg=colour245]○#[fg=colour252] "
	}
}

// BlockedCount returns how many of liveSessions currently have status
// StatusBlocked — i.e. how many live agents are waiting on the user.
func BlockedCount(liveSessions []string) int {
	n := 0
	for _, s := range liveSessions {
		if Status(s, true) == core.StatusBlocked {
			n++
		}
	}
	return n
}

// RefreshAttention recomputes the blocked-workspace count and pushes it into the
// status line's @atelier_attention option, then forces a redraw — so the badge
// is event-driven (no polling) and updates the instant a workspace blocks or
// clears. Called from every status change and session mutation.
func RefreshAttention() {
	marker := ""
	if n := BlockedCount(tmux.ListSessions()); n > 0 {
		marker = fmt.Sprintf("#[fg=colour173,bold]● %d#[nobold,fg=colour252]", n)
	}
	_ = tmux.SetGlobal("@atelier_attention", marker)
	_ = tmux.RefreshStatus()
}

// shellQuote wraps s in single quotes, escaping embedded single quotes, so it
// is safe as a single argument in a /bin/sh command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
