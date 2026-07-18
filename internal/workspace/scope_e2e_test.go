//go:build e2e

package workspace_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestScopePin_GlobalRoundTrip locks in the sticky-scope primitive:
// SetScopePin writes the @atelier_scope_pin tmux GLOBAL (read back by
// GetScopePin), replaces on re-pin, and clears on empty. It is a global
// option — session-lived, never mirrored to the statestore — so nothing
// here touches the on-disk cache.
func TestScopePin_GlobalRoundTrip(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	time.Sleep(150 * time.Millisecond)

	if got := workspace.GetScopePin(srv.Client); got != "" {
		t.Fatalf("fresh server: GetScopePin = %q, want empty", got)
	}

	if err := workspace.SetScopePin(srv.Client, "atelier"); err != nil {
		t.Fatalf("SetScopePin: %v", err)
	}
	if got := workspace.GetScopePin(srv.Client); got != "atelier" {
		t.Errorf("GetScopePin = %q, want atelier", got)
	}

	// Re-pin replaces the previous value.
	if err := workspace.SetScopePin(srv.Client, "#infra"); err != nil {
		t.Fatalf("re-pin: %v", err)
	}
	if got := workspace.GetScopePin(srv.Client); got != "#infra" {
		t.Errorf("re-pin GetScopePin = %q, want #infra", got)
	}

	// Empty query clears the pin (the M-p toggle).
	if err := workspace.SetScopePin(srv.Client, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := workspace.GetScopePin(srv.Client); got != "" {
		t.Errorf("after clear GetScopePin = %q, want empty", got)
	}
}
