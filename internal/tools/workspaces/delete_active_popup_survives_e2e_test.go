//go:build e2e

package workspaces_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// markerAlive reports whether a process whose command line contains
// `marker` is currently running. Used to detect the stand-in M-s picker
// popup (a `sleep` overlay tagged with a unique marker) without parsing
// tmux's popup internals.
func markerAlive(marker string) bool {
	return exec.Command("pgrep", "-f", marker).Run() == nil
}

// TestDeleteRow_ActiveDelete_PickerPopupSurvives locks in the load-bearing
// invariant behind "M-x the workspace you're on and keep browsing": the M-s
// picker is a display-popup overlay riding on the outer client, and deleting
// the workspace that client is parked on must NOT tear it down. _delete-row
// achieves this by hopping the outer to a sibling (switch-client) BEFORE the
// kill — a popup survives both a switch-client and the subsequent
// kill-session, so the picker stays visible while focus shifts underneath and
// the list reloads. A regression that reordered kill-before-switch (or
// dropped the hop) would detach the client and kill the popup — this test
// fails loudly if that happens.
func TestDeleteRow_ActiveDelete_PickerPopupSurvives(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("bootstrap") // keep the server alive independent of workspaces
	time.Sleep(150 * time.Millisecond)

	// Build the atelier-* binaries before overriding HOME below.
	_ = srv.BinDir()

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	// Victim (active) workspace + a sibling to land on.
	seedWorkspaceSession(t, srv, "vyrwu/demo", "main", repoDir)
	seedWorkspaceSession(t, srv, "vyrwu/other", "main", repoDir)

	_ = srv.Attach(t, "vyrwu/demo")
	clientName := registerOuterClient(t, srv, "vyrwu/demo")

	// Stand in for the M-s picker: a popup overlay on the outer client,
	// tagged with a socket-unique marker so pgrep can find exactly this one.
	marker := "ATELIER_PICKER_MARKER_" + strings.ReplaceAll(srv.Socket, "-", "_")
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", marker).Run() })
	if _, err := srv.Client.Run("run-shell", "-b", fmt.Sprintf(
		"tmux -L %s display-popup -c %s -B -w70%% -h70%% -E 'sleep 300 # %s'",
		srv.Socket, clientName, marker)); err != nil {
		t.Fatalf("open picker popup: %v", err)
	}
	testtmux.Eventually(t, 3*time.Second, func() error {
		if !markerAlive(marker) {
			return fmt.Errorf("picker popup not up yet")
		}
		return nil
	})

	// Delete the ACTIVE workspace's default-branch sole window → kill-session.
	if _, err := srv.RunAtelier("tools", "workspaces", "_delete-row",
		"vyrwu/demo\tmain\t<display>"); err != nil {
		t.Fatalf("_delete-row: %v", err)
	}

	// Victim gone + focus shifted to the sibling (existing guarantees).
	testtmux.Eventually(t, 3*time.Second, func() error {
		if has, _ := srv.Client.HasSession("vyrwu/demo"); has {
			return fmt.Errorf("victim session vyrwu/demo still present")
		}
		return nil
	})
	outerClientOn(t, srv, clientName, "vyrwu/other")

	// The picker popup MUST still be alive — M-s stays visible through the
	// self-delete instead of collapsing onto a bare workspace.
	if !markerAlive(marker) {
		t.Fatal("picker popup was torn down by the self-delete — M-s must stay visible while focus shifts")
	}
}
