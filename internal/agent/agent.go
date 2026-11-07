// Package agent launches a space's coding agent — Claude Code or Codex — hands
// a space from one to the other, and tracks each session's runtime status.
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

// Window is the tmux window name of a space's agent, so it can always be found
// and respawned regardless of window index. The name predates Codex; it runs
// whichever agent the space has.
const Window = "claude"

// EnsureAgent makes sure the space's agent window is running and ready to
// select: it revives the whole session if it's gone, or respawns just the agent
// window if you exited the agent but left other windows (shells) open — so M-a
// always brings it back. It resumes the space's conversation with its agent.
func EnsureAgent(ws core.Workspace) error {
	if tmux.HasSession(ws.Session) && tmux.HasWindow(ws.Session, Window) {
		return nil
	}
	k := Of(ws.Agent)
	return RunAgent(ws, k, k.ResumeCmd(LatestSession(k, ws.Root(), ws.Created), ""))
}

// RunAgent runs cmd as the space's agent k: in a new session if the space has
// none, in a new agent window if that was closed, or in place of the agent that
// is running — which is how a swap replaces one agent with the other without
// killing the session (and detaching whoever is in it with its last window).
func RunAgent(ws core.Workspace, k Kind, cmd string) error {
	var launchErr error
	err := core.Update(func(s *core.State) {
		current := s.Find(ws.Slug)
		if current == nil || current.Retired {
			launchErr = fmt.Errorf("space %q is no longer active", ws.Slug)
			return
		}
		launchErr = runAgent(*current, k, cmd)
	})
	if err != nil {
		return err
	}
	return launchErr
}

// Launch while holding the state lock shared with purge and restoration.
func runAgent(ws core.Workspace, k Kind, cmd string) error {
	env := Env(ws.Slug, k)
	_ = InstallClaudeUsage(ws.Root())
	var err error
	switch {
	case !tmux.HasSession(ws.Session):
		if err = tmux.NewSession(ws.Session, ws.Root(), env, cmd); err == nil {
			err = tmux.RenameWindow(ws.Session, Window)
		}
	case !tmux.HasWindow(ws.Session, Window):
		err = tmux.NewWindowCmd(ws.Session, ws.Root(), Window, cmd, env)
	default:
		err = tmux.RespawnWindow(ws.Session, Window, ws.Root(), cmd, env)
	}
	if err != nil {
		return err
	}
	return tmux.SetAgentName(ws.Session, k.Label())
}

// Env is what agent k's process needs from atelier: the space identity its hooks
// and MCP server key on, which agent it is (see hookCommand), and the operator's
// git identity so it commits as them.
func Env(slug string, k Kind) map[string]string {
	env := map[string]string{"ATELIER_SESSION": slug, "ATELIER_SLUG": slug, "ATELIER_AGENT": string(k)}
	if g := GitConfigGlobal(); g != "" {
		env["GIT_CONFIG_GLOBAL"] = g
	}
	return env
}

// GenerateTitle asks agent k for a short human title for intent (used to name
// the workspace and its directory), with an optional model override. It returns
// "" on any failure or an implausible answer, so the caller can fall back to a
// deterministic title. Blocking: the caller runs it off the UI's update loop.
func GenerateTitle(k Kind, intent, model string) string {
	t, err := k.Ask("Reply with ONLY a 3-6 word title for this task, no trailing punctuation:\n\n"+intent, "", model)
	if err != nil {
		return ""
	}
	// Be forgiving of minor chattiness: take the first non-empty line and drop
	// any wrapping quotes, rather than rejecting the whole answer.
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	t = strings.Trim(t, `"'`)
	if t == "" || len(t) > 80 {
		return ""
	}
	return t
}

// GitConfigGlobal locates the operator's real global git config so workspace
// agents commit as them, not the OS default. Under `make dev` the XDG redirect
// hides ~/.config/git/config and a long-lived tmux server can lack
// GIT_CONFIG_GLOBAL, so resolving it by explicit path (rather than trusting env
// inheritance) is what makes agent attribution reliable. Prefers an existing
// GIT_CONFIG_GLOBAL (Makefile/prod), then the standard locations, picking the
// first that actually configures an identity. "" if none is found.
func GitConfigGlobal() string {
	var cands []string
	if g := os.Getenv("GIT_CONFIG_GLOBAL"); g != "" {
		cands = append(cands, g)
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".gitconfig"))
	}
	for _, p := range cands {
		if hasGitEmail(p) {
			return p
		}
	}
	return ""
}

// hasGitEmail reports whether the git config file at path actually configures a
// user.email — parsed with git, not a substring match: a "[sendemail]" header or
// a URL containing "email" would false-match, and since GIT_CONFIG_GLOBAL
// *replaces* the global config chain (not augments it), a wrong pick would strand
// the agent with no identity.
func hasGitEmail(path string) bool {
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return false
	}
	out, err := exec.Command("git", "config", "--file", path, "--get", "user.email").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// statusFile is the path of the status file for session.
func statusFile(session string) string {
	return filepath.Join(core.CacheDir(), "agents", session)
}

// SetStatus records st as session's current runtime status, creating the
// agents directory if needed. The file body is "<status>\n<unix-ts>".
func SetStatus(session string, st core.AgentStatus) error {
	if !core.ValidSpaceName(session) {
		return fmt.Errorf("invalid session name %q", session)
	}
	path := statusFile(session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("%s\n%d\n", st, time.Now().Unix())
	return os.WriteFile(path, []byte(body), 0o644)
}

// LastEvent is when session's agent last reported anything — the status file's
// mtime, which every hook rewrites — or zero if it never has.
func LastEvent(session string) time.Time {
	fi, err := os.Stat(statusFile(session))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// ClearStatus drops session's status file, for a space that no longer exists.
func ClearStatus(session string) error {
	if !core.ValidSpaceName(session) {
		return fmt.Errorf("invalid session name %q", session)
	}
	err := os.Remove(statusFile(session))
	if os.IsNotExist(err) {
		return nil
	}
	return err
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
