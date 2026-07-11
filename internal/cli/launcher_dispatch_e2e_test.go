//go:build e2e

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestLauncherDispatch_NonePopup_ExecsCommand exercises the WHOLE new
// launcher chain end-to-end against the real built binary:
//
//	atelier tools echo
//	  → ToolsCommand → plugin.Discover() (reads the [tools.echo] block)
//	  → Plugin.Dispatch (launcher branch) → runLauncher (popup=none)
//	  → execInPopup → syscall.Exec sh -c 'printf ...'
//
// This is the routing the unit tests can't cover (Dispatch/runLauncher
// os.Exit/syscall.Exec), so without it the launcher branch would ship
// untested. A regression that mis-routed popup=none, or failed to find
// the config launcher, or dropped the command, fails here.
func TestLauncherDispatch_NonePopup_ExecsCommand(t *testing.T) {
	cfg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[tools.echo]\nlaunch = \"printf atelier-launcher-ok\"\npopup = \"none\"\n"
	if err := os.WriteFile(filepath.Join(cfg, "atelier", "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", cfg)

	srv := testtmux.New(t)
	out, err := srv.RunTool("echo")
	if err != nil {
		t.Fatalf("`atelier tools echo` (launcher) errored: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), "atelier-launcher-ok") {
		t.Fatalf("launcher command output not observed; got:\n%s", out)
	}
}

// TestLauncherDispatch_ListsInToolsList confirms a config launcher shows up
// in `atelier tools list` as a launcher (not a built-in).
func TestLauncherDispatch_ListsInToolsList(t *testing.T) {
	cfg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[tools.mylauncher]\nlaunch = \"true\"\npopup = \"none\"\n"
	if err := os.WriteFile(filepath.Join(cfg, "atelier", "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", cfg)

	srv := testtmux.New(t)
	out, err := srv.RunAtelier("tools", "list")
	if err != nil {
		t.Fatalf("tools list errored: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "mylauncher") || !strings.Contains(s, "launcher") {
		t.Fatalf("launcher not shown as a launcher in tools list; got:\n%s", s)
	}
}
