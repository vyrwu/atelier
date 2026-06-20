//go:build e2e

package testtmux

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// AttachedClient drives the test tmux server through a real attached tmux
// client running in a pseudo-terminal. This is the *integration* layer —
// it exercises the actual display-popup → fzf → send-keys → switch-client
// chain the user sees. Headless `Server.RunAtelier` tests can't catch
// popup-rendering bugs; this can.
//
// Usage:
//
//	srv := testtmux.New(t)
//	srv.NewSession("main")
//	srv.SourceInit(t)              // wire atelier bindings into the server
//	client := srv.Attach(t, "main")
//	client.Send("\x1b;")           // M-;
//	client.WaitFor(t, "Select Tool", 2*time.Second)
//	client.Send("\n")              // accept first entry
type AttachedClient struct {
	pty io.ReadWriteCloser
	cmd *exec.Cmd
	srv *Server
	t   *testing.T
}

// Attach starts `tmux -L <socket> attach-session -t <session>` in a PTY,
// returning a handle that drains output and lets tests inject keystrokes.
// The client is killed in t.Cleanup.
//
// rows/cols default to 40x120 if zero — large enough for fzf/popup layouts.
func (s *Server) Attach(t *testing.T, session string) *AttachedClient {
	t.Helper()
	cmd := exec.Command("tmux", "-L", s.Socket, "attach-session", "-t", session)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("pty.StartWithSize: %v", err)
	}
	// Drain output continuously so the PTY doesn't block on backpressure.
	// We never read its contents — screen state is captured via tmux IPC
	// (capture-pane), which is more reliable than parsing terminal escapes.
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()

	c := &AttachedClient{pty: ptmx, cmd: cmd, srv: s, t: t}
	t.Cleanup(c.Close)
	// Give tmux a moment to actually attach before the test sends keys.
	c.waitForClientAttached(2 * time.Second)
	return c
}

// Send writes the given keystrokes to the PTY. Use the raw bytes the
// terminal would send — e.g. "\x1b;" for Alt-;, "\r" for Enter, "\x03"
// for Ctrl-C.
//
// Convenience: callers can compose long input sequences via fmt.Sprintf
// before Send — there is no internal escaping.
func (c *AttachedClient) Send(keys string) {
	c.t.Helper()
	if _, err := c.pty.Write([]byte(keys)); err != nil {
		c.t.Fatalf("Send: %v", err)
	}
}

// SendLine is Send(keys + "\r"). Convenience for typing-and-Enter.
func (c *AttachedClient) SendLine(line string) { c.Send(line + "\r") }

// Close detaches the client and tears down the PTY. Called automatically
// from t.Cleanup; callers don't normally invoke it.
func (c *AttachedClient) Close() {
	_ = c.pty.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_, _ = c.cmd.Process.Wait()
}

// Screen returns the visible content of the active pane on the test
// session. tmux's capture-pane is authoritative here — it bypasses any
// stray terminal escapes that might confuse a manual stdout parser.
func (c *AttachedClient) Screen() string {
	out, err := c.srv.Client.Run("capture-pane", "-p")
	if err != nil {
		c.t.Fatalf("capture-pane: %v", err)
	}
	return string(out)
}

// PopupScreen captures the visible content of the first detected atelier
// popup session (either `_atelier_*` or bash `_*pop*` prefix). Returns ""
// when no popup is open.
func (c *AttachedClient) PopupScreen() string {
	sessions := c.srv.Sessions()
	for _, s := range sessions {
		if !isPopupSession(s) {
			continue
		}
		out, err := c.srv.Client.Run("capture-pane", "-p", "-t", s+":0")
		if err == nil {
			return string(out)
		}
	}
	return ""
}

// WaitFor polls Screen() + PopupScreen() until substring appears or
// timeout elapses. On timeout, fails the test with the last screen
// contents for debugging.
func (c *AttachedClient) WaitFor(substring string, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(c.Screen(), substring) ||
			strings.Contains(c.PopupScreen(), substring) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.t.Fatalf(
		"WaitFor(%q) timed out after %s.\n--- main screen ---\n%s\n--- popup screen ---\n%s",
		substring, timeout, c.Screen(), c.PopupScreen())
}

// WaitForSession polls until a session with the given name exists, or
// fails the test on timeout. Useful when verifying that a binding
// actually created a workspace.
func (c *AttachedClient) WaitForSession(name string, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		has, _ := c.srv.Client.HasSession(name)
		if has {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.t.Fatalf("session %q not created within %s. Sessions=%v", name, timeout, c.srv.Sessions())
}

// WaitForNoPopup polls until every popup-prefix session is gone, or
// fails on timeout. Useful for asserting that dispatched popups closed
// cleanly after a switch-client.
func (c *AttachedClient) WaitForNoPopup(timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		open := false
		for _, s := range c.srv.Sessions() {
			if isPopupSession(s) {
				open = true
				break
			}
		}
		if !open {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.t.Fatalf("popup session still open after %s. Sessions=%v", timeout, c.srv.Sessions())
}

func (c *AttachedClient) waitForClientAttached(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := c.srv.Client.Run("list-clients", "-F", "#{client_name}")
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("tmux client did not attach within %s", timeout)
}

func isPopupSession(name string) bool {
	if strings.HasPrefix(name, "_atelier_") {
		return true
	}
	for _, p := range []string{"_popup_", "_claudepop_", "_k8spop_", "_awspop_", "_lazygitpop_", "_atelier"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Common key escape sequences for convenience.
const (
	KeyEnter  = "\r"
	KeyEsc    = "\x1b"
	KeyAltSC  = "\x1b;" // Alt-;
	KeyAltN   = "\x1bn" // Alt-n
	KeyAltS   = "\x1bs" // Alt-s
	KeyCtrlC  = "\x03"
	KeyCtrlN  = "\x0e"
	KeyCtrlS  = "\x13"
	KeyCtrlU  = "\x15"
	KeyCtrlA  = "\x01"
	KeyCtrlX  = "\x18"
	KeyUp     = "\x1b[A"
	KeyDown   = "\x1b[B"
	KeyRight  = "\x1b[C"
	KeyLeft   = "\x1b[D"
)

// Eventually retries fn until it returns nil or timeout. Used in tests
// to wait for asynchronous tmux state changes without arbitrary sleeps.
func Eventually(t *testing.T, timeout time.Duration, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Eventually timed out: %v", last)
}

// fmtScreen is a debug helper for failure messages — currently unused
// but kept here so test authors can drop it in when diagnosing.
var _ = fmt.Sprintf
