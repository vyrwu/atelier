package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPath_Tilde(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := ExpandPath("~/code")
	want := filepath.Join(home, "code")
	if got != want {
		t.Fatalf("ExpandPath: got %q want %q", got, want)
	}
}

func TestExpandPath_HomeEnv(t *testing.T) {
	got := ExpandPath("$HOME/code")
	if !strings.HasSuffix(got, "/code") {
		t.Fatalf("ExpandPath: $HOME/code should expand to /code suffix, got %q", got)
	}
}

func TestExpandPath_Empty(t *testing.T) {
	if got := ExpandPath(""); got != "" {
		t.Fatalf("ExpandPath of empty should stay empty, got %q", got)
	}
}

func TestExpandPath_Absolute(t *testing.T) {
	if got := ExpandPath("/etc/passwd"); got != "/etc/passwd" {
		t.Fatalf("ExpandPath of absolute path should be unchanged, got %q", got)
	}
}

func TestLoadSection_MissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	type cfg struct {
		Value string `toml:"value"`
	}
	c := cfg{Value: "default"}
	if err := LoadSection("test", &c); err != nil {
		t.Fatalf("LoadSection: %v", err)
	}
	if c.Value != "default" {
		t.Fatalf("expected defaults preserved when file missing, got %q", c.Value)
	}
}

func TestLoadSection_ReadsSection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "atelier", "config.toml")
	if err := os.WriteFile(conf, []byte("[mytool]\nvalue = \"override\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	type cfg struct {
		Value string `toml:"value"`
	}
	c := cfg{Value: "default"}
	if err := LoadSection("mytool", &c); err != nil {
		t.Fatalf("LoadSection: %v", err)
	}
	if c.Value != "override" {
		t.Fatalf("expected override, got %q", c.Value)
	}
}

func TestLoadSection_OtherSectionsIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	_ = os.MkdirAll(filepath.Join(dir, "atelier"), 0o755)
	conf := filepath.Join(dir, "atelier", "config.toml")
	_ = os.WriteFile(conf,
		[]byte("[other]\nignored = \"yes\"\n[mytool]\nvalue = \"x\"\n"), 0o644)

	type cfg struct {
		Value string `toml:"value"`
	}
	c := cfg{}
	if err := LoadSection("mytool", &c); err != nil {
		t.Fatalf("LoadSection: %v", err)
	}
	if c.Value != "x" {
		t.Fatalf("expected mytool.value, got %q", c.Value)
	}
}

func TestLoadSection_MissingSectionPreservesDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	_ = os.MkdirAll(filepath.Join(dir, "atelier"), 0o755)
	conf := filepath.Join(dir, "atelier", "config.toml")
	_ = os.WriteFile(conf, []byte("[other]\nfoo = \"bar\"\n"), 0o644)

	type cfg struct {
		Value string `toml:"value"`
	}
	c := cfg{Value: "default"}
	if err := LoadSection("missing", &c); err != nil {
		t.Fatalf("LoadSection: %v", err)
	}
	if c.Value != "default" {
		t.Fatalf("expected default kept, got %q", c.Value)
	}
}

func TestXDGConfigHome_Override(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
	if got := XDGConfigHome(); got != "/custom/xdg" {
		t.Fatalf("XDGConfigHome: got %q want /custom/xdg", got)
	}
}

func TestXDGCacheHome_Default(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".cache")
	if got := XDGCacheHome(); got != want {
		t.Fatalf("XDGCacheHome: got %q want %q", got, want)
	}
}
