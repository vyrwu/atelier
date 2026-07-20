//go:build e2e

package workspaces_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestDottedRepo_CreatesAndDisplaysWithDot is the end-to-end regression guard
// for "creating a workspace for cloudnativedenmark.dk breaks". It exercises
// both fixes at once:
//
//   - creation: tmux mangles '.'→'_' in session names, so the creator must
//     normalize the derived name (workspace.SessionName) or every -t target
//     fails and the window is never made. We assert the feat window exists
//     under the mangled session tmux actually stored.
//   - display: the M-s picker must show the real repo name (with the dot),
//     recovered from @repo_path — not the mangled "cloudnativedenmark_dk".
func TestDottedRepo_CreatesAndDisplaysWithDot(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	_ = srv.Attach(t, "main")
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "cloudnativedenmark", "cloudnativedenmark.dk", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"cloudnativedenmark/cloudnativedenmark.dk", repoDir, "main", "feat-logos"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Creation fix: the window must exist under the name tmux actually stored
	// (dot mangled to underscore). Pre-fix, every -t target carried the raw
	// ".dk" and this window was never created.
	wid, err := srv.Client.DisplayMessageAt("cloudnativedenmark/cloudnativedenmark_dk:feat-logos", "#{window_id}")
	if err != nil || strings.TrimSpace(wid) == "" {
		t.Fatalf("feat window not created under mangled session: wid=%q err=%v", wid, err)
	}

	// Display fix: the picker row's rendered name keeps the dot. The mangled
	// name ("…_dk") still appears in field 1 (the switch target); the dotted
	// form appears ONLY via the Display field, so its presence proves the fix.
	out, err := srv.RunAtelier("tools", "workspaces", "_session-list")
	if err != nil {
		t.Fatalf("_session-list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "cloudnativedenmark/cloudnativedenmark.dk") {
		t.Errorf("picker display dropped the dot; want a rendered name containing "+
			"\"cloudnativedenmark/cloudnativedenmark.dk\":\n%s", out)
	}
}
