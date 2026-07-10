package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// resetRegistry clears the package-global built-in registry so each test
// starts from a known-empty state. White-box: this test is in package
// plugin, so it can touch the unexported globals directly.
func resetRegistry(t *testing.T) {
	t.Helper()
	regMu.Lock()
	builtins = nil
	regByName = map[string]bool{}
	regMu.Unlock()
}

// isolateConfig points config.Path() at an empty temp dir so tests never
// read the developer's real ~/.config/atelier/config.toml, then writes
// the given config.toml body (empty body = no file).
func isolateConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if body != "" {
		cfgDir := filepath.Join(dir, "atelier")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
}

func noop(*cobra.Command) {}

func TestRegisterBuiltin_AndDiscover(t *testing.T) {
	resetRegistry(t)
	isolateConfig(t, "")
	RegisterBuiltin(&manifest.Manifest{Name: "foo", Description: "the foo"}, noop)

	res, err := Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(res.Plugins))
	}
	p := res.Plugins[0]
	if p.Name != "foo" || !p.IsBuiltin() {
		t.Fatalf("expected built-in foo, got %+v (builtin=%v)", p, p.IsBuiltin())
	}
}

func TestRegisterBuiltin_IdempotentByName(t *testing.T) {
	resetRegistry(t)
	isolateConfig(t, "")
	RegisterBuiltin(&manifest.Manifest{Name: "foo", Description: "first"}, noop)
	RegisterBuiltin(&manifest.Manifest{Name: "foo", Description: "second"}, noop)

	res, _ := Discover()
	if len(res.Plugins) != 1 {
		t.Fatalf("expected 1 plugin after double register, got %d", len(res.Plugins))
	}
	if res.Plugins[0].Manifest.Description != "first" {
		t.Fatalf("first registration should win, got %q", res.Plugins[0].Manifest.Description)
	}
}

func TestDiscover_SortsByName(t *testing.T) {
	resetRegistry(t)
	isolateConfig(t, "")
	RegisterBuiltin(&manifest.Manifest{Name: "foo"}, noop)
	RegisterBuiltin(&manifest.Manifest{Name: "bar"}, noop)
	RegisterBuiltin(&manifest.Manifest{Name: "baz"}, noop)

	res, _ := Discover()
	got := []string{res.Plugins[0].Name, res.Plugins[1].Name, res.Plugins[2].Name}
	if got[0] != "bar" || got[1] != "baz" || got[2] != "foo" {
		t.Fatalf("plugins not sorted: %v", got)
	}
}

func TestDiscover_LoadsLauncherFromConfig(t *testing.T) {
	resetRegistry(t)
	isolateConfig(t, `
[tools.k9s-aws]
launch = "aws-vault-k9s"
popup = "global"
key = "K"
requires = ["aws-vault-k9s"]
title = "K9s (AWS)"
description = "k9s with AWS auth"
`)
	res, err := Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	p, ok := res.FindByName("k9s-aws")
	if !ok {
		t.Fatalf("launcher k9s-aws not discovered: %+v", res)
	}
	if p.IsBuiltin() {
		t.Fatalf("launcher should not be a built-in")
	}
	if p.Manifest.Popup != manifest.KindGlobal {
		t.Fatalf("popup: got %q want global", p.Manifest.Popup)
	}
	if p.Manifest.Binding == nil || p.Manifest.Binding.Key != "K" {
		t.Fatalf("binding key not synthesized: %+v", p.Manifest.Binding)
	}
	if len(p.Manifest.Requires) != 1 || p.Manifest.Requires[0] != "aws-vault-k9s" {
		t.Fatalf("requires not carried through: %+v", p.Manifest.Requires)
	}
}

func TestDiscover_LauncherShadowingBuiltinSkipped(t *testing.T) {
	resetRegistry(t)
	RegisterBuiltin(&manifest.Manifest{Name: "claude", Description: "built-in"}, noop)
	isolateConfig(t, `
[tools.claude]
launch = "some-impostor"
`)
	res, _ := Discover()
	p, _ := res.FindByName("claude")
	if p == nil || !p.IsBuiltin() {
		t.Fatalf("built-in claude must win; got %+v", p)
	}
	if _, ok := res.Skipped["[tools.claude]"]; !ok {
		t.Fatalf("expected shadowing launcher to be skipped; skipped=%v", res.Skipped)
	}
}

func TestDiscover_LauncherMissingLaunchSkipped(t *testing.T) {
	resetRegistry(t)
	isolateConfig(t, `
[tools.bad]
popup = "none"
`)
	res, _ := Discover()
	if _, ok := res.FindByName("bad"); ok {
		t.Fatalf("launcher with no `launch` should be skipped, not registered")
	}
	if _, ok := res.Skipped["[tools.bad]"]; !ok {
		t.Fatalf("expected [tools.bad] in skipped; got %v", res.Skipped)
	}
}

func TestCheckRequirements(t *testing.T) {
	r := &DiscoveryResult{
		Plugins: []Plugin{
			{Name: "needy", Manifest: &manifest.Manifest{
				Name: "needy", Requires: []string{"this-binary-does-not-exist-xyz"}}},
			{Name: "fine", Manifest: &manifest.Manifest{Name: "fine"}},
		},
	}
	missing := r.CheckRequirements()
	if _, ok := missing["needy"]; !ok {
		t.Fatalf("expected needy to report missing requirement; got %v", missing)
	}
	if _, ok := missing["fine"]; ok {
		t.Fatalf("fine should have no missing requirements")
	}
}

func TestFindByName(t *testing.T) {
	r := &DiscoveryResult{
		Plugins: []Plugin{
			{Name: "foo", Manifest: &manifest.Manifest{Name: "foo"}},
			{Name: "bar", Manifest: &manifest.Manifest{Name: "bar"}},
		},
	}
	if _, ok := r.FindByName("foo"); !ok {
		t.Fatalf("FindByName foo: not found")
	}
	if _, ok := r.FindByName("nope"); ok {
		t.Fatalf("FindByName nope: should be missing")
	}
}
