//go:build e2e

package testtmux

import (
	"os"
	"path/filepath"
	"testing"
)

// SourceInit runs `atelier init` against the freshly-built core binary
// and sources its output into the test tmux server, wiring all atelier
// bindings + hooks + statusline. Without this, M-; etc. aren't bound.
//
// Also sets `escape-time 0` so PTY-injected escape sequences (e.g. "\x1b;"
// for Alt-;) are dispatched as keybinds without tmux's default 500ms
// wait-for-more-input window. Production users tune this in their own
// tmux.conf; tests need it as a baseline.
//
// Equivalent to the user's setup line plus a low escape-time:
//
//	set -g escape-time 0
//	run-shell 'atelier init | tmux source-file -'
func (s *Server) SourceInit(t *testing.T) {
	t.Helper()
	// Use a small positive escape-time so tmux still recognizes Meta-key
	// sequences (ESC followed by a key). With 0 tmux dispatches ESC
	// alone before the follow-up arrives, breaking M-;, M-n, M-s.
	if _, err := s.Client.Run("set-option", "-g", "escape-time", "50"); err != nil {
		t.Fatalf("set-option escape-time: %v", err)
	}
	// Inject the test bin dir into the server's update-environment list
	// AND the global env so popup -E + run-shell commands resolve the
	// freshly-built test binaries instead of any system-installed atelier.
	binPath := s.BinDir() + string(os.PathListSeparator) + os.Getenv("PATH")
	if _, err := s.Client.Run("set-environment", "-g", "PATH", binPath); err != nil {
		t.Fatalf("set-environment PATH: %v", err)
	}
	// Set ATELIER_TMUX_SOCKET globally so subprocesses spawned by
	// run-shell (popup cleanup, restore, stamp-statusline) can detect
	// they're running on a testtmux socket and apply test-mode
	// behavior — e.g. popup cleanup's --startup path skips its sweep
	// on `atelier-test-*` sockets so test-orphan fixtures survive.
	if _, err := s.Client.Run("set-environment", "-g", "ATELIER_TMUX_SOCKET", s.Socket); err != nil {
		t.Fatalf("set-environment ATELIER_TMUX_SOCKET: %v", err)
	}
	out, err := s.RunAtelier("init")
	if err != nil {
		t.Fatalf("atelier init: %v\n%s", err, out)
	}
	cfg := filepath.Join(t.TempDir(), "atelier.conf")
	if err := os.WriteFile(cfg, out, 0o644); err != nil {
		t.Fatalf("write atelier.conf: %v", err)
	}
	if _, err := s.Client.Run("source-file", cfg); err != nil {
		t.Fatalf("source-file %s: %v", cfg, err)
	}
}
