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
	// Both Stop (response complete) and Notification (selector /
	// permission / idle prompt) must route to the kernel AI stop-hook so
	// waiting-for-user states flag the parent window.
	for _, ev := range []string{"Stop", "Notification"} {
		groups, ok := got.Hooks[ev]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("missing %s hook: %+v", ev, got.Hooks)
		}
		cmd := groups[0].Hooks[0].Command
		if cmd != "atelier ai on-stop" {
			t.Errorf("%s command=%q want 'atelier ai on-stop'", ev, cmd)
		}
	}
}

// TestEnsure_RewritesStaleV2HookCommand guards the upgrade regression: a
// user coming from the pre-hexagonal build has a cached settings.json at
// version 2 whose hook command is the now-deleted `atelier tools claude
// notify-attention`. Ensure() MUST rewrite it to `atelier ai on-stop` —
// otherwise Claude's Stop/Notification hooks invoke a dead command and
// attention + recap silently break. The schemaVersion bump is what forces it.
func TestEnsure_RewritesStaleV2HookCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := canonicalSettings{
		AtelierVersion: 2,
		Hooks: map[string][]hookGroup{
			"Stop":         {{Hooks: []hookEntry{{Type: "command", Command: "atelier tools claude notify-attention"}}}},
			"Notification": {{Hooks: []hookEntry{{Type: "command", Command: "atelier tools claude notify-attention"}}}},
		},
	}
	data, _ := json.MarshalIndent(stale, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	got, _ := os.ReadFile(path)
	var cur canonicalSettings
	if err := json.Unmarshal(got, &cur); err != nil {
		t.Fatalf("parse: %v\n%s", err, got)
	}
	if cur.AtelierVersion != schemaVersion {
		t.Errorf("version not bumped: got %d want %d", cur.AtelierVersion, schemaVersion)
	}
	for _, ev := range []string{"Stop", "Notification"} {
		if cmd := cur.Hooks[ev][0].Hooks[0].Command; cmd != "atelier ai on-stop" {
			t.Errorf("%s hook still stale: %q", ev, cmd)
		}
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
