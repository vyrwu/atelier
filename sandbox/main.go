// Command sandbox launches a fully isolated, ephemeral atelier for demos
// and manual scenario testing. It hydrates a throwaway temp dir with real
// git repos, worktrees, and seeded workspace state (internal/seed), then
// launches atelier against it on a dedicated tmux socket — with its own
// XDG config/cache, PATH, and git identity. Nothing touches your real
// atelier server, dotfiles, repos, or git config.
//
// On exit (the client detaching or quitting) the sandbox tmux server is
// killed and the temp dir is removed. Pass --keep to leave it on disk.
//
// This binary lives outside cmd/ so it is neither shipped by goreleaser
// nor installed by `make install`; it is a dev/test tool only.
//
// Usage (via the Makefile):
//
//	make sandbox        # bundled launcher   (tmux -L atelier-sandbox)
//	make sandbox-tmux   # plugin / embed way (tmux -L atelier-sandbox-plugin)
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vyrwu/atelier/internal/seed"
)

//go:embed plugin.conf
var pluginConf []byte

const (
	bundledSocket = "atelier-sandbox"
	pluginSocket  = "atelier-sandbox-plugin"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "bundled", "launch mode: bundled | plugin")
	scenarioRef := flag.String("scenario", "acme-platform", "built-in scenario name or path to a scenario YAML file")
	binDir := flag.String("bin-dir", "bin", "dir with the freshly-built atelier binary to expose on the sandbox PATH")
	ai := flag.String("ai", "claude", "AI integration: claude (real agent, needs auth) | mock (offline, no auth)")
	keep := flag.Bool("keep", false, "keep the temp dir + server on exit instead of garbage-collecting")
	allowNested := flag.Bool("allow-nested", false, "launch even when inside an atelier tmux session (M-s/M-; will be captured by the outer session)")
	flag.Parse()

	// Refuse to launch nested inside a LIVE atelier session. atelier binds
	// M-s/M-;/etc in the root key-table, so the OUTER server intercepts those
	// keys before the nested sandbox sees them — you'd drive your live atelier
	// (and its installed binary) while believing you're in the sandbox. Run
	// from a terminal not attached to atelier instead.
	if !*allowNested {
		if sock := atelierParentSocket(); sock != "" {
			fmt.Fprintf(os.Stderr,
				"atelier sandbox: refusing to launch nested inside atelier tmux (%s).\n"+
					"The outer session's M-s/M-; bindings capture those keys before the\n"+
					"sandbox, so you'd drive your LIVE atelier, not this dev build.\n"+
					"Detach your live session first (or use a plain terminal), then rerun.\n"+
					"Override with --allow-nested if you know what you're doing.\n",
				filepath.Base(sock))
			return fmt.Errorf("nested inside atelier tmux (%s); use --allow-nested to override", filepath.Base(sock))
		}
	}

	scenario, err := loadScenario(*scenarioRef)
	if err != nil {
		return err
	}

	socket := bundledSocket
	switch *mode {
	case "bundled":
	case "plugin":
		socket = pluginSocket
	default:
		return fmt.Errorf("unknown mode %q (want bundled|plugin)", *mode)
	}

	root, err := os.MkdirTemp("", "atelier-sandbox-*")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	fmt.Printf("atelier demo sandbox → %s  (mode=%s, socket=%s)\n", root, *mode, socket)

	cleanup := func() {
		if *keep {
			fmt.Printf("\n--keep set; sandbox left at %s (tmux -L %s)\n", root, socket)
			return
		}
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		_ = os.RemoveAll(root)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cleanup()
		os.Exit(130)
	}()
	defer cleanup()

	layout, err := seed.Hydrate(root, scenario, seed.Options{AI: *ai})
	if err != nil {
		return fmt.Errorf("hydrate: %w", err)
	}
	if err := exposeBinary(*binDir, layout.BinDir); err != nil {
		return fmt.Errorf("expose binary: %w", err)
	}
	fmt.Printf("  seeded %d repos, %d workspaces\n", len(scenario.Repos), len(scenario.Workspaces))

	env := layout.Env()
	// Fresh server: kill any prior instance on this socket AND remove its
	// socket file so the launch is a true cold start.
	coldStart(socket, env)

	launch := launchCmd(*mode, socket, layout, root)
	launch.Env = env
	launch.Stdin, launch.Stdout, launch.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Launch from the non-git multi-repo root so atelier doesn't adopt the
	// atelier source tree as a stray workspace.
	launch.Dir = layout.MultiRoot

	fmt.Println("  launching… (M-q detach / C-d quit tears the sandbox down)")
	_ = launch.Run() // non-zero exit on detach/quit is normal; cleanup still runs
	return nil
}

// atelierParentSocket returns the socket path of the tmux server we're nested
// inside IFF that server is an atelier server (has @atelier* globals), else "".
// It's how the sandbox detects the nested-launch footgun where the outer
// atelier eats the picker keybindings.
func atelierParentSocket() string {
	sock := tmuxEnvSocket(os.Getenv("TMUX"))
	if sock == "" {
		return ""
	}
	out, err := exec.Command("tmux", "-S", sock, "show-options", "-g").CombinedOutput()
	if err != nil {
		return ""
	}
	if strings.Contains(string(out), "@atelier") {
		return sock
	}
	return ""
}

// tmuxEnvSocket extracts the socket-path field from a $TMUX value
// ("<socket>,<pid>,<session>"). Returns "" when not inside tmux.
func tmuxEnvSocket(tmuxEnv string) string {
	if tmuxEnv == "" {
		return ""
	}
	sock, _, _ := strings.Cut(tmuxEnv, ",")
	return sock
}

// coldStart guarantees atelier launches onto a clean server. It kills any
// prior instance on socket and removes the socket file, using the SAME
// (TMUX*-stripped) env atelier itself uses — so both agree the socket lives
// under /tmp, not the caller's $TMUX_TMPDIR (set when `make sandbox` runs from
// inside another tmux/atelier session). Without the explicit removal a wedged
// prior server survives kill-server and atelier's own wedged-server recovery
// races the slow-dying process, dropping the client straight to `[exited]`.
// Bounded so a stopped/wedged server can't hang the launch. The sandbox socket
// is dedicated and throwaway, so nuking it unconditionally is safe.
func coldStart(socket string, env []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	kill := exec.CommandContext(ctx, "tmux", "-L", socket, "kill-server")
	kill.Env = env
	_ = kill.Run()
	_ = os.Remove(socketPath(socket, env))
}

// socketPath mirrors atelier's tmuxSocketPath for the given env:
// $TMUX_TMPDIR (or /tmp) + /tmux-$UID/ + socket.
func socketPath(socket string, env []string) string {
	dir := "/tmp"
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "TMUX_TMPDIR="); ok && v != "" {
			dir = v
		}
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)
}

func launchCmd(mode, socket string, layout *seed.Layout, root string) *exec.Cmd {
	if mode == "plugin" {
		conf := filepath.Join(root, "plugin.conf")
		_ = os.WriteFile(conf, pluginConf, 0o644)
		return exec.Command("tmux", "-L", socket, "-f", conf,
			"new-session", "-A", "-s", "default", "-c", layout.MultiRoot)
	}
	// Exec the freshly-built binary by ABSOLUTE path (exposeBinary symlinked
	// it into BinDir). A bare "atelier" here would be resolved by exec.Command
	// via LookPath against the LAUNCHER's $PATH — finding the user's installed
	// atelier, not this dev build — before launch.Env (BinDir-first PATH) ever
	// applies. That silently ran the installed binary as the sandbox driver.
	return exec.Command(filepath.Join(layout.BinDir, "atelier"), "run", "--socket", socket)
}

// exposeBinary symlinks the freshly-built atelier binary into the sandbox
// bin dir so it (and its compiled-in tools) win over any installed copy.
// The single-binary kernel means there's just one binary to expose.
func exposeBinary(srcDir, dstDir string) error {
	absSrc, err := filepath.Abs(filepath.Join(srcDir, "atelier"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(absSrc); err != nil {
		return fmt.Errorf("%s not found (run `make build` first): %w", absSrc, err)
	}
	link := filepath.Join(dstDir, "atelier")
	_ = os.Remove(link)
	return os.Symlink(absSrc, link)
}

// loadScenario reads a scenario by built-in name or from a YAML file path.
func loadScenario(ref string) (*seed.Scenario, error) {
	if fi, err := os.Stat(ref); err == nil && !fi.IsDir() {
		return seed.LoadFile(ref)
	}
	return seed.Builtin(ref)
}
