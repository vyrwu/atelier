//go:build e2e

// Package testtmux gives each e2e test its own isolated tmux server.
//
// Each Server uses `tmux -L atelier-test-<random>` so it cannot collide
// with the user's real tmux or with any concurrent test. Cleanup runs in
// t.Cleanup, so the server is killed even if the test panics.
package testtmux

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// silence unused import warning when strings is only used in this file.
var _ = strings.TrimSpace

type Server struct {
	Socket string
	T      *testing.T
	Client *tmuxhost.Client
}

func New(t *testing.T) *Server {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not on PATH: %v", err)
	}
	socket := fmt.Sprintf("atelier-test-%s", randHex(8))

	// Point tmux at a minimal test config file that both:
	//   1. Prevents tmux from sourcing the developer's real
	//      ~/.config/tmux/tmux.conf — which invokes `atelier state
	//      restore` on server start and leaks production workspaces
	//      into the test socket. `-f <file>` overrides tmux's default
	//      config-file search.
	//   2. Sets base-index=1 / pane-base-index=1. Atelier's own target
	//      format assumes 1-indexed windows (e.g. `select-window -t
	//      =session:1` for the default workspace window). Without
	//      this, tests targeting `:1` fail on tmux's stock 0-indexed
	//      defaults. Previously "worked" only because the user config
	//      leak also carried these settings in.
	cfgFile, err := os.CreateTemp("", "atelier-test-tmux-*.conf")
	if err != nil {
		t.Fatalf("create test tmux config: %v", err)
	}
	if _, err := cfgFile.WriteString(
		"set-option -g base-index 1\n" +
			"set-window-option -g pane-base-index 1\n",
	); err != nil {
		_ = cfgFile.Close()
		t.Fatalf("write test tmux config: %v", err)
	}
	_ = cfgFile.Close()
	t.Cleanup(func() { _ = os.Remove(cfgFile.Name()) })

	client := tmuxhost.New(socket)
	client.SetConfigFile(cfgFile.Name())

	srv := &Server{
		Socket: socket,
		T:      t,
		Client: client,
	}
	t.Cleanup(srv.Kill)
	return srv
}

// Kill terminates the test tmux server and waits for it to actually
// exit. tmux's `kill-server` command returns immediately while the
// server processes the shutdown asynchronously — closing sessions,
// firing hooks, flushing buffers. Without the wait, t.Cleanup's
// tempdir RemoveAll races with hook callbacks that may still write
// to $XDG_CACHE_HOME (e.g. stamp-last-active hitting statestore).
//
// We poll for socket-file removal with a short deadline. tmux
// removes the socket file as the last step of server teardown,
// so its disappearance is a reliable "server fully dead" signal.
func (s *Server) Kill() {
	_ = s.Client.KillServer()

	socketPath := tmuxSocketPath(s.Socket)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); os.IsNotExist(err) {
			return // server is gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Timeout: server lingered. Best-effort SIGKILL the socket
	// file too so the next test on the same socket name doesn't
	// reuse it.
	_ = os.Remove(socketPath)
}

// tmuxSocketPath returns the filesystem path tmux uses for the
// named socket — matches tmux's logic: $TMUX_TMPDIR (or /tmp) +
// /tmux-$UID/<socket-name>.
func tmuxSocketPath(socket string) string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)
}

func (s *Server) NewSession(name string) {
	s.T.Helper()
	if err := s.Client.NewSession(name, true); err != nil {
		s.T.Fatalf("NewSession(%q): %v", name, err)
	}
}

func (s *Server) Sessions() []string {
	s.T.Helper()
	out, err := s.Client.ListSessions()
	if err != nil {
		s.T.Fatalf("ListSessions: %v", err)
	}
	return out
}

func (s *Server) MustHaveSession(name string) {
	s.T.Helper()
	has, err := s.Client.HasSession(name)
	if err != nil {
		s.T.Fatalf("HasSession(%q): %v", name, err)
	}
	if !has {
		s.T.Fatalf("expected session %q, got: %v", name, s.Sessions())
	}
}

// SetEnv stamps a server-wide env var via `set-environment -g`. Children
// spawned by tmux (popup -E commands, run-shell, etc.) inherit it.
// Use this to feed ATELIER_CODE_ROOT or other config into the popup chain
// without touching the calling process's environment.
func (s *Server) SetEnv(key, value string) {
	s.T.Helper()
	if _, err := s.Client.Run("set-environment", "-g", key, value); err != nil {
		s.T.Fatalf("set-environment -g %s=%s: %v", key, value, err)
	}
}

// WindowsIn returns the window names (#W) for the given session, in
// index order. Empty slice if the session doesn't exist.
func (s *Server) WindowsIn(session string) []string {
	s.T.Helper()
	has, _ := s.Client.HasSession(session)
	if !has {
		return nil
	}
	out, err := s.Client.Run("list-windows", "-t", "="+session, "-F", "#W")
	if err != nil {
		s.T.Fatalf("list-windows: %v", err)
	}
	var result []string
	for _, line := range splitLines(strings.TrimSpace(string(out))) {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// BinDir returns the test binary directory. The single `atelier` binary is
// built into it once per `go test` invocation and added to PATH for child
// processes via RunAtelier.
func (s *Server) BinDir() string {
	return atelierBinDir(s.T)
}

// Binary returns the path to the core atelier binary in BinDir.
func (s *Server) Binary() string {
	return filepath.Join(s.BinDir(), "atelier")
}

// RunAtelier invokes the single atelier binary with PATH=BinDir.
func (s *Server) RunAtelier(args ...string) ([]byte, error) {
	return s.run(s.Binary(), args)
}

// RunTool dispatches to a tool through the single binary:
// `atelier tools <tool> <args...>`. There are no per-tool binaries — the
// core resolves the tool from its in-process registry.
func (s *Server) RunTool(tool string, args ...string) ([]byte, error) {
	return s.run(s.Binary(), append([]string{"tools", tool}, args...))
}

func (s *Server) run(binary string, args []string) ([]byte, error) {
	cmd := exec.Command(binary, args...)
	// Strip TMUX/atelier env vars inherited from the test process's
	// real shell — they'd leak parent state (e.g. TMUX_PARENT_WINDOW
	// pointing at the developer's actual tmux window) into the
	// isolated test child. Each tool resolves its target from globals
	// or the test socket explicitly.
	filtered := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX") {
			continue
		}
		if strings.HasPrefix(kv, "ATELIER_") {
			// Whitelist test-set gates that tests need to reach the
			// subprocess. Everything else ATELIER_* is stripped to
			// isolate from developer-machine state.
			//
			// Kept whitelist entries:
			//   ATELIER_SYNC_BUILD — makes runWorkspacePrompt run its
			//     build inline instead of deferring via display-popup,
			//     so RunAtelier can assert state deterministically.
			//   ATELIER_TMUX_CONFIG — pins `-f /dev/null` on every
			//     tmux invocation so tmux doesn't source the
			//     developer's ~/.config/tmux/tmux.conf into the
			//     isolated test server (which would restore prod
			//     workspaces onto it via atelier init).
			if strings.HasPrefix(kv, "ATELIER_SYNC_BUILD=") ||
				strings.HasPrefix(kv, "ATELIER_TMUX_CONFIG=") {
				filtered = append(filtered, kv)
			}
			continue
		}
		filtered = append(filtered, kv)
	}
	cmd.Env = append(filtered,
		"PATH="+s.BinDir()+string(os.PathListSeparator)+os.Getenv("PATH"),
		"ATELIER_TMUX_SOCKET="+s.Socket,
		// Match the parent test's tmux-isolation: every tmux call the
		// subprocess makes gets `-f <test-config>` so the developer's
		// real tmux.conf can't restore production state into the test
		// socket, AND base-index=1 stays consistent with the parent.
		"ATELIER_TMUX_CONFIG="+s.Client.ConfigFile(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_ASKPASS=true",
		"SSH_ASKPASS=true",
	)
	return cmd.CombinedOutput()
}

var (
	buildOnce   sync.Once
	builtBinDir string
	buildErr    error
)

// atelierBinDir builds the cmd/atelier binary into a shared temp dir
// once per `go test` invocation and returns its path. (The loop over
// cmd/* now matches exactly one directory — the single binary.)
func atelierBinDir(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), fmt.Sprintf("atelier-test-bin-%s", randHex(8)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			buildErr = err
			return
		}
		cmds, err := os.ReadDir(filepath.Join(repoRoot(), "cmd"))
		if err != nil {
			buildErr = err
			return
		}
		for _, e := range cmds {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "atelier") {
				continue
			}
			out := filepath.Join(dir, name)
			cmd := exec.Command("go", "build", "-o", out, "github.com/vyrwu/atelier/cmd/"+name)
			cmd.Dir = repoRoot()
			if outBytes, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("go build %s: %v\n%s", name, err, outBytes)
				return
			}
		}
		builtBinDir = dir
	})
	if buildErr != nil {
		t.Fatalf("%v", buildErr)
	}
	return builtBinDir
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// repoRoot resolves the atelier repo root from this file's location.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
