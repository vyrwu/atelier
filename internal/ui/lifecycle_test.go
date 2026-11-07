package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/gc"
)

func TestRestoreUpdatesActivityAndRejectsDeletedSpace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := time.Now().Add(-60 * 24 * time.Hour)
	w := core.Workspace{Slug: "restore", Session: "restore", Created: old, RetiredAt: old, Retired: true}
	if err := core.Update(func(s *core.State) { s.Workspaces = append(s.Workspaces, w) }); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if err := setRetired(w.Slug, false); err != nil {
		t.Fatal(err)
	}
	restored := core.Load().Find(w.Slug)
	if restored == nil || restored.Retired || gc.LastActive(*restored).Before(before) {
		t.Fatalf("restore did not mark activity: %+v", restored)
	}
	if err := core.RemoveWorkspace(w.Slug); err != nil {
		t.Fatal(err)
	}
	if err := setRetired(w.Slug, false); err == nil {
		t.Fatal("restoring a concurrently deleted space succeeded")
	}
}

func TestRestoreKeepsRetiredOnInvalidOrUnwritableStatus(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(core.CacheDir(), "agents", "unwritable"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(core.CacheDir(), "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	for _, session := range []string{"../sentinel", "unwritable"} {
		w := core.Workspace{Slug: "restore", Session: session, Retired: true, RetiredAt: old}
		if err := core.Update(func(s *core.State) { s.Workspaces = []core.Workspace{w} }); err != nil {
			t.Fatal(err)
		}
		if err := setRetired(w.Slug, false); err == nil {
			t.Errorf("restore with session %q succeeded", session)
		}
		if got := core.Load().Find(w.Slug); got == nil || !got.Retired || !got.RetiredAt.Equal(old) {
			t.Errorf("failed restore changed the record: %+v", got)
		}
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("restore overwrote a file outside the status directory: %q, %v", data, err)
	}
}
