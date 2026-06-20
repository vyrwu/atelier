//go:build e2e

package cli_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestClearPopup_ClearsAttentionOnParentWindow simulates the
// client-session-changed hook firing: parent workspace has @needs_attention
// raised, then the user switches into the atelier popup. clear-popup is
// invoked with the popup's session name and should clear attention on the
// parent.
func TestClearPopup_ClearsAttentionOnParentWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	w, err := workspace.Info(srv.Client, "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if err := workspace.SetAttention(srv.Client, w.WindowID, true); err != nil {
		t.Fatalf("SetAttention: %v", err)
	}
	workPaneID := w.PaneID

	// Create the matching atelier popup session for this parent.
	popupName := "_atelier_claude_" + digitsOnly(w.SessionID) + "_" + digitsOnly(w.WindowID)
	if err := srv.Client.NewSession(popupName, true); err != nil {
		t.Fatalf("NewSession popup: %v", err)
	}

	// Invoke clear-popup with the popup session as explicit --session
	// (mimicking what the hook does when the client moves into it).
	out, err := srv.RunAtelier(
		"status", "attention", "clear-popup",
		"--socket", srv.Socket,
		"--session", popupName,
	)
	if err != nil {
		t.Fatalf("RunAtelier: %v\n%s", err, out)
	}

	// Query info against the original work pane explicitly — tmux's
	// "current" session may have shifted after we created the popup.
	again, err := workspace.Info(srv.Client, workPaneID)
	if err != nil {
		t.Fatalf("Info after: %v", err)
	}
	if again.Attention {
		t.Fatalf("expected Attention=false after clear-popup, still set on window %s", again.WindowID)
	}
}

func TestClearPopup_IgnoresNonAtelierSession(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	w, _ := workspace.Info(srv.Client, "")
	_ = workspace.SetAttention(srv.Client, w.WindowID, true)
	workPaneID := w.PaneID

	out, err := srv.RunAtelier(
		"status", "attention", "clear-popup",
		"--socket", srv.Socket,
		"--session", "some-random-workspace",
	)
	if err != nil {
		t.Fatalf("RunAtelier: %v\n%s", err, out)
	}

	// Attention should still be set; clear-popup is a no-op for non-popup sessions.
	again, _ := workspace.Info(srv.Client, workPaneID)
	if !again.Attention {
		t.Fatalf("clear-popup wrongly cleared attention on a non-popup session change")
	}
}

func digitsOnly(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}

// silence unused-import flagging when this build tag isn't active
var _ = strings.HasPrefix
