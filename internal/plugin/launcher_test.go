package plugin

import (
	"testing"

	"github.com/vyrwu/atelier/internal/manifest"
)

func TestLauncherSpec_toManifest_Defaults(t *testing.T) {
	// Bare spec: only launch set. popup defaults to none, invoke to open,
	// start_cwd derives false (not a workspace popup).
	m, err := LauncherSpec{Launch: "mytool"}.toManifest("mytool")
	if err != nil {
		t.Fatalf("toManifest: %v", err)
	}
	if m.Popup != manifest.KindNone {
		t.Errorf("popup default: got %q want none", m.Popup)
	}
	if m.PrimaryInvoke != "open" || m.Binding.Invoke != "open" {
		t.Errorf("invoke default: got primary=%q binding=%q want open/open", m.PrimaryInvoke, m.Binding.Invoke)
	}
	if m.Binding.StartCwd {
		t.Errorf("start_cwd should default false for popup=none")
	}
	if !m.Tool {
		t.Errorf("launcher manifests must be Tool=true (appear in selector)")
	}
}

func TestLauncherSpec_toManifest_WorkspaceDerivesStartCwd(t *testing.T) {
	m, err := LauncherSpec{Launch: "x", Popup: "workspace"}.toManifest("x")
	if err != nil {
		t.Fatalf("toManifest: %v", err)
	}
	if !m.Binding.StartCwd {
		t.Errorf("popup=workspace should derive start_cwd=true")
	}
}

func TestLauncherSpec_toManifest_StartCwdOverride(t *testing.T) {
	no := false
	m, err := LauncherSpec{Launch: "x", Popup: "workspace", StartCwd: &no}.toManifest("x")
	if err != nil {
		t.Fatalf("toManifest: %v", err)
	}
	if m.Binding.StartCwd {
		t.Errorf("explicit start_cwd=false must override the workspace-derived true")
	}
}

func TestLauncherSpec_toManifest_CarriesFields(t *testing.T) {
	m, err := LauncherSpec{
		Launch: "x", Popup: "global", Key: "K", KeyTable: "root",
		Requires: []string{"x"}, Icon: "胡", AccentColor: "110",
		Title: "X", Description: "the x", Invoke: "run",
	}.toManifest("x")
	if err != nil {
		t.Fatalf("toManifest: %v", err)
	}
	if m.Popup != manifest.KindGlobal || m.Binding.Key != "K" || m.Binding.KeyTable != "root" {
		t.Errorf("fields not carried: popup=%q key=%q table=%q", m.Popup, m.Binding.Key, m.Binding.KeyTable)
	}
	if m.PrimaryInvoke != "run" || m.Binding.Invoke != "run" {
		t.Errorf("invoke override not applied: %q / %q", m.PrimaryInvoke, m.Binding.Invoke)
	}
	if m.UI == nil || m.UI.Icon != "胡" || m.UI.AccentColor != "110" || m.UI.PopupTitle != "X" {
		t.Errorf("UI not synthesized: %+v", m.UI)
	}
	if m.Description != "the x" || len(m.Requires) != 1 {
		t.Errorf("description/requires not carried: %q %v", m.Description, m.Requires)
	}
}

func TestShellExecArgs(t *testing.T) {
	shell, argv := shellExecArgs("printf hi", "/bin/zsh")
	if shell != "/bin/zsh" {
		t.Errorf("shell: got %q want /bin/zsh", shell)
	}
	want := []string{"/bin/zsh", "-c", "printf hi"}
	if len(argv) != 3 || argv[0] != want[0] || argv[1] != want[1] || argv[2] != want[2] {
		t.Errorf("argv: got %v want %v", argv, want)
	}
	// Empty $SHELL falls back to /bin/sh.
	shell, argv = shellExecArgs("echo x", "")
	if shell != "/bin/sh" || argv[0] != "/bin/sh" {
		t.Errorf("empty SHELL fallback: got shell=%q argv0=%q want /bin/sh", shell, argv[0])
	}
}
