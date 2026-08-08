package workspaces

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRunGitCtx_DeadlineSurfacesAsTimeout guards the whole point of the git
// timeout: an expired context makes runGitCtx return a clear "timed out" error
// (so the caller stamps a pull-error) rather than a raw git failure or a hang.
func TestRunGitCtx_DeadlineSurfacesAsTimeout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	// Already-past deadline: the git op cannot outrun it, so ctx.Err() is
	// DeadlineExceeded when runGitCtx inspects it.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()

	err := runGitCtx(ctx, t.TempDir(), "fetch", "origin", "main")
	if err == nil {
		t.Fatal("expected an error from an expired-context git op")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got %v", err)
	}
}
