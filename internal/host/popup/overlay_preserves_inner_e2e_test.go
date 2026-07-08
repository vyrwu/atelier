//go:build e2e

package popup_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/testtmux"
)

// innerClientCount reports how many attached clients belong to a
// popup-managed (`_`-prefixed) session — i.e. how many tool popups are
// still open. Mirrors listClients' outer/inner split.
func innerClientCount(t *testing.T, srv *testtmux.Server) int {
	t.Helper()
	out, err := srv.Client.Run("list-clients", "-F", "#{client_session}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "_") {
			n++
		}
	}
	return n
}

// TestOpenOverInnerPopup_NestsOnInnerClient is the regression guard for the
// "Building workspace" spinner. It must render on top of the open Claude
// popup, NOT on the outer client (tmux allows only one popup per client, so
// a second popup on the outer client — occupied by the Claude popup — is
// silently dropped and the spinner's -E never runs). The fix nests the
// spinner on the inner (`_atelier_claude_*`) client instead.
//
// This asserts the -E command actually LAUNCHES (marker file written) and
// that the inner popup client is NOT detached in the process.
func TestOpenOverInnerPopup_NestsOnInnerClient(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.NewSession("_atelier_claude_0_0") // simulates the open Claude popup

	srv.Attach(t, "main")
	srv.Attach(t, "_atelier_claude_0_0")

	if got := innerClientCount(t, srv); got != 1 {
		t.Fatalf("setup: want 1 inner (popup) client, got %d", got)
	}

	marker := filepath.Join(t.TempDir(), "spinner-ran")
	invoke := fmt.Sprintf("touch %s", marker)
	if err := hostpopup.OpenOverInnerPopup(srv.Client, hostpopup.SpinnerStyleArgs("Building workspace"), invoke); err != nil {
		t.Fatalf("OpenOverInnerPopup: %v", err)
	}

	// The deferred display-popup nests on the inner client and runs -E.
	testtmux.Eventually(t, 3*time.Second, func() error {
		if _, err := os.Stat(marker); err != nil {
			return errSpinnerNotRun
		}
		return nil
	})

	// It must NOT have detached the underlying Claude popup client.
	if got := innerClientCount(t, srv); got != 1 {
		t.Fatalf("inner popup client was detached: inner client count=%d", got)
	}
}

// TestOpenOverInnerPopup_FallsBackToOuter covers the root-key-table path:
// the creator launched from the workspace directly, with no tool popup
// open. The spinner then nests on the outer client (nothing to render over)
// and still launches.
func TestOpenOverInnerPopup_FallsBackToOuter(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.Attach(t, "main")

	if got := innerClientCount(t, srv); got != 0 {
		t.Fatalf("setup: want 0 inner clients, got %d", got)
	}

	marker := filepath.Join(t.TempDir(), "spinner-ran")
	invoke := fmt.Sprintf("touch %s", marker)
	if err := hostpopup.OpenOverInnerPopup(srv.Client, hostpopup.SpinnerStyleArgs("Building workspace"), invoke); err != nil {
		t.Fatalf("OpenOverInnerPopup: %v", err)
	}

	testtmux.Eventually(t, 3*time.Second, func() error {
		if _, err := os.Stat(marker); err != nil {
			return errSpinnerNotRun
		}
		return nil
	})
}

var errSpinnerNotRun = scanErr("spinner -E did not run")
