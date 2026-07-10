package cli

import (
	"os"
	"strings"
	"testing"
)

// fakeSessions is a sessionChecker with a fixed set of live sessions.
type fakeSessions map[string]bool

func (f fakeSessions) HasSession(name string) (bool, error) { return f[name], nil }

// TestLaunchTargetForAlive guards bug 2 (the stray "zsh" window). On an alive
// server, relaunching onto a workspace that has NO live session must NOT
// bare-create it (which leaves an unstamped shell in M-s) — it lands on the
// neutral fallback instead. A live workspace is attached directly; the neutral
// fallback is always honored.
func TestLaunchTargetForAlive(t *testing.T) {
	live := fakeSessions{"vyrwu/atelier": true, "default": true}
	cases := []struct {
		name, resolved, fallback, want string
	}{
		{"live workspace → attach it", "vyrwu/atelier", "default", "vyrwu/atelier"},
		{"dead workspace → neutral fallback (no bare-create)", "vyrwu/aws-athena", "default", "default"},
		{"resolved IS the fallback → keep it", "default", "default", "default"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := launchTargetForAlive(live, c.resolved, c.fallback); got != c.want {
				t.Errorf("launchTargetForAlive(%q,%q) = %q, want %q", c.resolved, c.fallback, got, c.want)
			}
		})
	}
}

// TestInsideAtelierServer guards the nested-launch fix: bare `atelier` run
// from inside the atelier runtime must be detected (so it refuses to nest and
// collapse the session), while a plain shell or the user's OWN tmux must NOT
// trip it (launching atelier from the user's tmux is the primary-entry case).
func TestInsideAtelierServer(t *testing.T) {
	cases := []struct {
		name, tmux, socket string
		want               bool
	}{
		{"not in tmux", "", "atelier", false},
		{"inside atelier", "/tmp/tmux-501/atelier,1234,4", "atelier", true},
		{"inside atelier via /private symlink", "/private/tmp/tmux-501/atelier,1234,4", "atelier", true},
		{"user's own default tmux", "/tmp/tmux-501/default,1234,0", "atelier", false},
		{"custom socket match", "/tmp/tmux-501/work,9,1", "work", true},
		{"different socket", "/tmp/tmux-501/atelier,1234,4", "work", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TMUX", c.tmux)
			if c.tmux == "" {
				_ = os.Unsetenv("TMUX")
			}
			if got := insideAtelierServer(c.socket); got != c.want {
				t.Errorf("insideAtelierServer(%q) with TMUX=%q = %v, want %v", c.socket, c.tmux, got, c.want)
			}
		})
	}
}

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
