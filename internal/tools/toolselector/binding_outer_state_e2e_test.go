//go:build e2e

package toolselector_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestMSemi_StampsRealIDs_NotLiteralFormat asserts that when M-; fires,
// the binding chain expands `#{pane_id}`, `#{session_id}`, `#{window_id}`
// against the active pane — NOT stores the literal `#{...}` strings.
//
// Bug history: initgen emitted `set-option -g` instead of `set-option -gF`.
// Without -F tmux stores the literal format string verbatim, so:
//
//   - @atelier_outer_session = "#{session_id}"  (literal, not "$0")
//   - @atelier_outer_window  = "#{window_id}"   (literal, not "@0")
//
// All downstream tools that compose the Claude/popup-shell backing
// session name as `_atelier_<tool>_<sid-digits>_<wid-digits>` then end
// up with empty digits → session names like `_atelier_claude__`, which
// don't match BuildSessionList's lookup → no claude markers and no
// recap suffixes in the workspace selector.
func TestMSemi_StampsRealIDs_NotLiteralFormat(t *testing.T) {
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

		if strings.Contains(pane, "#{") || strings.Contains(sess, "#{") || strings.Contains(win, "#{") {
			return fmt.Errorf("globals contain literal #{...} (binding missing -F): pane=%q session=%q window=%q",
				pane, sess, win)
		}
		if !strings.HasPrefix(pane, "%") {
			return fmt.Errorf("@atelier_outer_pane=%q, want %%N", pane)
		}
		if !strings.HasPrefix(sess, "$") {
			return fmt.Errorf("@atelier_outer_session=%q, want $N", sess)
		}
		if !strings.HasPrefix(win, "@") {
			return fmt.Errorf("@atelier_outer_window=%q, want @N", win)
		}
		return nil
	})
}

// TestMSemi_CapturesOuterClient asserts @atelier_outer_client gets
// stamped with the PTY client's name. Critical when multiple clients
// are attached: switch-client without -c picks one non-deterministically.
//
// Bug history: the user had `_atelier_popupshell__` attached alongside
// their real workspace client; switch-client landed on the popupshell
// session instead of the workspace. The fix targets `-c <outer-client>`,
// which only works if the binding captured the client name first.
func TestMSemi_CapturesOuterClient(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	client := srv.Attach(t, "main")
	time.Sleep(300 * time.Millisecond)

	client.Send("\x1b;")
	testtmux.Eventually(t, 3*time.Second, func() error {
		v, _ := srv.Client.ShowGlobalOption("@atelier_outer_client")
		if v == "" || strings.Contains(v, "#{") {
			return fmt.Errorf("@atelier_outer_client=%q (binding missing #{client_name} -F?)", v)
		}
		return nil
	})
}
