package ui

import (
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
