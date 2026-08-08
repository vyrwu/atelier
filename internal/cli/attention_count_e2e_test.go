//go:build e2e

package cli

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestCountAttentionWindows_MatchesPicker locks the badge rollup to the same
// inclusion predicate as the M-s picker (workspace.Listable). Only windows a
// user could actually open from the picker may inflate the ⏺ count:
//   - worktree workspace (@repo_path)      → counted
//   - repo-less multi-repo (@ai_workspace_kind) → counted
//   - a raw window with attention but no workspace metadata → NOT counted
//     (the phantom-notification bug: a ⏺ with no picker row behind it)
//   - a popup backing session (@needs_attention misrouted) → NOT counted
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

	seed("repo/worktree", map[string]string{"@repo_path": "/tmp/x", "@needs_attention": "1"})
	seed("auto/multi-repo", map[string]string{"@ai_workspace_kind": "multi-repo", "@needs_attention": "1"})
	seed("bare/no-metadata", map[string]string{"@needs_attention": "1"})    // phantom
	seed("_atelier_claude_9_9", map[string]string{"@needs_attention": "1"}) // misrouted popup
	seed("repo/idle", map[string]string{"@repo_path": "/tmp/y"})            // listable but no attention

	if got := countAttentionWindows(srv.Client); got != 2 {
		t.Errorf("countAttentionWindows = %d, want 2 (worktree + multi-repo only)", got)
	}
}
