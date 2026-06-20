//go:build e2e

// PTY-driven integration tests for the claude tool. The Stop hook
// integration (notify-attention) is exercised here end-to-end against
// a running tmux server.
package claude_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestNotifyAttention_FlagsOuterWindow simulates the Claude Stop hook
// firing and verifies that @needs_attention=1 is stamped on the
// specified outer window.
func TestNotifyAttention_FlagsOuterWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)

	out, err := srv.Client.Run("list-windows", "-a", "-F", "#{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	wid := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])

	res, err := srv.RunAtelier(
		"tools", "claude", "notify-attention",
		"--window", wid,
	)
	if err != nil {
		t.Fatalf("notify-attention: %v\n%s", err, res)
	}

	testtmux.Eventually(t, 2*time.Second, func() error {
		v, _ := srv.Client.GetWindowOption(wid, "@needs_attention")
		if v != "1" {
			return fmt.Errorf("@needs_attention=%q, want 1", v)
		}
		return nil
	})
}
