// Package tmux is a thin wrapper over the tmux CLI. Every call runs on
// atelier's dedicated socket (core.LoadConfig().Socket) so it never
// disturbs the user's main tmux server.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// socket returns the configured dedicated socket name, defaulting to
// "atelier".
func socket() string {
	s := core.LoadConfig().Socket
	if s == "" {
		s = "atelier"
	}
	return s
}

// Run executes `tmux -L <socket> <args...>` and returns its combined output. On
// non-zero exit the trimmed output is folded into the error. A 5s timeout treats
// a wedged server as a bug rather than hanging the UI (attach blocks legitimately
// and goes through execTmux/syscall.Exec, never here).
func Run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	full := append([]string{"-L", socket()}, args...)
	// #nosec G204 G702 -- Fixed tmux binary and argument array; shell commands are quoted by their builders.
	out, err := exec.CommandContext(ctx, "tmux", full...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("tmux %s: %w (%s)",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// EnsureServer starts the tmux server on the dedicated socket. It is
// idempotent: start-server is a no-op when the server already runs.
func EnsureServer() error {
	_, err := Run("start-server")
	return err
}

// HasSession reports whether a session with the exact given name exists.
func HasSession(name string) bool {
	_, err := Run("has-session", "-t", "="+name)
	return err == nil
}

// ListSessions returns the names of all sessions on the dedicated
// socket. Best-effort: it returns an empty slice on any error (e.g. no
// server running yet).
func ListSessions() []string {
	names, _ := ReadSessions()
	return names
}

// ReadSessions distinguishes an absent server from a failed query. Destructive
// callers must not interpret a timeout or a missing tmux binary as no sessions.
func ReadSessions() ([]string, error) {
	out, err := Run("list-sessions", "-F", "#{session_name}")
	if err != nil && !serverAbsent(string(out)) {
		return nil, err
	}
	if err != nil {
		return nil, nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

func serverAbsent(out string) bool {
	out = strings.TrimSpace(out)
	return strings.HasPrefix(out, "no server running on ") ||
		(strings.HasPrefix(out, "error connecting to ") &&
			(strings.HasSuffix(out, "(No such file or directory)") || strings.HasSuffix(out, "(Connection refused)")))
}

// StopSession succeeds only after a successful kill or a query confirming
// absence. In particular, failure to contact a running server is not success.
func StopSession(name string) error {
	if err := KillSession(name); err != nil {
		names, queryErr := ReadSessions()
		if queryErr != nil {
			return errors.Join(err, queryErr)
		}
		if slices.Contains(names, name) {
			return err
		}
	}
	return nil
}

// NewSession creates a detached session named name, with working
// directory cwd, running cmd. The command is wrapped so the given env
// vars are exported portably before it runs. If cmd is empty the user's
// $SHELL (or /bin/sh) is run instead.
func NewSession(name, cwd string, env map[string]string, cmd string) error {
	_, err := Run("new-session", "-d", "-s", name, "-c", cwd, wrap(env, cmd))
	return err
}

// wrap builds a portable shell command string that exports env before
// running cmd: `env K=V K2=V2 ... sh -lc '<cmd>'`. Env keys are sorted
// for deterministic output; values are shell-quoted. When cmd is empty
// the user's login shell is launched instead.
func wrap(env map[string]string, cmd string) string {
	var b strings.Builder
	if len(env) > 0 {
		b.WriteString("env")
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(" ")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(shquote(env[k]))
		}
		b.WriteString(" ")
	}
	if cmd == "" {
		b.WriteString(shquote(userShell()))
	} else {
		b.WriteString("sh -lc ")
		b.WriteString(shquote(cmd))
	}
	return b.String()
}

// shquote wraps s in single quotes, escaping any embedded single quotes
// so it is safe to pass through /bin/sh.
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// userShell returns the user's login shell from $SHELL, falling back to
// /bin/sh.
func userShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// ClientSession returns the session a client is currently attached to, or "".
func ClientSession(client string) string {
	if client == "" {
		return ""
	}
	out, err := Run("display-message", "-c", client, "-p", "#{client_session}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SwitchClient points the given client at session. When client is empty
// the -c flag is omitted and tmux uses the current client.
func SwitchClient(client, session string) error {
	args := []string{"switch-client"}
	if client != "" {
		args = append(args, "-c", client)
	}
	args = append(args, "-t", "="+session)
	_, err := Run(args...)
	return err
}

// NewWindow creates a window named name in session, running the login shell in
// working directory cwd.
func NewWindow(session, cwd, name string) error {
	return NewWindowCmd(session, cwd, name, "", nil)
}

// NewWindowCmd creates a window named name in session at cwd, running cmd (the
// login shell when cmd is empty) with env exported.
func NewWindowCmd(session, cwd, name, cmd string, env map[string]string) error {
	_, err := Run("new-window", "-t", "="+session, "-c", cwd, "-n", name, wrap(env, cmd))
	return err
}

// RespawnWindow runs cmd at cwd in session's window name afresh, with env
// exported, killing whatever the window was running.
func RespawnWindow(session, name, cwd, cmd string, env map[string]string) error {
	_, err := Run("respawn-window", "-k", "-c", cwd, "-t", "="+session+":"+name, wrap(env, cmd))
	return err
}

// RenameWindow renames session's active window (used right after NewSession to
// label the agent window; a manual rename disables automatic-rename for that
// window, so the label sticks even as the agent runs).
func RenameWindow(session, name string) error {
	_, err := Run("rename-window", "-t", "="+session, name)
	return err
}

// SelectWindow makes the given window (an index or name) the active window of
// session, so a following SwitchClient lands there.
func SelectWindow(session, window string) error {
	_, err := Run("select-window", "-t", "="+session+":"+window)
	return err
}

// SelectFirstWindow activates a session's lowest-indexed window — the one it was
// created with (the agent) — regardless of the server's base-index (the user's
// ~/.tmux.conf may set base-index 1). Used so a switch lands on the agent, not a
// shell opened later in the same session.
func SelectFirstWindow(session string) error {
	out, err := Run("list-windows", "-t", "="+session, "-F", "#{window_index}")
	if err != nil {
		return err
	}
	idx := string(out)
	if i := strings.IndexByte(idx, '\n'); i >= 0 {
		idx = idx[:i]
	}
	if idx = strings.TrimSpace(idx); idx == "" {
		return nil
	}
	return SelectWindow(session, idx)
}

// DetachClient detaches the attached client from the server, leaving all
// sessions running. Used by the home splash to leave atelier gracefully.
func DetachClient() error {
	_, err := Run("detach-client")
	return err
}

// EscapeText doubles '#' so user text embedded in a status-line format renders
// literally, instead of a '#[' being read as a style directive at draw time.
func EscapeText(s string) string {
	return strings.ReplaceAll(s, "#", "##")
}

// SetTitle stores a workspace's display title as a session option the status
// line renders (#{@atelier_title}), so it updates instantly with no polling.
func SetTitle(session, title string) error {
	_, err := Run("set-option", "-t", session, "@atelier_title", EscapeText(title))
	return err
}

// SetAgentName stores the name of the agent a session runs as a session option
// the status line renders (#{@atelier_agent}) when the agent's window is in view.
func SetAgentName(session, name string) error {
	_, err := Run("set-option", "-t", session, "@atelier_agent", EscapeText(name))
	return err
}

// RefreshStatus forces attached clients to redraw their status line now (so a
// title change shows immediately, not on the next status-interval).
func RefreshStatus() error {
	_, err := Run("refresh-client", "-S")
	return err
}

// SetStatusMarker stores a pre-rendered agent-status marker (with tmux #[...]
// colour codes) as a session option the status line renders (#{@atelier_status}),
// so a working/blocked/idle change shows instantly with no polling. The caller
// renders the marker so this package stays free of status semantics.
func SetStatusMarker(session, marker string) error {
	_, err := Run("set-option", "-t", session, "@atelier_status", marker)
	return err
}

// SetGlobal sets a server-global option (set-option -g), e.g. the activity
// spinner the status line reads (#{@atelier_spin}).
func SetGlobal(name, val string) error {
	_, err := Run("set-option", "-g", name, val)
	return err
}

// Flash shows a transient message in the status line's top-right slot
// (#{@atelier_spin}, shared with the activity spinner) and schedules its own
// clear `seconds` later — server-side via run-shell, so it survives the popup
// that triggered it closing. msg may carry tmux #[...] colour codes.
func Flash(msg string, seconds int) error {
	if err := SetGlobal("@atelier_spin", msg); err != nil {
		return err
	}
	_ = RefreshStatus()
	clear := fmt.Sprintf("tmux -L %s set -g @atelier_spin ''; tmux -L %s refresh-client -S", socket(), socket())
	_, err := Run("run-shell", "-b", "-d", fmt.Sprintf("%d", seconds), clear)
	return err
}

// SetSessionEnv sets an environment variable on a session so its future windows
// and panes inherit it (set-environment). Used to pin GIT_CONFIG_GLOBAL so agent
// commits carry the operator's identity even under a redirected dev XDG.
func SetSessionEnv(session, key, val string) error {
	_, err := Run("set-environment", "-t", session, key, val)
	return err
}

// Notify shows a toast on client's status line for 5s (the default ~750ms is too
// quick to read); its look comes from message-style (set in the bindings). An
// empty client shows it on the current client.
func Notify(client, msg string) error {
	args := []string{"display-message", "-d", "5000"}
	if client != "" {
		args = append(args, "-c", client)
	}
	args = append(args, msg)
	_, err := Run(args...)
	return err
}

// HasWindow reports whether session has a window named name.
func HasWindow(session, name string) bool {
	out, err := Run("list-windows", "-t", "="+session, "-F", "#{window_name}")
	if err != nil {
		return false
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(ln) == name {
			return true
		}
	}
	return false
}

// EnsureShell selects the session's "shell" window, creating one at cwd if there
// isn't one yet — so M-c always lands on a shell in the workspace dir.
func EnsureShell(session, cwd string) error {
	if HasWindow(session, "shell") {
		return SelectWindow(session, "shell")
	}
	return NewWindow(session, cwd, "shell")
}

// MoveClients points every client attached to session at to, so killing
// session doesn't detach them from atelier.
func MoveClients(session, to string) {
	out, err := Run("list-clients", "-t", "="+session, "-F", "#{client_name}")
	if err != nil {
		return
	}
	for _, c := range strings.Fields(string(out)) {
		_ = SwitchClient(c, to)
	}
}

// KillSession destroys the named session.
func KillSession(name string) error {
	_, err := Run("kill-session", "-t", "="+name)
	return err
}

// GlobalOption reads a global user option (e.g. @atelier_outer), or "".
func GlobalOption(name string) string {
	out, err := Run("show-options", "-gqv", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// HomeSession is the landing splash's session name — the one session atelier
// always keeps, so there is somewhere to return to.
const HomeSession = "home"

// EnsureHome creates the splash session running cmd if it is missing, and
// (re)applies its status line: the bar stays, minus the parts that describe a
// space, since there is no space here. Idempotent — both the M-h binding and the
// overlay go through it, so the splash is always there to land on.
func EnsureHome(cwd, cmd string) error {
	if !HasSession(HomeSession) {
		if err := NewSession(HomeSession, cwd, nil, cmd); err != nil {
			return err
		}
	}
	// An older build hid the bar outright; unset that before styling it.
	_, _ = Run("set-option", "-t", HomeSession, "-u", "status")
	_, err := Run("set-option", "-t", HomeSession, "status-left", " #[fg=colour110,bold]atelier#[nobold] ")
	return err
}

// RestartHome restarts the splash's program in place. A running process keeps
// the code it started with, so after an upgrade the splash would otherwise go on
// showing the old binary's screen for as long as the server lives.
func RestartHome(cwd, cmd string) error {
	// "=home:" — the session's current window and pane; a bare "=home" does not
	// resolve as a pane target.
	_, err := Run("respawn-pane", "-k", "-c", cwd, "-t", "="+HomeSession+":", wrap(nil, cmd))
	return err
}

// CapturePane joins wrapped lines and includes the requested scrollback.
func CapturePane(target string, scrollback int) (string, error) {
	out, err := Run("capture-pane", "-p", "-J", "-t", target, "-S", fmt.Sprintf("-%d", scrollback))
	return string(out), err
}

func sendLiteralArgs(target, text string) []string {
	return []string{"send-keys", "-t", target, "-l", "--", text}
}

func SendLiteral(target, text string) error {
	_, err := Run(sendLiteralArgs(target, text)...)
	return err
}

func SendKeys(target string, keys ...string) error {
	_, err := Run(append([]string{"send-keys", "-t", target, "--"}, keys...)...)
	return err
}

func DetachSession(session string) error {
	_, err := Run("detach-client", "-s", "="+session)
	return err
}

func SetSessionOption(session, name, value string) error {
	_, err := Run("set-option", "-t", session, name, value)
	return err
}
