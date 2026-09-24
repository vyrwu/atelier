package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Every atelier file resolves through these. Getting one wrong writes to a path
// nothing reads — silent, not an error — so the XDG overrides are worth pinning.
func TestPaths(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")

	cases := map[string]struct{ got, want string }{
		"state":  {StatePath(), "/xdg/state/atelier/state.json"},
		"config": {ConfigPath(), "/xdg/config/atelier/config.toml"},
		"guide":  {WorkspaceGuidePath(), "/xdg/config/atelier/WORKSPACE_CLAUDE.md"},
		"cache":  {CacheDir(), "/xdg/cache/atelier"},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", name, c.got, c.want)
		}
	}
}

// With no XDG vars set, the paths fall back to the documented defaults under
// $HOME rather than to a relative path.
func TestPathsDefaultToHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got, want := StatePath(), filepath.Join(home, ".local/state", "atelier", "state.json"); got != want {
		t.Errorf("StatePath() = %q, want %q", got, want)
	}
}

// ATELIER_ROOT overrides the default workspace root and honours a leading ~ —
// this is the value LoadConfig pushes the configured root through.
func TestAteliersRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	t.Setenv("ATELIER_ROOT", "")
	if got, want := AteliersRoot(), filepath.Join(home, "ateliers"); got != want {
		t.Errorf("default root = %q, want %q", got, want)
	}

	t.Setenv("ATELIER_ROOT", "/srv/spaces")
	if got := AteliersRoot(); got != "/srv/spaces" {
		t.Errorf("override root = %q, want /srv/spaces", got)
	}

	t.Setenv("ATELIER_ROOT", "~/spaces")
	if got, want := AteliersRoot(), filepath.Join(home, "spaces"); got != want {
		t.Errorf("tilde root = %q, want %q", got, want)
	}

	w := Workspace{Slug: "brave-otter"}
	if got, want := w.Root(), filepath.Join(home, "spaces", "brave-otter"); got != want {
		t.Errorf("workspace root = %q, want %q", got, want)
	}
}

// A config root is applied through $ATELIER_ROOT (expanded), but an explicit env
// var wins — that is how `make dev` runs against an isolated root and socket.
func TestLoadConfigRootAndSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "root = \"~/spaces\"\nsocket = \"from-config\"\nnaming = false\n"
	if err := os.WriteFile(filepath.Join(dir, "atelier", "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ATELIER_ROOT", "")
	t.Setenv("ATELIER_SOCKET", "")
	c := LoadConfig()
	if c.Socket != "from-config" || c.Naming {
		t.Errorf("config not applied: %+v", c)
	}
	home, _ := os.UserHomeDir()
	if got, want := os.Getenv("ATELIER_ROOT"), filepath.Join(home, "spaces"); got != want {
		t.Errorf("ATELIER_ROOT = %q, want the expanded %q", got, want)
	}

	t.Setenv("ATELIER_SOCKET", "from-env")
	if c := LoadConfig(); c.Socket != "from-env" {
		t.Errorf("socket = %q, want the env var to win", c.Socket)
	}
}

// A fresh install has no config file at all and must still come up with working
// defaults (NFR-S3).
func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ATELIER_ROOT", "")
	t.Setenv("ATELIER_SOCKET", "")
	c := LoadConfig()
	if c.Socket != "atelier" || !c.Naming || c.NamingModel != "sonnet" {
		t.Errorf("defaults = %+v", c)
	}
}

func TestFriendlyNameShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		n := FriendlyName()
		parts := splitOnce(n, '-')
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			t.Fatalf("FriendlyName() = %q, want adjective-noun", n)
		}
		seen[n] = true
	}
	if len(seen) < 20 {
		t.Errorf("only %d distinct names in 200 draws — not random enough to avoid constant collisions", len(seen))
	}
}

func splitOnce(s string, sep byte) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}
