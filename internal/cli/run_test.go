package cli

import (
	"os"
	"strings"
	"testing"
)

// TestWriteBundledConfig_EmitsAtelierTmuxSocketEnv locks in the
// load-bearing invariant: the bundled launcher's generated tmux
// config MUST emit `set-environment -g ATELIER_TMUX_SOCKET <socket>`
// near the top so every `run-shell` child (restore, stamp-statusline,
// stamp-last-seen, status emitters) routes back to the bundled
// server via -L.
//
// THE BUG THIS GUARDS AGAINST:
// Without that env var, run-shell -b children don't reliably inherit
// TMUX (tmux backgrounds detach from client context). So
// `atelier state restore` invoked from inside the bundled config
// runs `tmux new-session` against the USER'S DEFAULT tmux socket
// instead of the atelier socket. Restore fails with "server exited
// unexpectedly", silently writes errors to debug.log, and the user
// sees zero workspaces restored on next launch.
//
// HISTORY: this exact bug shipped because no test exercised the
// bundled launcher's child-env propagation. Every other restore
// test passes the socket explicitly (via --socket flag or
// tmuxhost.New(srv.Socket)) — bypassing the env-var routing the
// bundled launcher relies on. This test closes that gap.
func TestWriteBundledConfig_EmitsAtelierTmuxSocketEnv(t *testing.T) {
	// Use a temp HOME so writeBundledConfig writes its output to a
	// directory we own; assert on the file contents.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "") // force fallback to HOME/.cache

	const socket = "atelier-test-routing"
	confPath, err := writeBundledConfig(socket)
	if err != nil {
		t.Fatalf("writeBundledConfig: %v", err)
	}
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read %s: %v", confPath, err)
	}
	conf := string(data)

	wantLine := `set-environment -g ATELIER_TMUX_SOCKET "` + socket + `"`
	if !strings.Contains(conf, wantLine) {
		t.Errorf("bundled config missing socket-routing env line.\n"+
			"want substring: %q\nfull conf:\n%s", wantLine, conf)
	}

	// The env line must appear BEFORE the first run-shell command.
	// run-shell -b children only see env vars that were set with
	// set-environment -g BEFORE the run-shell call.
	envIdx := strings.Index(conf, "ATELIER_TMUX_SOCKET")
	runShellIdx := strings.Index(conf, "run-shell")
	if envIdx < 0 || runShellIdx < 0 {
		t.Fatalf("could not locate env/run-shell positions in conf")
	}
	if envIdx > runShellIdx {
		t.Errorf("ATELIER_TMUX_SOCKET env line (offset %d) must come BEFORE "+
			"the first run-shell (offset %d); otherwise run-shell children "+
			"spawned during config-sourcing won't see the var.",
			envIdx, runShellIdx)
	}
}
