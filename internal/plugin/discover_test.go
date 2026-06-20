package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/manifest"
)

// writeFakeTool creates an executable shell script at dir/atelier-<name> that
// echoes manifestJSON when called with manifest.Sentinel.
func writeFakeTool(t *testing.T, dir, name, manifestJSON string) {
	t.Helper()
	path := filepath.Join(dir, Prefix+name)
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "%s" ]; then
  cat <<'EOF'
%s
EOF
  exit 0
fi
echo "unknown invocation: $@" >&2
exit 1
`, manifest.Sentinel, manifestJSON)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDiscover_FindsValidTool(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "foo", `{"api_version":1,"name":"foo","description":"test tool"}`)

	res, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d: %+v", len(res.Plugins), res)
	}
	p := res.Plugins[0]
	if p.Name != "foo" || p.Manifest.Description != "test tool" {
		t.Fatalf("unexpected plugin: %+v", p)
	}
}

func TestDiscover_SkipsInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "bad", `{"name":"bad"}`) // missing api_version

	res, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 0 {
		t.Fatalf("expected 0 plugins, got %d", len(res.Plugins))
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %d", len(res.Skipped))
	}
}

func TestDiscover_HandlesMultipleTools(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "foo", `{"api_version":1,"name":"foo"}`)
	writeFakeTool(t, dir, "bar", `{"api_version":1,"name":"bar"}`)
	writeFakeTool(t, dir, "baz", `{"api_version":1,"name":"baz"}`)

	res, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 3 {
		t.Fatalf("expected 3 plugins, got %d", len(res.Plugins))
	}
	// Verify sorted by name
	names := []string{res.Plugins[0].Name, res.Plugins[1].Name, res.Plugins[2].Name}
	if names[0] != "bar" || names[1] != "baz" || names[2] != "foo" {
		t.Fatalf("plugins not sorted: %v", names)
	}
}

func TestDiscover_IgnoresNonPrefixed(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "foo", `{"api_version":1,"name":"foo"}`)
	if err := os.WriteFile(filepath.Join(dir, "other-tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write other-tool: %v", err)
	}
	res, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 1 || res.Plugins[0].Name != "foo" {
		t.Fatalf("expected only foo, got %+v", res.Plugins)
	}
}

func TestDiscover_FirstDirWins(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()
	writeFakeTool(t, d1, "foo", `{"api_version":1,"name":"foo","description":"first"}`)
	writeFakeTool(t, d2, "foo", `{"api_version":1,"name":"foo","description":"second"}`)

	res, err := Discover(d1, d2)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(res.Plugins))
	}
	if got := res.Plugins[0].Manifest.Description; got != "first" {
		t.Fatalf("expected first PATH entry to win, got %q", got)
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
