package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// tmux quantises every colour a program emits down to the 256-palette unless it
// is told the terminal can take 24-bit — which is why a theme can look flatter
// inside atelier than the same tool run outside it. The capability is read from
// the terminal atelier was launched from, never assumed.
func TestRGBSettingFollowsTheTerminal(t *testing.T) {
	cases := map[string]bool{
		"truecolor": true,
		"24bit":     true,
		"TrueColor": true, // case is not the terminal's contract to keep
		"":          false,
		"yes":       false,
	}
	for colorterm, want := range cases {
		t.Setenv("COLORTERM", colorterm)
		got := strings.Contains(rgbSetting(), "RGB")
		if got != want {
			t.Errorf("COLORTERM=%q: advertises RGB = %v, want %v", colorterm, got, want)
		}
	}
	t.Setenv("COLORTERM", "truecolor")
	if s := rgbSetting(); !strings.HasSuffix(s, "\n") {
		t.Errorf("setting must end the line it is spliced into: %q", s)
	}
}

// The fallback title is cut on characters, never inside one.
func TestDeterministicTitleCutsOnRunes(t *testing.T) {
	long := strings.Repeat("zażółć gęślą jaźń ", 6)
	got := deterministicTitle(long)
	if !utf8.ValidString(got) {
		t.Fatalf("title is not valid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 61 {
		t.Errorf("title is %d characters, want at most 60 plus the ellipsis", n)
	}
	if got := deterministicTitle("  short   task  "); got != "short task" {
		t.Errorf("short title = %q", got)
	}
}

// Bindings drift when the binary is rebuilt or upgraded under a running server:
// the old ones call actions the new binary no longer has, and a key silently
// does nothing. refreshBindings, run from the popup and the window keys, puts
// the current ones back — judged by what the server last sourced, not by the
// file, which can be current while the server never loaded it.
func TestRefreshBindingsFollowsTheServer(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("ATELIER_SOCKET", "atelier-test-no-server") // sourcing fails harmlessly
	path := filepath.Join(cfg, "atelier", "atelier.tmux")
	frag, version := bindingsFragment()

	live := "an-older-version"
	orig := liveBindings
	liveBindings = func() string { return live }
	t.Cleanup(func() { liveBindings = orig })

	// The file is already current, but the server has older bindings: write and
	// source anyway.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(frag), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	refreshBindings()
	if fi, _ := os.Stat(path); fi.ModTime().Equal(old) {
		t.Error("server had stale bindings but they were not re-sourced")
	}

	// The server has the current bindings: nothing to do.
	live = version
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	refreshBindings()
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(old) {
		t.Error("bindings rewritten although the server was current")
	}
}

// M-x opens the overlay straight onto the swap confirm for the space it was
// pressed in, capturing the client first like every other popup key.
func TestFragmentBindsSwap(t *testing.T) {
	frag, _ := bindingsFragment()
	for _, line := range strings.Split(frag, "\n") {
		if strings.HasPrefix(line, "bind -n M-x ") {
			if !strings.Contains(line, "@atelier_outer") || !strings.HasSuffix(line, ` open --swap"`) {
				t.Errorf("M-x binding = %q", line)
			}
			return
		}
	}
	t.Error("no M-x binding")
}

// The status line names the window — the agent, or "Command Line" — after the
// title; a long title is cut short so that name is never what gets cut.
func TestStatusLeftKeepsTheWindowName(t *testing.T) {
	frag, _ := bindingsFragment()
	for _, line := range strings.Split(frag, "\n") {
		if !strings.HasPrefix(line, "set -g status-left ") {
			continue
		}
		if !strings.Contains(line, "#{=/50/…:@atelier_title}") {
			t.Errorf("status-left = %q, want the title truncated", line)
		}
		if !strings.Contains(line, "#{@atelier_agent}") || !strings.Contains(line, "Command Line") {
			t.Errorf("status-left = %q, want the agent's name and the shell's", line)
		}
		return
	}
	t.Error("no status-left")
}

// Sourcing the fragment is what records its version in the server.
func TestFragmentRecordsItsVersion(t *testing.T) {
	frag, version := bindingsFragment()
	if !strings.HasSuffix(frag, "set -g @atelier_bindings \""+version+"\"\n") {
		t.Errorf("fragment does not end by recording %q", version)
	}
	if len(version) != 16 {
		t.Errorf("version = %q, want a 16-hex-digit hash", version)
	}
}
