//go:build e2e

package workspace_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestReturnToOuterShell_StaysOnOuterWindow covers the reported bug:
// "M-; → Shell, when already in the Shell, keeps switching to another
// workspace". Workspaces are windows within a tmux session; the old
// "Shell" entry ran `select-window -t ':1'` (window INDEX 1). Run from
// inside the selector popup — whose current session is the user's
// workspace — ':1' resolves to `<workspace>:1`, yanking the user to a
// different workspace whenever they weren't already on window index 1.
//
// ReturnToOuterShell instead selects @atelier_outer_window — the exact
// window id the M-; root binding recorded — which is unique across the
// server and so lands correctly regardless of the current-session
// context or window index.
//
// This exercises the no-popup-open branch (the path the real bug hits:
// the selector popup itself is not a "_"-prefixed backing session, so
// with no other tool popup open there are no inner clients to detach).
func TestReturnToOuterShell_StaysOnOuterWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("ws")
	// Three windows so a non-index-1 window exists (independent of the
	// server's base-index) and a distinct index-1 window exists for the
	// old ':1' code to have jumped to.
	if _, err := srv.Client.Run("new-window", "-t", "ws"); err != nil {
		t.Fatalf("new-window: %v", err)
	}
	if _, err := srv.Client.Run("new-window", "-t", "ws"); err != nil {
		t.Fatalf("new-window: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// Map window index -> window id.
	out, _ := srv.Client.Run("list-windows", "-t", "ws", "-F", "#{window_index} #{window_id}")
	byIndex := map[string]string{}
	var outerWin string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		byIndex[fields[0]] = fields[1]
		if fields[0] != "1" {
			outerWin = fields[1] // a window that isn't index 1
		}
	}
	win1 := byIndex["1"]
	if outerWin == "" || win1 == "" || outerWin == win1 {
		t.Fatalf("need a non-index-1 outer window distinct from :1; windows=%q", out)
	}

	// The user pressed M-; while on outerWin, so the root binding stamped
	// @atelier_outer_window to it. Park ws's active window on the index-1
	// window — where the old ':1' code would (wrongly) leave the user.
	if err := srv.Client.SetGlobalOption("@atelier_outer_window", outerWin); err != nil {
		t.Fatalf("stamp outer window: %v", err)
	}
	if _, err := srv.Client.Run("select-window", "-t", win1); err != nil {
		t.Fatalf("select-window win1: %v", err)
	}

	if err := workspace.ReturnToOuterShell(srv.Client); err != nil {
		t.Fatalf("ReturnToOuterShell: %v", err)
	}

	got, _ := srv.Client.Run("display-message", "-p", "-t", "ws", "#{window_id}")
	active := strings.TrimSpace(string(got))
	if active != outerWin {
		t.Errorf("after Shell return, ws active window = %q, want recorded outer %q (index-1 window %q is where the ':1' bug lands)",
			active, outerWin, win1)
	}
}
