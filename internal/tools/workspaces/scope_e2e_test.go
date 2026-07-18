//go:build e2e

package workspaces_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestSetScopePin_TogglesGlobalAndFooter drives the M-p wiring end to end.
// First press (not pinned) persists @atelier_scope_pin and echoes the fzf
// actions that add a trailing space and light the Pinned badge. Second
// press (already pinned) clears the scope and echoes clear-query plus a
// badge-free footer — the unpin toggle.
func TestSetScopePin_TogglesGlobalAndFooter(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	time.Sleep(150 * time.Millisecond)

	// Pin.
	out, err := srv.RunAtelier("tools", "workspaces", "_set-scope-pin", "atelier")
	if err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if s := string(out); !strings.Contains(s, "put( )") || !strings.Contains(s, "Pinned") {
		t.Errorf("pin output missing put/badge, got:\n%q", s)
	}
	if got, _ := srv.Client.ShowGlobalOption("@atelier_scope_pin"); got != "atelier" {
		t.Fatalf("@atelier_scope_pin = %q, want atelier", got)
	}

	// Unpin (query is ignored on the second press — state, not query,
	// drives the toggle).
	out2, err := srv.RunAtelier("tools", "workspaces", "_set-scope-pin", "atelier")
	if err != nil {
		t.Fatalf("unpin: %v\n%s", err, out2)
	}
	if s := string(out2); !strings.Contains(s, "clear-query") || strings.Contains(s, "Pinned") {
		t.Errorf("unpin output must clear-query and drop the badge, got:\n%q", s)
	}
	if got, _ := srv.Client.ShowGlobalOption("@atelier_scope_pin"); got != "" {
		t.Errorf("after unpin @atelier_scope_pin = %q, want empty", got)
	}
}

// TestSetScopePin_EmptyQueryNoOp proves M-p on an empty query with no pin
// does nothing: no scope is written and no picker action is emitted.
func TestSetScopePin_EmptyQueryNoOp(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	time.Sleep(150 * time.Millisecond)

	out, err := srv.RunAtelier("tools", "workspaces", "_set-scope-pin", "")
	if err != nil {
		t.Fatalf("_set-scope-pin: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("empty-query no-op should emit nothing, got:\n%q", out)
	}
	if got, _ := srv.Client.ShowGlobalOption("@atelier_scope_pin"); got != "" {
		t.Errorf("@atelier_scope_pin = %q, want empty", got)
	}
}
