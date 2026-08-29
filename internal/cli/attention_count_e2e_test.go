//go:build e2e

package cli

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestCountAttentionWindows_MatchesPicker locks the badge rollup to the same
// inclusion predicate as the M-s picker (workspace.Listable). Only windows a
// user could actually open from the picker may inflate the ⏺ count. A window is
// listable iff its session carries @workspace_id:
//   - workspace with @workspace_id + attention → counted
//   - a second workspace with @workspace_id + attention → counted
//   - a raw window with attention but no @workspace_id → NOT counted
//     (the phantom-notification bug: a ⏺ with no picker row behind it)
//   - a popup backing session (@needs_attention misrouted) → NOT counted
//   - a listable workspace with no attention → NOT counted
func TestCountAttentionWindows_MatchesPicker(t *testing.T) {
	srv := testtmux.New(t)

	// seed a session with an optional @workspace_id (SESSION-scoped, as
	// production stamps it) and an optional @needs_attention on its window
	// (WINDOW-scoped). Mirrors the real option scopes so the read paths match.
	seed := func(session, workspaceID string, attention bool) {
		srv.NewSession(session)
		if workspaceID != "" {
			if _, err := srv.Client.Run("set-option", "-t", session, "@workspace_id", workspaceID); err != nil {
				t.Fatalf("set @workspace_id on %s: %v", session, err)
			}
		}
		if attention {
			out, err := srv.Client.Run("display-message", "-p", "-t", session+":1", "#{window_id}")
			if err != nil {
				t.Fatalf("resolve window id for %s: %v", session, err)
			}
			wid := strings.TrimSpace(string(out))
			if err := srv.Client.SetWindowOption(wid, "@needs_attention", "1"); err != nil {
				t.Fatalf("set @needs_attention on %s: %v", session, err)
			}
		}
	}

	seed("vyrwu/one", "vyrwu/one", true)
	seed("vyrwu/two", "vyrwu/two", true)
	seed("bare/no-metadata", "", true)      // phantom: attention, no @workspace_id
	seed("_atelier_claude_9_9", "", true)   // misrouted popup
	seed("vyrwu/idle", "vyrwu/idle", false) // listable but no attention

	if got := countAttentionWindows(srv.Client); got != 2 {
		t.Errorf("countAttentionWindows = %d, want 2 (the two workspaces with attention)", got)
	}
}
