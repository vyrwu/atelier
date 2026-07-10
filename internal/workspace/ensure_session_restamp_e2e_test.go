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
// a BARE session with no @repo_path. When open/recover then calls
// EnsureSession on that pre-existing session, it must re-stamp @repo_path —
// otherwise the session (and any window recover adds to it) is filtered out of
// the M-s picker and appears to have vanished.
func TestEnsureSession_RestampsBareExistingSession(t *testing.T) {
	srv := testtmux.New(t)
	// Simulate the launcher's bare `new-session -A` result: a session named
	// after the workspace but with NO @repo_path stamped.
	srv.NewSession("vyrwu/atelier")
	if v, _ := srv.Client.Run("show-option", "-t", "vyrwu/atelier", "-qv", "@repo_path"); strings.TrimSpace(string(v)) != "" {
		t.Fatalf("precondition: bare session should have no @repo_path, got %q", v)
	}

	// open/recover path: EnsureSession on the existing bare session.
	created, err := workspace.EnsureSession(srv.Client, "vyrwu/atelier", "/Users/me/code/github/vyrwu/atelier", "main")
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if created {
		t.Errorf("created=true, want false (session already existed)")
	}

	// The heal: @repo_path is now stamped, so M-s will include this session.
	got, _ := srv.Client.Run("show-option", "-t", "vyrwu/atelier", "-qv", "@repo_path")
	if strings.TrimSpace(string(got)) != "/Users/me/code/github/vyrwu/atelier" {
		t.Errorf("@repo_path not restamped on existing bare session: %q", got)
	}
}
