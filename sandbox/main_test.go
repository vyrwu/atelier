package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/seed"
)

// TestLaunchCmd_BundledUsesSandboxBinary guards the LookPath footgun: bundled
// mode must exec the freshly-built binary from the sandbox BinDir by absolute
// path. A bare "atelier" is resolved by exec.Command via LookPath against the
// launcher's own $PATH, which finds the user's INSTALLED atelier instead of
// the dev build — so `make sandbox` silently ran the installed binary (and its
// old UI) instead of the code under test.
func TestLaunchCmd_BundledUsesSandboxBinary(t *testing.T) {
	layout := &seed.Layout{BinDir: "/tmp/sb/bin", MultiRoot: "/tmp/sb/code"}
	cmd := launchCmd("bundled", "atelier-sandbox", layout, "/tmp/sb")

	want := filepath.Join(layout.BinDir, "atelier")
	if cmd.Path != want {
		t.Fatalf("bundled launch binary = %q, want %q (must be the sandbox build, not a PATH lookup)", cmd.Path, want)
	}
	if len(cmd.Args) == 0 || cmd.Args[0] != want {
		t.Errorf("argv[0] = %v, want %q", cmd.Args, want)
	}
}

// TestSocketPath_MatchesAtelierResolution locks the cold-start socket path to
// atelier's own tmuxSocketPath logic. The sandbox env strips TMUX*, so atelier
// always resolves the socket under /tmp — coldStart MUST remove that exact file
// or a wedged prior server survives and the client lands on `[exited]`.
func TestSocketPath_MatchesAtelierResolution(t *testing.T) {
	uid := os.Getuid()

	// Sandbox env (TMUX_TMPDIR stripped) → /tmp, regardless of the caller.
	got := socketPath("atelier-sandbox", []string{"PATH=/usr/bin", "HOME=/x"})
	want := filepath.Join("/tmp", fmt.Sprintf("tmux-%d", uid), "atelier-sandbox")
	if got != want {
		t.Errorf("stripped env: socketPath = %q, want %q", got, want)
	}

	// If TMUX_TMPDIR IS present in the env, honor it (matches tmux + atelier).
	got = socketPath("atelier-sandbox", []string{"TMUX_TMPDIR=/custom/tmp"})
	want = filepath.Join("/custom/tmp", fmt.Sprintf("tmux-%d", uid), "atelier-sandbox")
	if got != want {
		t.Errorf("TMUX_TMPDIR env: socketPath = %q, want %q", got, want)
	}
}

// TestTmuxEnvSocket parses the socket field out of a $TMUX value — the first
// step of the nested-launch guard. A stray parse here would let the sandbox
// launch nested inside a live atelier and silently hand the user the outer
// (installed) picker instead of the dev build.
func TestTmuxEnvSocket(t *testing.T) {
	cases := map[string]string{
		"":                                      "",
		"/private/tmp/tmux-501/atelier,13764,3": "/private/tmp/tmux-501/atelier",
		"/tmp/tmux-501/atelier-sandbox,1,0":     "/tmp/tmux-501/atelier-sandbox",
	}
	for in, want := range cases {
		if got := tmuxEnvSocket(in); got != want {
			t.Errorf("tmuxEnvSocket(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLaunchCmd_PluginUsesTmux confirms plugin mode drives tmux directly. It
// invokes atelier only from inside the server via the sandbox PATH, so it was
// never affected by the bundled-mode binary-resolution bug.
func TestLaunchCmd_PluginUsesTmux(t *testing.T) {
	layout := &seed.Layout{BinDir: "/tmp/sb/bin", MultiRoot: "/tmp/sb/code"}
	cmd := launchCmd("plugin", "atelier-sandbox-plugin", layout, t.TempDir())

	if filepath.Base(cmd.Path) != "tmux" {
		t.Fatalf("plugin launch binary = %q, want tmux", cmd.Path)
	}
}
