//go:build e2e

package cli_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDispatcher_ListsDiscoveredTools runs the freshly-built `atelier`
// binary and verifies that `atelier tools list` reports the built-in tools
// registered in the in-process registry (internal/tools/all).
func TestDispatcher_ListsDiscoveredTools(t *testing.T) {
	binDir := filepath.Join(repoRoot(), "bin")
	core := filepath.Join(binDir, "atelier")
	if _, err := exec.Command(core, "version").Output(); err != nil {
		t.Skipf("core not built (run `make build` first): %v", err)
	}

	cmd := exec.Command(core, "tools", "list")
	cmd.Env = append(cmd.Env, "PATH="+binDir, "HOME="+t.TempDir())
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("atelier tools list: %v\nstderr: %s", err, errBuf.String())
	}
	for _, want := range []string{"k8s", "pg", "aws", "workspaces", "toolselector"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing tool %q in output:\n%s", want, out.String())
		}
	}
}

func TestInitAggregator_EmitsAllToolBlocks(t *testing.T) {
	binDir := filepath.Join(repoRoot(), "bin")
	core := filepath.Join(binDir, "atelier")
	if _, err := exec.Command(core, "version").Output(); err != nil {
		t.Skipf("core not built (run `make build` first): %v", err)
	}

	cmd := exec.Command(core, "init")
	cmd.Env = append(cmd.Env, "PATH="+binDir, "HOME="+t.TempDir())
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("atelier init: %v\nstderr: %s", err, errBuf.String())
	}
	// After the binding-simplification, only switchers and tools with
	// in-popup hotkeys emit bindings. Tools reached only via the selector
	// (popupshell, lazygit, claude, aws) declare no bindings.
	for _, want := range []string{
		"# --- toolselector ---",
		"# --- workspaces ---",
		"# --- hooks ---",
		"# --- statusline ---",
		"atelier popup cleanup",
		// Statusline stamping: idempotent, no append-on-every-source.
		"atelier internal stamp-statusline",
		`bind -T root "M-;"`,
		`bind -T root "M-n"`,
		`bind -T root "M-a"`,
		// M-q quit (core binding, no plugin). FR-5.3: detach, not kill,
		// so background popup agents survive across user sessions.
		`bind -T root  "M-q" detach-client`,
		`bind -T popup "M-q" run-shell -b 'atelier server quit'`,
		// Popup-table bindings now use inline `display-popup` so the
		// new popup nests on the current popup-client (the prior
		// `run-shell -b 'atelier popup goto-tool ...'` form lost the
		// popup-client context and disturbed underlying tool ptys).
		`bind -T popup "M-;" display-popup`,
		`bind -T popup "M-n" display-popup`,
		`bind -T popup "M-a" display-popup`,
		// Persistence block emitted at the end so prior tmux state
		// is restored on every server start. No session-closed /
		// window-unlinked hooks: tmux exit is the normal case that
		// must preserve state, not a wipe signal.
		`run-shell 'atelier state restore'`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("init missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"bind -T root p ",
		"bind -T root g ",
		"bind -T root c ",
		"bind -T root 9 ",
		"bind -T root a ",
		"bind -T root C-s ",
		"bind -T popup \"C-s\"",
		// k8s now emits a binding block (M-c → switch context), so
		// it's no longer in the "no bindings" set.
		"# --- popupshell ---",
		"# --- lazygit ---",
		"# --- claude ---",
		"# --- aws ---",
	} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("init unexpectedly contains stale binding %q", unwanted)
		}
	}
}

func TestDispatcher_RoutesToCorrectBinary(t *testing.T) {
	binDir := filepath.Join(repoRoot(), "bin")
	core := filepath.Join(binDir, "atelier")
	if _, err := exec.Command(core, "version").Output(); err != nil {
		t.Skipf("core not built (run `make build` first): %v", err)
	}
	// `atelier tools pg --help` dispatches in-process to the pg tool's
	// command tree and surfaces its subcommands.
	cmd := exec.Command(core, "tools", "pg", "--help")
	cmd.Env = append(cmd.Env, "PATH="+binDir, "HOME="+t.TempDir())
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("dispatcher: %v\nstderr: %s", err, errBuf.String())
	}
	combined := out.String() + errBuf.String()
	if !strings.Contains(combined, "switch") || !strings.Contains(combined, "contexts") {
		t.Fatalf("expected dispatcher to surface pg subcommands, got:\n%s", combined)
	}
}

func TestDispatcher_UnknownTool_Errors(t *testing.T) {
	binDir := filepath.Join(repoRoot(), "bin")
	core := filepath.Join(binDir, "atelier")
	if _, err := exec.Command(core, "version").Output(); err != nil {
		t.Skipf("core not built (run `make build` first): %v", err)
	}
	cmd := exec.Command(core, "tools", "definitely-not-a-real-tool")
	cmd.Env = append(cmd.Env, "PATH="+binDir, "HOME="+t.TempDir())
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected error for unknown tool, got nil")
	}
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
