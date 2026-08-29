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

	seed := func(session string, opts map[string]string) {
		srv.NewSession(session)
		out, err := srv.Client.Run("display-message", "-p", "-t", session+":1", "#{window_id}")
		if err != nil {
			t.Fatalf("resolve window id for %s: %v", session, err)
		}
		wid := strings.TrimSpace(string(out))
		for k, v := range opts {
			if err := srv.Client.SetWindowOption(wid, k, v); err != nil {
				t.Fatalf("set %s=%s on %s: %v", k, v, session, err)
			}
		}
	}

	seed("vyrwu/one", map[string]string{"@workspace_id": "vyrwu/one", "@needs_attention": "1"})
	seed("vyrwu/two", map[string]string{"@workspace_id": "vyrwu/two", "@needs_attention": "1"})
	seed("bare/no-metadata", map[string]string{"@needs_attention": "1"})    // phantom
	seed("_atelier_claude_9_9", map[string]string{"@needs_attention": "1"}) // misrouted popup
	seed("vyrwu/idle", map[string]string{"@workspace_id": "vyrwu/idle"})    // listable but no attention

	if got := countAttentionWindows(srv.Client); got != 2 {
		t.Errorf("countAttentionWindows = %d, want 2 (the two workspaces with attention)", got)
	}
}
