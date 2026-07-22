//go:build e2e

package workspaces_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// requirePickerFzf skips when the installed fzf is too old to parse the
// picker's styling (the `footer:` color key, fzf ≥0.65). Older fzf exits
// before rendering, so the picker can't be driven — the real app is
// equally broken there, which is a separate concern from this bind fix.
// Keeps the render-dependent guard active wherever fzf is modern enough
// (local dev + macOS CI); CI's older Linux fzf skips cleanly.
func requirePickerFzf(t *testing.T) {
	t.Helper()
	cmd := exec.Command("fzf", "--color=footer:103", "-f", "probe")
	cmd.Stdin = strings.NewReader("probe\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if strings.Contains(stderr.String(), "invalid color") {
		t.Skipf("fzf too old to render the picker styling: %s", strings.TrimSpace(stderr.String()))
	}
}

// TestSessionPicker_DeleteRowWithRecap_ActuallyFires is the regression
// guard for the "can't delete / can't even enter a workspace" wedge
// (fix-deploy-drop-offline-mec1-target, feat/remove-livekit-non-eu-envs).
//
// Root cause: the picker binds passed the WHOLE styled `{}` row into a
// transform-emitted `execute-silent(_delete-row {})` action. A row whose
// `@attention_recap` holds free-form AI text (ANSI escapes + an embedded
// recap newline + `+` / `(` / `)` / `;`) corrupted fzf's re-parse of that
// action, so `_delete-row` NEVER fired — and the stuck "Confirm?" prompt
// then also swallowed plain Enter, blocking navigation too.
//
// Every other delete test calls `_delete-row` DIRECTLY, bypassing fzf —
// which is exactly why the bug shipped. This one drives the REAL picker
// command (same binds, same fzf action layer) with a recap-laden row and
// asserts the workspace is actually gone. It fails against the old `{}`
// binds and passes with the `{1} {2}` fix.
//
// The picker is launched in a normal window (not the M-s display-popup):
// display-popup overlays aren't capturable via capture-pane, and the bug
// lives in the shared binds, not the popup chrome — so a plain window is
// a faithful and deterministic surface.
func TestSessionPicker_DeleteRowWithRecap_ActuallyFires(t *testing.T) {
	requirePickerFzf(t)
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

	// Isolate the doomed row (recap is not searchable — --nth=1 = name),
	// then M-x → Confirm? → Enter commits the delete.
	waitForPane("feat-doomed", 5*time.Second)
	sendKeys("-l", "doomed")
	time.Sleep(200 * time.Millisecond)
	sendKeys("M-x")
	waitForPane("Confirm", 3*time.Second)
	sendKeys("Enter")

	testtmux.Eventually(t, 5*time.Second, func() error {
		if has, _ := srv.Client.HasSession("vyrwu/demo"); has {
			return fmt.Errorf("workspace 'vyrwu/demo' still present — delete did not fire through the fzf picker")
		}
		return nil
	})
}
