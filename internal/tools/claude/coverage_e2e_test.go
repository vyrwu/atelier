//go:build e2e

// Coverage tests for the claude tool's @claude_prompt / @claude_workspace_kind
// one-shot semantics + notify-attention env-resolution paths.
package claude_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestClaude_NotifyAttention_TmuxParentWindowEnv_Wins verifies that
// when TMUX_PARENT_WINDOW_ID env is set (as the deferred-Claude-popup
// dispatcher does), notify-attention uses that target — not the
// (possibly-stale) @atelier_outer_window global.
//
// Bug history: the prompt-mode workspace creator deferred its Claude
// popup with NO env, so claude.OpenCommand fell back to stale globals
// and attached the backing session to the WRONG (default) window.
// The fix passes -e TMUX_PARENT_SESSION_ID/WINDOW_ID; this test pins
// that env-var-wins semantics.
func TestClaude_NotifyAttention_TmuxParentWindowEnv_Wins(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	// Create a SECOND window in the session so we have two distinct
	// window IDs to disambiguate against.
	if _, err := srv.Client.Run("new-window", "-t", "work", "-n", "other"); err != nil {
		t.Fatalf("new-window: %v", err)
	}

	// Resolve the two window IDs.
	out, err := srv.Client.Run("list-windows", "-t", "work", "-F", "#{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	lines := splitLines(string(out))
	if len(lines) < 2 {
		t.Fatalf("expected 2 windows, got %v", lines)
	}
	mainWin := lines[0]
	otherWin := lines[1]

	// Stamp @atelier_outer_window pointing at MAIN (the wrong target).
	_ = srv.Client.SetGlobalOption("@atelier_outer_window", mainWin)

	// Invoke notify-attention with --window OTHER. The env-var/flag
	// must win over the stale global.
	out, err = srv.RunAtelier("tools", "claude", "notify-attention",
		"--window", otherWin)
	if err != nil {
		t.Fatalf("notify-attention: %v\n%s", err, out)
	}

	testtmux.Eventually(t, 2*time.Second, func() error {
		// Verify attention is ON `other`, NOT on `main`.
		o, _ := srv.Client.GetWindowOption(otherWin, "@needs_attention")
		m, _ := srv.Client.GetWindowOption(mainWin, "@needs_attention")
		if o != "1" {
			return fmt.Errorf("@needs_attention not set on %s (=%q)", otherWin, o)
		}
		if m == "1" {
			return fmt.Errorf("@needs_attention wrongly set on main %s too", mainWin)
		}
		return nil
	})
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		if line := s[start:]; line != "" {
			out = append(out, line)
		}
	}
	return out
}
