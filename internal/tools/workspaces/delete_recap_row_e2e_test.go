//go:build e2e

package workspaces_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestSessionPicker_DeleteRowWithRecap_ActuallyFires drives the REAL
// bubbletea M-s picker end-to-end: it launches `tools workspaces sessions`
// on a recap-laden row and asserts the workspace is actually gone after
// M-x → confirm → Enter.
//
// History: this was the regression guard for the fzf "can't delete / can't
// even enter" wedge, where a recap laced with ANSI + `+`/`(`/`)`/`;`
// corrupted fzf's re-parse of the `execute-silent(_delete-row {})` action.
// The bubbletea picker deletes in-process (deleteRow) with no shell action
// round-trip, so that whole bug class is gone by construction — but a
// recap-laden row driven through the real picker is still the truest
// end-to-end delete test, so it stays.
//
// The picker is launched in a normal window (not the M-s display-popup):
// display-popup overlays aren't capturable via capture-pane, so a plain
// window is a faithful and deterministic surface.
func TestSessionPicker_DeleteRowWithRecap_ActuallyFires(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	_ = srv.Attach(t, "main") // a client must be attached for the window to size
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	// Real worktree + window for a non-default branch. Sole window in the
	// session (the creator kills the auto-created default-branch window),
	// so a successful delete kills the whole session.
	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo", repoDir, "main", "feat-doomed"); err != nil {
		t.Fatalf("create wt: %v", err)
	}
	srv.MustHaveSession("vyrwu/demo")

	// The trigger: a recap laced with the punctuation that broke the fzf
	// action re-parse. Set as a window option, exactly as restore does.
	const recap = "PR #514 gated (all CI pass), verified config: staging + euw1 EU only; awaiting merge"
	if _, err := srv.Client.Run("set-option", "-w", "-t", "vyrwu/demo:feat-doomed",
		workspace.OptRecap, recap); err != nil {
		t.Fatalf("stamp recap: %v", err)
	}

	// Launch the real picker in a normal window. A new-window in the
	// pre-existing "main" session inherits that session's (stale) env, so
	// bare `atelier` in the picker's BINDS would resolve to whatever is on
	// the developer's PATH — not the freshly-built test binary. Prefix with
	// `env PATH=<BinDir>:...` so the picker AND its bind children (the
	// delete transform runs `atelier tools workspaces _delete-prompt` /
	// `_delete-row` via the shell) all resolve the binary under test.
	launch := fmt.Sprintf("env PATH=%s ATELIER_TMUX_SOCKET=%s ATELIER_CODE_ROOT=%s HOME=%s XDG_CACHE_HOME=%s %s tools workspaces sessions",
		srv.BinDir()+string(os.PathListSeparator)+os.Getenv("PATH"),
		srv.Socket, testtmux.CodeRoot(tmp), tmp, os.Getenv("XDG_CACHE_HOME"), srv.Binary())
	if _, err := srv.Client.Run("new-window", "-t", "main", "-n", "picker", "-c", tmp, launch); err != nil {
		t.Fatalf("launch picker: %v", err)
	}
	const pane = "main:picker"
	sendKeys := func(args ...string) {
		if _, err := srv.Client.Run(append([]string{"send-keys", "-t", pane}, args...)...); err != nil {
			t.Fatalf("send-keys %v: %v", args, err)
		}
	}
	waitForPane := func(sub string, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		var last string
		for time.Now().Before(deadline) {
			out, _ := srv.Client.Run("capture-pane", "-p", "-t", pane)
			if last = string(out); strings.Contains(last, sub) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("waitForPane(%q) timed out; last pane:\n%s", sub, last)
	}

	// The doomed workspace is the sole row, so it's already selected:
	// M-x → confirm prompt → Enter commits the delete.
	waitForPane("feat-doomed", 5*time.Second)
	time.Sleep(200 * time.Millisecond)
	sendKeys("M-x")
	waitForPane("(y/n)", 3*time.Second)
	sendKeys("Enter")

	testtmux.Eventually(t, 5*time.Second, func() error {
		if has, _ := srv.Client.HasSession("vyrwu/demo"); has {
			return fmt.Errorf("workspace 'vyrwu/demo' still present — delete did not fire through the picker")
		}
		return nil
	})
}
