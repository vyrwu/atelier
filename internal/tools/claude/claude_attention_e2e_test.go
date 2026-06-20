//go:build e2e

package claude_test

import (
	"testing"

	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestNotifyAttention_SetsAttentionOnOuterWindow simulates Claude's Stop
// hook firing inside an atelier-spawned popup. The hook reads the outer
// window from atelier's global options and flags it.
func TestNotifyAttention_SetsAttentionOnOuterWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	w, err := workspace.Info(srv.Client, "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if err := srv.Client.SetGlobalOption(state.OptOuterPane, w.PaneID); err != nil {
		t.Fatalf("SetGlobalOption pane: %v", err)
	}
	if err := srv.Client.SetGlobalOption(state.OptOuterSession, w.SessionID); err != nil {
		t.Fatalf("SetGlobalOption session: %v", err)
	}
	if err := srv.Client.SetGlobalOption(state.OptOuterWindow, w.WindowID); err != nil {
		t.Fatalf("SetGlobalOption window: %v", err)
	}

	// Invoke atelier-claude notify-attention via the dispatcher.
	out, err := srv.RunAtelier(
		"tools", "claude", "notify-attention",
		"--socket", srv.Socket,
	)
	if err != nil {
		t.Fatalf("RunAtelier: %v\n%s", err, out)
	}

	again, err := workspace.Info(srv.Client, "")
	if err != nil {
		t.Fatalf("Info after: %v", err)
	}
	if !again.Attention {
		t.Fatalf("expected Attention=true after notify-attention, got false (window=%s)", again.WindowID)
	}
}
