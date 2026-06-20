//go:build e2e

package workspace_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestLandOuter_DetachesPopupForOtherWorkspace covers the bug the
// user just hit: claude popup open on workspace A, M-s to workspace
// B, claude popup stays visible on top of B because it's on its own
// popup-pty client. LandOuter must detach popup-clients whose
// backing session doesn't match the target workspace's
// (session_id, window_id) tuple.
//
// Pure-unit coverage of the scoping rule lives in
// TestShouldDetachPopupClient (no tmux required); this e2e exists to
// catch end-to-end glue regressions (list-clients format, run-shell
// firing, etc).
func TestLandOuter_DetachesPopupForOtherWorkspace(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("ws-a")
	srv.NewSession("ws-b")
	time.Sleep(150 * time.Millisecond)

	// Read the sid/wid of ws-a's first window so we can name a
	// popup-backing session that's scoped to it.
	out, _ := srv.Client.Run("display-message", "-p", "-t", "ws-a",
		"#{session_id}|#{window_id}")
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) != 2 {
		t.Fatalf("display-message: %q", out)
	}
	sidA := strings.TrimPrefix(parts[0], "$")
	widA := strings.TrimPrefix(parts[1], "@")

	// Create a popup-backing session as if a Claude popup had been
	// opened on ws-a, and attach a client to it (the popup-client).
	popupSess := "_atelier_claude_" + sidA + "_" + widA
	if err := srv.Client.NewSession(popupSess, true); err != nil {
		t.Fatalf("create popup session: %v", err)
	}
	popupClient := srv.Attach(t, popupSess)
	defer popupClient.Close()
	time.Sleep(150 * time.Millisecond)

	beforeOut, _ := srv.Client.Run("list-clients", "-F", "#{client_session}")
	if !strings.Contains(string(beforeOut), popupSess) {
		t.Fatalf("popup client not attached. list-clients:\n%s", beforeOut)
	}

	// Land on ws-b. Since ws-b has a different (sid, wid) than
	// ws-a, the popup-client should be detached.
	if err := workspace.LandOuter(srv.Client, "=ws-b", "=ws-b:1"); err != nil {
		t.Fatalf("LandOuter: %v", err)
	}
	// run-shell -b fires async; give tmux a beat.
	time.Sleep(400 * time.Millisecond)

	afterOut, _ := srv.Client.Run("list-clients", "-F", "#{client_session}")
	if strings.Contains(string(afterOut), popupSess) {
		t.Errorf("popup client for ws-a was not detached after landing on ws-b. list-clients:\n%s",
			afterOut)
	}
}
