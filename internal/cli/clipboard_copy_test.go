package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPickClipboardTool_NoneFound: empty PATH → no tool → ok=false.
// The copy-mode yank must SILENTLY no-op in this case rather than
// erroring out, since breaking the user's yank with a cryptic
// "clipboard tool not found" message would be worse than a missing
// clipboard handoff. The OSC 52 set-clipboard path still works in
// most terminals.
func TestPickClipboardTool_NoneFound(t *testing.T) {
	t.Setenv("PATH", "")
	_, _, ok := pickClipboardTool()
	if ok {
		t.Errorf("empty PATH should yield ok=false; the silent-no-op contract is broken if this returns true")
	}
}

// TestPickClipboardTool_Darwin_PrefersPbcopy: on macOS, pbcopy is
// always present and is the universal clipboard handoff. Even if
// other tools happened to be on PATH (wl-copy via a weird port),
// pbcopy must win.
func TestPickClipboardTool_Darwin_PrefersPbcopy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only test")
	}
	dir := t.TempDir()
	makeFakeBinary(t, dir, "pbcopy")
	t.Setenv("PATH", dir)
	tool, args, ok := pickClipboardTool()
	if !ok {
		t.Fatalf("expected ok=true with pbcopy on PATH")
	}
	if filepath.Base(tool) != "pbcopy" {
		t.Errorf("expected pbcopy, got %q", tool)
	}
	if len(args) != 0 {
		t.Errorf("pbcopy takes no extra args, got %v", args)
	}
}

// TestPickClipboardTool_Linux_PrefersWlCopy: under Wayland (the
// modern default on most Linux distros today), wl-copy is the
// right handoff. If wl-copy AND xclip are both present, wl-copy
// wins — important for users on Wayland who happen to have xclip
// installed for legacy reasons.
func TestPickClipboardTool_Linux_PrefersWlCopy(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("linux-priority test")
	}
	dir := t.TempDir()
	makeFakeBinary(t, dir, "wl-copy")
	makeFakeBinary(t, dir, "xclip")
	makeFakeBinary(t, dir, "xsel")
	t.Setenv("PATH", dir)
	tool, _, ok := pickClipboardTool()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if filepath.Base(tool) != "wl-copy" {
		t.Errorf("wl-copy must win over xclip/xsel on Linux; got %q", tool)
	}
}

// TestPickClipboardTool_Linux_FallsBackToXclip: pure-X11 systems
// without wl-copy installed should land on xclip with the
// `-selection clipboard` args (NOT the default primary selection,
// which is a different X11 buffer most users don't paste from).
func TestPickClipboardTool_Linux_FallsBackToXclip(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("linux-only test")
	}
	dir := t.TempDir()
	makeFakeBinary(t, dir, "xclip")
	t.Setenv("PATH", dir)
	tool, args, ok := pickClipboardTool()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if filepath.Base(tool) != "xclip" {
		t.Errorf("expected xclip, got %q", tool)
	}
	want := []string{"-selection", "clipboard"}
	if len(args) != 2 || args[0] != want[0] || args[1] != want[1] {
		t.Errorf("xclip args = %v, want %v (must target clipboard selection, not primary)", args, want)
	}
}

func makeFakeBinary(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}
