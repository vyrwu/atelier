//go:build e2e

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// prebuildBinaries forces testtmux's lazy build to run with the
// REAL $HOME so go's module cache lands in the user's normal
// GOPATH. We then redirect $HOME for the tmux server's env in the
// test body — without this, the go build invoked downstream
// would populate the tempdir $HOME with $HOME/go/pkg/mod (read-only
// by design), and t.TempDir's RemoveAll cleanup fails on those
// files with "permission denied," failing the test even when its
// assertions passed.
func prebuildBinaries(t *testing.T, srv *testtmux.Server) {
	t.Helper()
	if _, err := srv.RunAtelier("version"); err != nil {
		t.Fatalf("pre-build atelier binary: %v", err)
	}
}

// pollFor waits up to `timeout` for `check` to return true.
// Used to ride out async tmux ops like run-shell -b 'atelier
// internal stamp-statusline' that complete shortly after the
// outer source-file returns. Production startup races on this
// briefly too (the statusline segments appear within ~tens of
// ms of tmux being ready); the test just needs a reliable trigger.
func pollFor(t *testing.T, timeout time.Duration, check func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestLocalOverride_SourcedAndWins is the runtime proof that the
// `~/.config/atelier/tmux.conf.local` override mechanism actually
// works — not just that the if-shell line is in the generated
// config (the unit test in initgen covers that), but that tmux
// actually evaluates the if-shell, finds the file, sources it,
// and the override settings take precedence over atelier's
// defaults.
//
// Without an end-to-end test, a quoting bug in the if-shell
// (single-vs-double quotes, missing brackets, wrong path
// expansion) would slip through. The unit test would still pass
// because the literal string is correct; tmux's interpretation
// is what's actually load-bearing.
func TestLocalOverride_SourcedAndWins(t *testing.T) {
	srv := testtmux.New(t)
	prebuildBinaries(t, srv)

	// Redirect HOME so tmux's `~` expansion finds our test override
	// file. Done AFTER pre-building binaries so go's module cache
	// stays in the real GOPATH (see prebuildBinaries comment).
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Drop a local override that sets a sentinel value we can
	// distinguish from atelier's default. status-left is convenient:
	// atelier sets it to " #S "; we set it to a distinctive literal
	// that can't be confused with anything else in the config.
	localDir := filepath.Join(tmpHome, ".config", "atelier")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	localPath := filepath.Join(localDir, "tmux.conf.local")
	const sentinel = " OVERRIDE-SENTINEL "
	if err := os.WriteFile(localPath,
		[]byte("set -g status-left '"+sentinel+"'\n"), 0o644); err != nil {
		t.Fatalf("write local: %v", err)
	}

	srv.NewSession("seed")

	// Generate the atelier bundled config and source it.
	out, err := srv.RunAtelier("init")
	if err != nil {
		t.Fatalf("atelier init: %v\n%s", err, out)
	}
	confPath := filepath.Join(t.TempDir(), "atelier.conf")
	if err := os.WriteFile(confPath, out, 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	sourceOut, sourceErr := exec.Command("tmux", "-L", srv.Socket,
		"source-file", confPath).CombinedOutput()
	if sourceErr != nil {
		t.Fatalf("source-file: %v\n%s", sourceErr, sourceOut)
	}

	// Verify the override took effect AT RUNTIME by querying tmux.
	got, err := srv.Client.Run("show-options", "-gv", "status-left")
	if err != nil {
		t.Fatalf("show-options status-left: %v", err)
	}
	gotTrim := strings.TrimRight(string(got), "\n")
	if gotTrim != sentinel {
		t.Errorf("status-left = %q, want %q — override file was NOT sourced or did not win.\nlocal file: %s\nsourced conf: %s",
			gotTrim, sentinel, localPath, confPath)
	}
}

// TestLocalOverride_AbsentIsSilentNoOp locks the no-customization
// path: a fresh user with no `~/.config/atelier/tmux.conf.local`
// gets atelier's defaults with ZERO errors from the if-shell
// probe. If if-shell's exit non-zero leaks through (or the
// source-file path is wrong), users see a startup error on every
// `atelier` launch — the kind of friction the distribution mode
// exists to prevent.
func TestLocalOverride_AbsentIsSilentNoOp(t *testing.T) {
	srv := testtmux.New(t)
	prebuildBinaries(t, srv)

	// HOME with no .config/atelier dir at all. Set AFTER pre-build
	// to avoid the go-modcache-in-tempdir cleanup race.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	srv.NewSession("seed")

	out, err := srv.RunAtelier("init")
	if err != nil {
		t.Fatalf("atelier init: %v\n%s", err, out)
	}
	confPath := filepath.Join(t.TempDir(), "atelier.conf")
	if err := os.WriteFile(confPath, out, 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	sourceOut, sourceErr := exec.Command("tmux", "-L", srv.Socket,
		"source-file", confPath).CombinedOutput()
	if sourceErr != nil {
		t.Fatalf("source-file errored without local file: %v\n%s", sourceErr, sourceOut)
	}
	combined := string(sourceOut)
	// Specifically check for if-shell-related failures.
	for _, badPhrase := range []string{
		"No such file",
		"source-file:",
		"can't open",
		"if-shell:",
	} {
		if strings.Contains(combined, badPhrase) {
			t.Errorf("missing-file probe produced tmux error containing %q:\n%s",
				badPhrase, combined)
		}
	}

	// Atelier's default status-left must still be in place since
	// no override file existed to replace it.
	got, err := srv.Client.Run("show-options", "-gv", "status-left")
	if err != nil {
		t.Fatalf("show-options status-left: %v", err)
	}
	gotTrim := strings.TrimRight(string(got), "\n")
	if !strings.Contains(gotTrim, "#S") {
		t.Errorf("atelier default status-left lost without an override file: got %q",
			gotTrim)
	}
}

// TestLocalOverride_PreservesAtelierStatuslineSegments is the
// integration test for the subtle interaction between user
// overrides and atelier's stamp-statusline injection. When a user
// sets a custom window-status-current-format in their override
// file, stamp-statusline (which runs in StatuslineBlock, emitted
// AFTER ThemeBlock that sources the override) must re-inject
// atelier's freshness + attention segments into the user's
// custom format.
//
// Locks the contract: user overrides win on style, atelier
// segments are always present regardless of user format choice.
func TestLocalOverride_PreservesAtelierStatuslineSegments(t *testing.T) {
	srv := testtmux.New(t)
	prebuildBinaries(t, srv)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	localDir := filepath.Join(tmpHome, ".config", "atelier")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// User sets a totally custom window-status-current-format. It
	// must NOT include atelier segments; stamp-statusline is
	// supposed to add them.
	if err := os.WriteFile(filepath.Join(localDir, "tmux.conf.local"),
		[]byte("set -g window-status-current-format '#[bold]CUSTOM:#W#[nobold]'\n"),
		0o644); err != nil {
		t.Fatalf("write local: %v", err)
	}

	srv.NewSession("seed")

	out, err := srv.RunAtelier("init")
	if err != nil {
		t.Fatalf("atelier init: %v\n%s", err, out)
	}
	confPath := filepath.Join(t.TempDir(), "atelier.conf")
	if err := os.WriteFile(confPath, out, 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	sourceOut, sourceErr := exec.Command("tmux", "-L", srv.Socket,
		"source-file", confPath).CombinedOutput()
	if sourceErr != nil {
		t.Fatalf("source-file: %v\n%s", sourceErr, sourceOut)
	}

	// stamp-statusline runs via `run-shell -b` (background) from
	// StatuslineBlock, so injection completes shortly AFTER source-file
	// returns. Poll until segments appear or timeout.
	var gotStr string
	ok := pollFor(t, 5*time.Second, func() bool {
		out, err := srv.Client.Run("show-options", "-gv", "window-status-current-format")
		if err != nil {
			return false
		}
		gotStr = strings.TrimRight(string(out), "\n")
		return strings.Contains(gotStr, "atelier status freshness") &&
			strings.Contains(gotStr, "atelier status attention")
	})
	if !ok {
		t.Errorf("timed out waiting for stamp-statusline to inject atelier segments; final value: %q", gotStr)
	}
	// User's custom layout must survive the re-injection.
	if !strings.Contains(gotStr, "CUSTOM:") {
		t.Errorf("user's custom window-status-current-format was overwritten by atelier: got %q",
			gotStr)
	}
}
