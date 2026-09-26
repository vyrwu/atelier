package ui

import (
	"strings"
	"testing"
)

func splashText(m splashModel) string {
	m.w, m.h = 100, 44
	return ansiRe.ReplaceAllString(m.View(), "")
}

// The splash is the only place the keymap, the version, and the dependency
// check are written down — there is no separate Help screen to fall back on.
func TestSplashCarriesTheKeymapAndDoctor(t *testing.T) {
	Version = "1.0.0"
	out := splashText(splashModel{
		count: 3,
		deps: []dep{
			{name: "git", ok: true, version: "2.51"},
			{name: "claude", ok: false},
			{name: "diffnav", ok: true, optional: true},
		},
	})

	if !strings.Contains(out, "1.0.0") {
		t.Error("splash does not show the version")
	}
	for _, g := range splashKeys {
		for _, kv := range g.keys {
			if !strings.Contains(out, kv[0]) {
				t.Errorf("key %q missing from the splash", kv[0])
			}
			if !strings.Contains(out, kv[1]) {
				t.Errorf("label %q missing from the splash", kv[1])
			}
		}
	}
	if !strings.Contains(out, "git") || !strings.Contains(out, "2.51") {
		t.Error("present dependency not shown with its version")
	}
	if !strings.Contains(out, "not found") {
		t.Error("missing dependency not called out")
	}
	if !strings.Contains(out, "diffnav") {
		t.Error("optional viewer not shown")
	}
	if !strings.Contains(out, "3 workspaces") {
		t.Errorf("workspace count missing: %s", out)
	}
}

// M-h is what brings you back here, so it has to be on the legend.
func TestSplashDocumentsHome(t *testing.T) {
	found := false
	for _, g := range splashKeys {
		for _, kv := range g.keys {
			if kv[0] == "M-h" && kv[1] == "home" {
				found = true
			}
		}
	}
	if !found {
		t.Error("splash legend does not document M-h as home")
	}
}

func TestSplashCountIsSingularForOne(t *testing.T) {
	if out := splashText(splashModel{count: 1}); !strings.Contains(out, "1 workspace  ·") {
		t.Error("one workspace should not be pluralised")
	}
	if out := splashText(splashModel{count: 0}); !strings.Contains(out, "0 workspaces") {
		t.Error("zero workspaces should be pluralised")
	}
}

// A short terminal drops the wordmark before it drops the keymap — the top of
// the splash is never cut off.
func TestSplashKeepsTheKeymapOnAShortTerminal(t *testing.T) {
	m := splashModel{count: 2, deps: []dep{{name: "git", ok: true, version: "2.51"}}}
	for _, h := range []int{8, 12, 16, 44} {
		m.w, m.h = 80, h
		out := ansiRe.ReplaceAllString(m.View(), "")
		if n := strings.Count(out, "\n") + 1; n > h {
			t.Errorf("h=%d: rendered %d lines", h, n)
		}
		for _, g := range splashKeys {
			for _, kv := range g.keys {
				if !strings.Contains(out, kv[0]+"  "+kv[1]) {
					t.Errorf("h=%d: %s %s cut off", h, kv[0], kv[1])
				}
			}
		}
	}
}

func TestMajorMinor(t *testing.T) {
	for in, want := range map[string]string{"2.50.1": "2.50", "3.5": "3.5", "2.1.123": "2.1", "": ""} {
		if got := majorMinor(in); got != want {
			t.Errorf("majorMinor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathVersion(t *testing.T) {
	for _, c := range []struct{ name, path, want string }{
		{"diffnav", "/nix/store/g4zp4xyjr6bh7h7xl56xa31k15ddjf89-diffnav-0.11.0/bin/diffnav", "0.11.0"},
		{"gh-enhance", "/nix/store/j3ds34hmhxx69j4am8ng3awdd9gajirf-gh-enhance-0.6.1/bin/gh-enhance", "0.6.1"},
		{"diffnav", "/opt/homebrew/Cellar/diffnav/0.11.0/bin/diffnav", "0.11.0"},
		{"diffnav", "/Users/me/go1.26.1/bin/diffnav", ""},
		{"diffnav", "/usr/local/bin/diffnav", ""},
	} {
		if got := pathVersion(c.name, c.path); got != c.want {
			t.Errorf("pathVersion(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
