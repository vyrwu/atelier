// Package tmux is a thin wrapper over the tmux CLI. Every call runs on
// atelier's dedicated socket (core.LoadConfig().Socket) so it never
// disturbs the user's main tmux server.
package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	out, err := Run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return []string{}
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
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

// RenameWindow renames session's active window (used right after NewSession to
// label the agent window "claude"; a manual rename disables automatic-rename for
// that window, so the label sticks even as Claude runs).
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
// ~/.tmux.conf may set base-index 1). Used so a switch lands on Claude, not a
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

// Notify flashes a one-line message on client's status line (a non-modal toast).
// An empty client shows it on the current client.
func Notify(client, msg string) error {
	args := []string{"display-message"}
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
// isn't one yet — so M-s always lands on a shell in the workspace dir.
func EnsureShell(session, cwd string) error {
	if HasWindow(session, "shell") {
		return SelectWindow(session, "shell")
	}
	return NewWindow(session, cwd, "shell")
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
