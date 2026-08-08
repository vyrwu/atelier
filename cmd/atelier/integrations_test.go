package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig points config.LoadSection at a temp config.toml with body.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "atelier")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// No config → AI defaults to claude, forge off.
func TestComposeIntegrations_Defaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := composeIntegrations()
	if set.AI == nil || set.AI.Name() != "claude" {
		t.Errorf("default AI = %v, want claude", set.AI)
	}
	if set.Forge != nil {
		t.Errorf("default forge = %v, want off", set.Forge)
	}
}

// [ai] provider selects the adapter; [forge] provider selects the forge.
func TestComposeIntegrations_ProviderSelection(t *testing.T) {
	writeConfig(t, "[ai]\nprovider = \"mock\"\n[forge]\nprovider = \"mock\"\n")
	set := composeIntegrations()
	if set.AI == nil || set.AI.Name() != "mock" {
		t.Errorf("AI = %v, want mock", set.AI)
	}
	if set.Forge == nil || set.Forge.Name() != "mock" {
		t.Errorf("forge = %v, want mock", set.Forge)
	}
}

// Empty AI provider disables the capability.
func TestComposeIntegrations_AIDisabled(t *testing.T) {
	writeConfig(t, "[ai]\nprovider = \"\"\n")
	set := composeIntegrations()
	if set.AI != nil {
		t.Errorf("AI = %v, want nil (disabled)", set.AI)
	}
}
