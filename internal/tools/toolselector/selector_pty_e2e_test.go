//go:build e2e

// PTY-driven integration tests for the toolselector. Verify that the
// `M-;` root binding (installed by `atelier init`) is actually
// triggerable by real keyboard input from an attached client, and that
// its dispatch chain runs to completion (observable via side effects
// on tmux options).
//
// These complement the unit tests under selector_test.go which cover
// the entry-list construction and dispatch logic in isolation.
package toolselector_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestSelector_MSemi_FiresBinding asserts that pressing M-; via a
// PTY-attached client triggers the atelier-init binding chain
// (verified by @atelier_outer_pane being set as a side effect).
//
// Bug history: this was silently broken — display-popup from PTY M-;
// did nothing; without this test we couldn't tell whether bindings,
// key delivery, or the popup itself was at fault.
func TestSelector_MSemi_FiresBinding(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	client := srv.Attach(t, "main")
	time.Sleep(300 * time.Millisecond)

	client.Send("\x1b;")
	testtmux.Eventually(t, 3*time.Second, func() error {
		v, _ := srv.Client.ShowGlobalOption("@atelier_outer_pane")
		if v == "" {
			return fmt.Errorf("@atelier_outer_pane unset; M-; binding did not fire")
		}
		return nil
	})
}
