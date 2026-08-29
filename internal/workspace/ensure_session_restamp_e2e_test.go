//go:build e2e

package workspace_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestEnsureSession_RestampsBareExistingSession guards the M-s-shrinking bug:
// the launcher's `new-session -A -s <name>` can recreate a killed workspace as
// a BARE session with no @workspace_id. When a later flow calls EnsureSession
// on that pre-existing session, it must re-stamp @workspace_id — otherwise the
// session is filtered out of the M-s picker and appears to have vanished.
func TestEnsureSession_RestampsBareExistingSession(t *testing.T) {
	srv := testtmux.New(t)
	// Simulate the launcher's bare `new-session -A` result: a session named
	// after the workspace but with NO @workspace_id stamped.
	srv.NewSession("vyrwu/atelier")
	if v, _ := srv.Client.Run("show-option", "-t", "vyrwu/atelier", "-qv", workspace.OptWorkspaceID); strings.TrimSpace(string(v)) != "" {
		t.Fatalf("precondition: bare session should have no @workspace_id, got %q", v)
	}

	// EnsureSession on the existing bare session.
	created, err := workspace.EnsureSession(srv.Client, "vyrwu/atelier", t.TempDir(), "agent")
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if created {
		t.Errorf("created=true, want false (session already existed)")
	}

	// The heal: @workspace_id is now stamped, so M-s will include this session.
	got, _ := srv.Client.Run("show-option", "-t", "vyrwu/atelier", "-qv", workspace.OptWorkspaceID)
	if strings.TrimSpace(string(got)) != "vyrwu/atelier" {
		t.Errorf("@workspace_id not restamped on existing bare session: %q", got)
	}
}
