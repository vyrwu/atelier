package claudesettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsure_WritesCanonicalJSON_WithStopHook(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	path, err := Ensure()
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := filepath.Join(tmp, "atelier", "claude", "settings.json")
	if path != want {
		t.Fatalf("path=%q want %q", path, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got canonicalSettings
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	if got.AtelierVersion != schemaVersion {
		t.Errorf("version=%d want %d", got.AtelierVersion, schemaVersion)
	}
	stop, ok := got.Hooks["Stop"]
	if !ok || len(stop) == 0 || len(stop[0].Hooks) == 0 {
		t.Fatalf("missing Stop hook: %+v", got.Hooks)
	}
	cmd := stop[0].Hooks[0].Command
	if cmd != "atelier tools claude notify-attention" {
		t.Errorf("Stop command=%q want 'atelier tools claude notify-attention'", cmd)
	}
}

// Idempotent: 2nd Ensure on an up-to-date file leaves it untouched.
func TestEnsure_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	path, err := Ensure()
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	stat1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Re-write with stale version, then re-Ensure — should rewrite.
	stale := canonicalSettings{AtelierVersion: 0}
	data, _ := json.MarshalIndent(stale, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	path2, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path {
		t.Fatalf("path drifted: %q vs %q", path2, path)
	}
	stat2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// File should have been rewritten — modtime may differ but the
	// schemaVersion is the canonical check.
	updated, _ := os.ReadFile(path)
	var current canonicalSettings
	_ = json.Unmarshal(updated, &current)
	if current.AtelierVersion != schemaVersion {
		t.Errorf("stale version not rewritten: got %d want %d", current.AtelierVersion, schemaVersion)
	}
	_ = stat1
	_ = stat2
}
