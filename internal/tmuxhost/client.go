// Package tmuxhost is the only place in atelier that shells out to tmux.
// Keeping every `exec.Command("tmux", ...)` here gives us one testable seam
// and one place to handle tmux version differences.
package tmuxhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vyrwu/atelier/internal/debuglog"
)

// DefaultTimeout caps how long any single tmux command may take. A
// healthy tmux server responds to any well-formed command in
// milliseconds; anything past a few seconds means the server is
// wedged (popup eating input, pty issue, crashed worker, etc.).
//
// Without a deadline atelier would hang forever on a wedged server —
// every status-bar refresh, every workspace listing, every popup
// dispatch. We treat that as a bug and impose an upper bound on
// every exec instead.
//
// Tuned to be generous (slow CI / cold cache / heavy machine) without
// being so long that a user notices when it triggers.
const DefaultTimeout = 5 * time.Second

type Client struct {
	socket     string
	timeout    time.Duration
	configFile string // when set, prepends `-f <file>` to every tmux call
}

// SetConfigFile makes every subsequent tmux invocation include
// `-f <path>` (before the -L flag). Intended for tests: passing
// `-f /dev/null` prevents tmux from sourcing the developer's real
// ~/.config/tmux/tmux.conf, which — via atelier's own init emitted
// there — would trigger `atelier state restore` on the fresh test
// socket and populate it with production workspaces from
// ~/.cache/atelier/state-<host>.json.
//
// Production code never needs this; a stray `-f /dev/null` in a
// production tmux invocation would drop the user's own tmux config.
func (c *Client) SetConfigFile(path string) { c.configFile = path }

// New returns a Client that targets the named tmux socket (-L).
// Pass an empty string to target the user's default tmux server — or,
// for tests, the ATELIER_TMUX_SOCKET env var if set. The env-var path
// lets sub-commands invoked via `atelier tools ...` from inside e2e
// tests route to the isolated test server without each command needing
// its own --socket flag.
//
// Every tmux invocation through this client has a DefaultTimeout
// deadline; callers that need a different deadline can use
// NewWithTimeout.
func New(socket string) *Client {
	return NewWithTimeout(socket, DefaultTimeout)
}

// NewWithTimeout returns a Client with a custom per-command deadline.
// Use only when DefaultTimeout is genuinely wrong (e.g. for `attach`
// flows that legitimately block on user input — which is why we use
// syscall.Exec there, not exec.Command).
func NewWithTimeout(socket string, timeout time.Duration) *Client {
	if socket == "" {
		socket = os.Getenv("ATELIER_TMUX_SOCKET")
	}
	c := &Client{socket: socket, timeout: timeout}
	// ATELIER_TMUX_CONFIG lets test harnesses (or advanced users)
	// override the tmux config file for every tmux call made through
	// this client — e2e tests set it to /dev/null so tmux doesn't
	// source the developer's ~/.config/tmux/tmux.conf into the
	// isolated test server.
	if v := os.Getenv("ATELIER_TMUX_CONFIG"); v != "" {
		c.configFile = v
	}
	return c
}

func (c *Client) Socket() string     { return c.socket }
func (c *Client) ConfigFile() string { return c.configFile }

func (c *Client) args(args []string) []string {
	out := make([]string, 0, len(args)+4)
	if c.configFile != "" {
		out = append(out, "-f", c.configFile)
	}
	if c.socket != "" {
		out = append(out, "-L", c.socket)
	}
	out = append(out, args...)
	return out
}

// exec runs `tmux <fullArgs>` under the client's timeout and returns
// CombinedOutput + err. Every other Client method funnels through here
// so the timeout, env, and debug logging are uniform.
//
// On timeout, the underlying process is SIGKILLed and a clearly-
// labelled error returned so callers can tell "tmux is wedged" apart
// from "tmux returned non-zero".
func (c *Client) exec(fullArgs []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", fullArgs...)
	out, err := cmd.CombinedOutput()
	debuglog.LogCmd(fullArgs, out, err)
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("tmux %s: timed out after %s (server wedged?)",
			strings.Join(fullArgs, " "), c.timeout)
	}
	return out, err
}

// Run invokes `tmux <args>` and returns its combined output. Every
// call is recorded to the debug log so silent failures (esp. tmux's
// "no such window" / "no current client") leave a trace.
func (c *Client) Run(args ...string) ([]byte, error) {
	fullArgs := c.args(args)
	out, err := c.exec(fullArgs)
	if err != nil {
		return out, fmt.Errorf("tmux %s: %w (%s)",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Version returns `tmux -V` output (e.g. "tmux 3.4").
func (c *Client) Version() (string, error) {
	out, err := c.Run("-V")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// HasSession returns true if a session with the given name exists.
func (c *Client) HasSession(name string) (bool, error) {
	fullArgs := c.args([]string{"has-session", "-t", "=" + name})
	_, err := c.exec(fullArgs)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// NewSession creates a session with the given name.
func (c *Client) NewSession(name string, detached bool) error {
	args := []string{"new-session", "-s", name}
	if detached {
		args = append(args, "-d")
	}
	_, err := c.Run(args...)
	return err
}

// NewSessionWithCommand creates a detached session that runs the given
// shell command. Sizes the new session to the outer client's terminal
// when @atelier_outer_client is set — without -x/-y, tmux defaults
// detached sessions to 80x24 and TUI tools (gh-dash, lazygit, k9s)
// measure that initial size and lay themselves out for an 80-col
// screen, leaving the rest of the popup empty even after the popup
// attaches at full geometry and aggressive-resize fires.
//
// Best-effort: when the outer client can't be resolved, falls back to
// the original (no-size-hint) form so legacy callers / tests still
// work.
func (c *Client) NewSessionWithCommand(name, shellCmd string) error {
	args := []string{"new-session", "-d", "-s", name}
	if w, h := c.outerClientSize(); w > 0 && h > 0 {
		args = append(args, "-x", fmt.Sprintf("%d", w), "-y", fmt.Sprintf("%d", h))
	}
	args = append(args, shellCmd)
	_, err := c.Run(args...)
	return err
}

// outerClientSize returns the dimensions of the client recorded in
// @atelier_outer_client (set by the M-; / M-n / M-s root binding).
// Returns (0, 0) on any lookup failure so the caller can fall back to
// tmux's default.
func (c *Client) outerClientSize() (int, int) {
	outerBytes, err := c.Run("show-options", "-gv", "@atelier_outer_client")
	if err != nil {
		return 0, 0
	}
	outer := strings.TrimSpace(string(outerBytes))
	if outer == "" {
		return 0, 0
	}
	wh, err := c.Run("display-message", "-p", "-t", outer, "#{client_width}x#{client_height}")
	if err != nil {
		return 0, 0
	}
	parts := strings.SplitN(strings.TrimSpace(string(wh)), "x", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	w, errW := strconv.Atoi(parts[0])
	h, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil {
		return 0, 0
	}
	return w, h
}

// KillSession terminates the named session.
func (c *Client) KillSession(name string) error {
	_, err := c.Run("kill-session", "-t", "="+name)
	return err
}

// ListSessions returns all session names on the target server.
func (c *Client) ListSessions() ([]string, error) {
	fullArgs := c.args([]string{"list-sessions", "-F", "#{session_name}"})
	out, err := c.exec(fullArgs)
	if err != nil {
		if strings.Contains(string(out), "no server running") {
			return nil, nil
		}
		return nil, fmt.Errorf("list-sessions: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// ListWindows returns lines of "<session_id> <window_id>" pairs across all sessions.
func (c *Client) ListWindows() ([]string, error) {
	fullArgs := c.args([]string{"list-windows", "-a", "-F", "#{session_id} #{window_id}"})
	out, err := c.exec(fullArgs)
	if err != nil {
		if strings.Contains(string(out), "no server running") {
			return nil, nil
		}
		return nil, fmt.Errorf("list-windows: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// KillServer terminates the entire tmux server on the target socket. Best-effort.
func (c *Client) KillServer() error {
	_, _ = c.exec(c.args([]string{"kill-server"}))
	return nil
}

// Attach exec()s into `tmux attach-session -t name`, replacing atelier's process.
// Use this from CLI commands invoked from `tmux display-popup -E`.
//
// Attach uses syscall.Exec (process replacement) NOT exec.Command, so
// the DefaultTimeout doesn't apply — attach legitimately blocks
// forever (until the user detaches). That's the intended semantics.
func (c *Client) Attach(name string) error {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	args := []string{"tmux"}
	args = append(args, c.args([]string{"attach-session", "-t", "=" + name})...)
	return syscall.Exec(bin, args, os.Environ())
}

// DisplayMessage runs `tmux display-message -p <format>` and returns the result.
func (c *Client) DisplayMessage(format string) (string, error) {
	out, err := c.Run("display-message", "-p", format)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DisplayMessageAt runs `tmux display-message -p -t <target> <format>` and
// returns the result. Used when querying a specific session/window/pane.
func (c *Client) DisplayMessageAt(target, format string) (string, error) {
	out, err := c.Run("display-message", "-p", "-t", target, format)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ShowGlobalOption reads a global tmux option set by `set-option -g <name>`.
// Returns "" (not an error) if the option is unset.
func (c *Client) ShowGlobalOption(name string) (string, error) {
	fullArgs := c.args([]string{"show-options", "-gv", name})
	out, err := c.exec(fullArgs)
	if err != nil {
		if isOptionUnset(out) {
			return "", nil
		}
		return "", fmt.Errorf("show-options -gv %s: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// SetGlobalOption sets a global tmux option (`set-option -g`).
func (c *Client) SetGlobalOption(name, value string) error {
	_, err := c.Run("set-option", "-g", name, value)
	return err
}

// UnsetGlobalOption removes a global tmux option (`set-option -gu`).
func (c *Client) UnsetGlobalOption(name string) error {
	_, err := c.Run("set-option", "-gu", name)
	return err
}

// GetWindowOption reads a window option from the given window.
func (c *Client) GetWindowOption(windowID, name string) (string, error) {
	fullArgs := c.args([]string{"show-window-options", "-v", "-t", windowID, name})
	out, err := c.exec(fullArgs)
	if err != nil {
		if isOptionUnset(out) {
			return "", nil
		}
		return "", fmt.Errorf("show-window-options: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// SetWindowOption sets a window option on the given window.
func (c *Client) SetWindowOption(windowID, name, value string) error {
	_, err := c.Run("set-window-option", "-t", windowID, name, value)
	return err
}

// UnsetWindowOption removes a window option (with -u).
func (c *Client) UnsetWindowOption(windowID, name string) error {
	_, err := c.Run("set-window-option", "-t", windowID, "-u", name)
	return err
}

func isOptionUnset(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "unknown option") || strings.Contains(s, "no such option")
}
