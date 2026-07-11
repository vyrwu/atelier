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
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

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
	flag.Parse()

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

	// Fresh server: kill any prior one on this socket so the launch is a
	// true cold start.
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()

	launch := launchCmd(*mode, socket, layout, root)
	launch.Env = layout.Env()
	launch.Stdin, launch.Stdout, launch.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Launch from the non-git multi-repo root so atelier doesn't adopt the
	// atelier source tree as a stray workspace.
	launch.Dir = layout.MultiRoot

	fmt.Println("  launching… (M-q detach / C-d quit tears the sandbox down)")
	_ = launch.Run() // non-zero exit on detach/quit is normal; cleanup still runs
	return nil
}

func launchCmd(mode, socket string, layout *seed.Layout, root string) *exec.Cmd {
	if mode == "plugin" {
		conf := filepath.Join(root, "plugin.conf")
		_ = os.WriteFile(conf, pluginConf, 0o644)
		return exec.Command("tmux", "-L", socket, "-f", conf,
			"new-session", "-A", "-s", "default", "-c", layout.MultiRoot)
	}
	return exec.Command("atelier", "run", "--socket", socket)
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
