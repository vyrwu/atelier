//go:build e2e

package workspace_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestEnsureSession_DottedSessionName is the regression guard for the
// "creating a workspace for cloudnativedenmark.dk breaks" bug. tmux rewrites
// '.' and ':' to '_' in session names, so a raw "…dk" target has tmux parsing
// ".dk" as a window/pane and every -t operation fails. With the
// SessionName-normalized identity, EnsureSession resolves cleanly end-to-end.
func TestEnsureSession_DottedSessionName(t *testing.T) {
	srv := testtmux.New(t)

	session := workspace.SessionName("cloudnativedenmark/cloudnativedenmark.dk")
	if strings.ContainsAny(session, ".:") {
		t.Fatalf("SessionName left a tmux delimiter in %q", session)
	}

	root := t.TempDir()
	created, err := workspace.EnsureSession(srv.Client, session, root, "agent")
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if !created {
		t.Fatalf("EnsureSession created=false, want true for a fresh session")
	}
	// The session must be resolvable by the SAME name we used to create it —
	// this is exactly what broke when the name still carried the dot.
	if has, _ := srv.Client.HasSession(session); !has {
		t.Fatalf("session %q not resolvable after EnsureSession", session)
	}

	// @workspace_id (the Listable marker) is stamped and equals the session.
	got, _ := srv.Client.Run("show-option", "-t", session, "-qv", workspace.OptWorkspaceID)
	if strings.TrimSpace(string(got)) != session {
		t.Fatalf("@workspace_id = %q, want %q", strings.TrimSpace(string(got)), session)
	}

	// Window 1 is marked the driver.
	wid, err := srv.Client.DisplayMessageAt(session+":1", "#{window_id}")
	if err != nil || wid == "" {
		t.Fatalf("resolve window 1: %v", err)
	}
	drv, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceDriver)
	if drv != "1" {
		t.Fatalf("@workspace_driver on window 1 = %q, want 1", drv)
	}
}
