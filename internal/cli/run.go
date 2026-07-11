package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/initgen"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// sessionChecker is the slice of tmuxhost.Client launchTargetForAlive needs —
// broken out so the launch-target decision is unit-testable without a server.
type sessionChecker interface {
	HasSession(name string) (bool, error)
}

// launchTargetForAlive picks which session to attach to when the atelier
// server is ALREADY ALIVE. A live session is attached directly. A dead
// workspace-named session is NOT bare-created: `new-session -A` would leave an
// unstamped "zsh" shell in that workspace — it shows up in M-s and needs a
// manual exit — so we land on the neutral fallback ("default") instead. On a
// FRESH server the RestoreBlock restores the workspace before attach, so this
// path isn't taken (and the resolved workspace is landed on properly).
func launchTargetForAlive(h sessionChecker, resolved, fallback string) string {
	if resolved == fallback {
		return resolved
	}
	if has, _ := h.HasSession(resolved); has {
		return resolved
	}
	return fallback
}

// probeTimeout caps how long we'll wait for the atelier tmux server to
// respond to a liveness probe before declaring it wedged. Set short
// because a healthy server responds in <50ms; anything past 2s is a
// stuck server-state-machine (popup-eating-input, kernel-pty issue,
// crashed worker thread, etc.).
const probeTimeout = 2 * time.Second

// RunCommand is the bundled-distribution launch path. Running
// `atelier` with no subcommand defaults to this: it materializes the
// bundled tmux config (engine + theme), spawns tmux on a dedicated
// socket, and attaches the user's terminal.
//
// Why a dedicated socket: full isolation from any tmux server the
// user may already be running. The atelier server lives at
// `tmux -L atelier ...`; the user's regular `tmux` server is
// untouched. `M-q` detaches; `atelier server kill` (explicit) is the
// only path that tears down the atelier server.
//
// Why `new-session -A`: idempotent reattach. Running `atelier` from
// a second terminal joins the existing session rather than spawning
// a second one. Restoring an interrupted session is the same gesture
// as launching a fresh one.
//
// Why not exec.LookPath("tmux") and exec the process: we want to
// regain control after tmux exits so we can clean up the temp config
// file. `cmd.Run()` blocks until tmux exits; that's the semantics
// we want.
func RunCommand() *cobra.Command {
	var (
		socket  = "atelier"
		session = "default"
	)
	c := &cobra.Command{
		Use:   "run",
		Short: "Launch the bundled atelier tmux runtime",
		Long: `Spawn a dedicated tmux server (-L atelier) configured with atelier's
bundled bindings, hooks, statusline, and theme, then attach this
terminal to it.

This is the default behavior of the bare ` + "`atelier`" + ` command — running
` + "`atelier`" + ` and ` + "`atelier run`" + ` are equivalent. The dedicated socket
isolates atelier from any other tmux server you may already be
running; M-q detaches this client. Use ` + "`atelier server kill`" + ` to
force-quit the server (background agents are terminated).

Idempotent: a second invocation attaches to the existing session
rather than spawning a duplicate.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBundled(cmd, socket, session)
		},
	}
	c.Flags().StringVar(&socket, "socket", "atelier",
		"tmux socket name passed via -L")
	c.Flags().StringVar(&session, "session", "default",
		"initial tmux session name")
	return c
}

func runBundled(cmd *cobra.Command, socket, session string) error {
	launchCwd, _ := os.Getwd()
	debuglog.Logf("runBundled: launch pid=%d socket=%s session(arg)=%s cwd=%s",
		os.Getpid(), socket, session, launchCwd)

	// Refuse to nest. Running `atelier` from INSIDE the atelier runtime would
	// `new-session -A` a second client onto the same session; tmux sizes a
	// session to its smallest client, so a fresh nested client collapses the
	// whole session to its dimensions (observed: a 1-row client froze the
	// outer view). Launching from a DIFFERENT (the user's own) tmux is still
	// supported — that's the primary-entry case — so this only trips when
	// $TMUX points at atelier's own socket.
	if insideAtelierServer(socket) {
		fmt.Fprintln(os.Stderr,
			"atelier: already inside the atelier runtime — not nesting (it collapses the session).\n"+
				"  M-;  pick a tool   M-s  switch workspace   M-n  new workspace   M-q  detach")
		return nil
	}

	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found on PATH: %w", err)
	}

	confPath, err := writeBundledConfig(socket)
	if err != nil {
		return fmt.Errorf("write bundled config: %w", err)
	}
	debuglog.Logf("runBundled: bundled config written to %s", confPath)
	// Clean up the temp config when tmux exits. The temp file lives
	// for the duration of the tmux session — tmux's source-file
	// processes the file at startup, but we keep it around in case
	// of later resource lookups.
	defer func() { _ = os.Remove(confPath) }()

	// Liveness probe + auto-recovery for a wedged prior server.
	//
	// Without this, `tmux ... new-session -A -s default` would hang
	// FOREVER if the prior atelier server got stuck (popup eating
	// input, crashed worker thread, kernel pty bug — all observed in
	// the wild). The new-session call would block trying to attach
	// to a server that never responds, and the user has no way out
	// short of `pkill -9`.
	//
	// Strategy: ping the socket with `has-session` under a 2s deadline.
	// If it doesn't respond, that server is wedged — force-kill it +
	// remove the socket file. Then proceed with a fresh new-session.
	recoverState, err := recoverWedgedServer(tmuxBin, socket)
	debuglog.Logf("runBundled: recoverWedgedServer state=%s err=%v", recoverState, err)
	if err != nil {
		fmt.Fprintf(os.Stderr, "atelier: warning: failed to clean up prior server: %v\n", err)
		// Continue anyway — the new-session attempt below will either
		// succeed (if the recovery was actually fine) or fail with a
		// clearer error than a silent hang.
	}

	// Resume the last-active workspace if one is cached. Restore (run
	// synchronously from inside the bundled config — see RestoreBlock)
	// will have already recreated all cached sessions by the time
	// new-session -A -s <target> runs, so attaching to the resolved
	// last-active name just attaches to the restored session.
	fallback := session // the neutral landing session ("default")
	resolvedSession := resolveLaunchSession(fallback)
	if strings.HasPrefix(recoverState, "alive") {
		// Server already alive: the RestoreBlock won't re-run, so a dead
		// last-active workspace would be bare-created by `new-session -A`
		// below. Only attach to it if it's actually live; else land neutral.
		resolvedSession = launchTargetForAlive(tmuxhost.New(socket), resolvedSession, fallback)
	}
	debuglog.Logf("runBundled: resolved session=%s (arg=%s, recoverState=%s)",
		resolvedSession, fallback, recoverState)
	session = resolvedSession

	tmux := exec.Command(tmuxBin,
		"-L", socket,
		"-f", confPath,
		"new-session", "-A", "-s", session,
	)
	debuglog.Logf("runBundled: launching tmux: %s -L %s -f %s new-session -A -s %s",
		tmuxBin, socket, confPath, session)
	tmux.Stdin = os.Stdin
	tmux.Stdout = os.Stdout
	tmux.Stderr = os.Stderr
	// Strip TMUX / TMUX_PANE / TMUX_PARENT_* from the env. If the user
	// invoked `atelier` from inside another tmux session, these would
	// confuse the new server's children — they'd resolve to the outer
	// tmux's pane instead of the atelier server's. Stripping these is
	// also what makes `atelier` work as the user's PRIMARY entry from
	// any shell, even one already running inside tmux.
	tmux.Env = sanitizedEnv()
	runErr := tmux.Run()
	debuglog.Logf("runBundled: tmux exited err=%v", runErr)
	if runErr != nil {
		// Differentiate "tmux exited cleanly via kill-server" (which
		// returns a non-zero exit on macOS) from real launch failures.
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("tmux: %w", runErr)
	}
	return nil
}

// recoverWedgedServer probes the atelier tmux socket with a short
// deadline. If the server doesn't respond in time, it's wedged —
// force-kill it and remove the dead socket file so the next
// new-session call lands on a clean server.
//
// Returns nil when there's nothing to do (no prior server) or when
// recovery succeeds. Returns an error only when we tried to recover
// but couldn't; callers should warn and continue (the new-session
// below will surface a clearer error than a silent hang).
func recoverWedgedServer(tmuxBin, socket string) (string, error) {
	// Cheap pre-check: if the socket file doesn't exist, no server
	// to probe. Skip the probe entirely.
	sockPath := tmuxSocketPath(socket)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		return "no-prior-socket", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	probe := exec.CommandContext(ctx, tmuxBin, "-L", socket,
		"list-sessions", "-F", "#{session_name}")
	probe.Env = sanitizedEnv()
	probeOut, probeErr := probe.CombinedOutput()
	if probeErr == nil {
		// Server is alive and responsive — leave it alone, new-session
		// -A will attach cleanly.
		trimmed := strings.TrimSpace(string(probeOut))
		live := strings.Count(trimmed, "\n")
		if len(trimmed) > 0 {
			live++
		}
		// Log the live session NAMES (not just the count) so comparing
		// consecutive launches pinpoints exactly which session vanished.
		debuglog.Logf("recoverWedgedServer: alive, %d sessions=[%s]",
			live, strings.ReplaceAll(trimmed, "\n", ","))
		return fmt.Sprintf("alive-attaching(sessions=%d)", live), nil
	}
	// Probe failed (timed out OR returned non-zero). Either way, the
	// server is in a state we can't reuse. Force-kill it.
	fmt.Fprintln(os.Stderr,
		"atelier: prior tmux server on socket \""+socket+
			"\" is unresponsive — cleaning up.")

	killCtx, killCancel := context.WithTimeout(context.Background(), probeTimeout)
	defer killCancel()
	kill := exec.CommandContext(killCtx, tmuxBin, "-L", socket, "kill-server")
	kill.Env = sanitizedEnv()
	_ = kill.Run() // best-effort; we'll nuke the socket file regardless

	// Belt-and-suspenders: remove the socket file even if kill-server
	// "succeeded" but the file is still there (tmux occasionally
	// leaves a stale socket after force-kill).
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		return "wedge-kill-socket-rm-failed", fmt.Errorf("remove stale socket %s: %w", sockPath, err)
	}
	return "wedge-killed", nil
}

// insideAtelierServer reports whether the current process is running inside
// the atelier tmux server itself (as opposed to a plain shell or the user's
// own tmux). tmux exports $TMUX = "<socket-path>,<pid>,<session>"; we compare
// the socket's basename to atelier's socket name. Basename (not full path) is
// deliberate: it's immune to /tmp↔/private/tmp symlinks and TMUX_TMPDIR
// differences that would otherwise defeat a full-path match on macOS.
func insideAtelierServer(socket string) bool {
	tmuxEnv := os.Getenv("TMUX")
	if tmuxEnv == "" {
		return false
	}
	sockField, _, _ := strings.Cut(tmuxEnv, ",")
	return sockField != "" && filepath.Base(sockField) == socket
}

// tmuxSocketPath returns the filesystem path tmux uses for the
// named socket. Matches tmux's logic: $TMUX_TMPDIR (or /tmp) +
// "/tmux-$UID/" + socket-name.
func tmuxSocketPath(socket string) string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)
}

// resolveLaunchSession decides which tmux session the bundled
// launcher should attach to. Reads the persisted LastActiveSession
// from the statestore cache; if present, returns that. Otherwise
// returns the fallback (typically "default"), preserving the
// caller's default behavior.
//
// Extracted from runBundled so it's directly testable without
// having to spawn tmux. Pure function over the statestore.
//
// Resilient to load failures (returns fallback) because a missing
// or corrupt cache should NOT block atelier from launching —
// worst case the user lands on "default" and re-navigates manually.
func resolveLaunchSession(fallback string) string {
	cached, err := statestore.Load()
	if err != nil {
		debuglog.Logf("resolveLaunchSession: load err=%v → fallback=%s", err, fallback)
		return fallback
	}
	if cached == nil {
		debuglog.Logf("resolveLaunchSession: cache empty → fallback=%s", fallback)
		return fallback
	}
	if cached.LastActiveSession == "" {
		debuglog.Logf("resolveLaunchSession: cache has %d workspaces, no LastActiveSession → fallback=%s",
			len(cached.Workspaces), fallback)
		return fallback
	}
	debuglog.Logf("resolveLaunchSession: cache has %d workspaces, LastActiveSession=%q",
		len(cached.Workspaces), cached.LastActiveSession)
	return cached.LastActiveSession
}

// sanitizedEnv returns a copy of the process environment with tmux's
// per-session variables stripped. atelier launches a NEW tmux server
// on a dedicated socket — inheriting TMUX / TMUX_PANE from an outer
// session would have children inside the new server resolve to
// pane/window IDs from the outer one. The new server is its own world.
func sanitizedEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "TMUX="),
			strings.HasPrefix(kv, "TMUX_PANE="),
			strings.HasPrefix(kv, "TMUX_PARENT_"):
			continue
		}
		out = append(out, kv)
	}
	return out
}

// writeBundledConfig generates the engine+theme tmux config for the
// bundled launcher and writes it to a stable per-user path.
//
// Calls initgen.Render — the same primitive used by `atelier init`
// (plugin mode). The only differences between modes are the options
// passed in:
//   - Socket: non-empty in bundled mode (so run-shell children route
//     via -L); empty in plugin mode (TMUX env routes).
//   - IncludeTheme: always true here (bundled launcher is the
//     curated demo path); flag-controlled in plugin mode.
//
// Writes to $XDG_CACHE_HOME/atelier/tmux.conf so reruns don't
// orphan files in /tmp. The launcher rewrites this file on every
// invocation — no stale-config risk.
func writeBundledConfig(socket string) (string, error) {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	cacheDir = filepath.Join(cacheDir, "atelier")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", cacheDir, err)
	}
	confPath := filepath.Join(cacheDir, "tmux.conf")

	var buf bytes.Buffer
	if _, err := initgen.Render(&buf, initgen.RenderOptions{
		Socket:       socket,
		IncludeTheme: true,
		Header: "# Generated by `atelier` -- bundled distribution config\n" +
			"# This file is recreated on every launch.",
	}); err != nil {
		return "", err
	}
	if err := os.WriteFile(confPath, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", confPath, err)
	}
	return confPath, nil
}
