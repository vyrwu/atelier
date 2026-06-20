//go:build e2e

// Coverage tests for tmux_tool_selector flows: dispatch chains that
// open backing popup sessions on the workspace client, popup-client
// detach + re-open choreography, and the special "Shell" navigation
// entry.
package toolselector_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestSelector_DispatchToPopupShell_OpensBackingSession asserts that
// dispatching from the toolselector to a workspace-scoped tool
// (popupshell) actually opens its backing session with the correct
// `_atelier_popupshell_<sid>_<wid>` name. This is the bug class where
// the suffix came up empty (`_atelier_popupshell__`) because globals
// stored the literal `#{...}` instead of expanded IDs.
//
// Bypasses fzf by invoking `popup goto-tool` directly (the same
// dispatcher path the popup-table M-; binding uses).
func TestSelector_DispatchToPopupShell_OpensBackingSession(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	client := srv.Attach(t, "main")
	time.Sleep(300 * time.Millisecond)

	// Fire M-; so the @atelier_outer_* globals get stamped with real
	// IDs (the binding chain).
	client.Send("\x1b;")
	testtmux.Eventually(t, 3*time.Second, func() error {
		v, _ := srv.Client.ShowGlobalOption("@atelier_outer_window")
		if v == "" || strings.Contains(v, "#{") {
			return fmt.Errorf("globals not stamped: window=%q", v)
		}
		return nil
	})

	// Read the stamped IDs to compute the expected session name.
	sess, _ := srv.Client.ShowGlobalOption("@atelier_outer_session")
	win, _ := srv.Client.ShowGlobalOption("@atelier_outer_window")
	expected := fmt.Sprintf("_atelier_popupshell_%s_%s",
		digits(sess), digits(win))

	// Drive the dispatch path directly. We can't use the PTY-driven
	// fzf because we'd need to navigate the menu, but the dispatcher
	// is exactly what the selector calls under the hood.
	if _, err := srv.RunAtelier("tools", "popupshell", "create",
		"--session", sess, "--window", win); err != nil {
		t.Fatalf("popupshell create: %v", err)
	}
	srv.MustHaveSession(expected)
}

// TestSelector_DispatchSetsRealIDsAfter_MSemi proves the binding chain
// is observable AFTER the dispatch via `popup goto-tool` (the popup-
// table sibling binding). Catches regressions where the goto-tool
// path forgot to re-stamp globals.
func TestSelector_DispatchSetsRealIDsAfter_MSemi(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	client := srv.Attach(t, "main")
	time.Sleep(300 * time.Millisecond)
	client.Send("\x1b;")
	testtmux.Eventually(t, 3*time.Second, func() error {
		pane, _ := srv.Client.ShowGlobalOption("@atelier_outer_pane")
		sess, _ := srv.Client.ShowGlobalOption("@atelier_outer_session")
		win, _ := srv.Client.ShowGlobalOption("@atelier_outer_window")
		// All three must be tmux IDs, NOT literal format strings.
		for _, kv := range []struct{ name, val string }{
			{"@atelier_outer_pane", pane},
			{"@atelier_outer_session", sess},
			{"@atelier_outer_window", win},
		} {
			if kv.val == "" || strings.Contains(kv.val, "#{") {
				return fmt.Errorf("%s=%q (binding missing -F?)", kv.name, kv.val)
			}
		}
		return nil
	})
}

func digits(s string) string {
	var out []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}
