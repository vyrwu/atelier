package operator

import (
	"context"
	"errors"
	"github.com/vyrwu/atelier/internal/core"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfirmWith(t *testing.T) {
	calls := 0
	ask := func(client, prompt string, timeout time.Duration) (bool, error) {
		calls++
		if client != "c1" || prompt != "delete?" || timeout != confirmTimeout {
			t.Fatalf("%s %s %s", client, prompt, timeout)
		}
		return true, nil
	}
	if err := confirmWith("delete?", func() ([]string, error) { return nil, nil }, ask); !errors.Is(err, ErrAway) || calls != 0 {
		t.Fatal(err, calls)
	}
	if err := confirmWith("delete?", func() ([]string, error) { return []string{"c1", "c2"}, nil }, ask); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if err := confirmWith("delete?", func() ([]string, error) { return []string{"c1"}, nil }, func(string, string, time.Duration) (bool, error) { return false, nil }); !errors.Is(err, ErrDeclined) {
		t.Fatal(err)
	}
	boom := errors.New("clients failed")
	if err := confirmWith("delete?", func() ([]string, error) { return nil, boom }, ask); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

func TestDeletePrompt(t *testing.T) {
	w := core.Workspace{Slug: "spry-otter", Title: "Fix auth"}
	if got := deletePrompt(w, []string{"repo/fix: 2 uncommitted"}); got != `Delete "Fix auth" (spry-otter)? Unsaved: repo/fix: 2 uncommitted` {
		t.Fatal(got)
	}
}

func TestDeleteRefusesWithoutYes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ATELIER_ROOT", t.TempDir())
	if err := core.Update(func(s *core.State) { s.Workspaces = []core.Workspace{{Slug: "keep", Session: "keep"}} }); err != nil {
		t.Fatal(err)
	}
	if err := Delete(context.Background(), "keep", func(string) error { return ErrDeclined }); !errors.Is(err, ErrDeclined) {
		t.Fatal(err)
	}
	if core.Load().Find("keep") == nil {
		t.Fatal("deleted refused space")
	}
	if err := Delete(context.Background(), core.MaitreSlug, func(string) error { return nil }); err == nil {
		t.Fatal("Maître accepted")
	}
}

func TestDeleteCancellationWinsOverLateConfirmation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ATELIER_ROOT", t.TempDir())
	w := core.Workspace{Slug: "keep", Session: "keep"}
	if err := os.MkdirAll(w.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(w.Root(), "work.txt")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := core.Update(func(s *core.State) { s.Workspaces = []core.Workspace{w} }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := Delete(ctx, w.Slug, func(string) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if core.Load().Find(w.Slug) == nil {
		t.Fatal("cancelled deletion removed state")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatal("cancelled deletion removed work", err)
	}
}
