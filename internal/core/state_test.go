package core

import (
	"fmt"
	"strings"
	"testing"
)

// TestAddUniqueWorkspace_Collision: when gen keeps returning a taken name, the
// second workspace must still get a unique slug (the random-suffix fallback).
func TestAddUniqueWorkspace_Collision(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dup := func() string { return "dup" }

	w1, err := AddUniqueWorkspace(Workspace{Title: "a"}, dup)
	if err != nil {
		t.Fatal(err)
	}
	if w1.Slug != "dup" {
		t.Fatalf("first slug = %q, want %q", w1.Slug, "dup")
	}
	if w1.Session != w1.Slug {
		t.Fatalf("session %q != slug %q", w1.Session, w1.Slug)
	}

	w2, err := AddUniqueWorkspace(Workspace{Title: "b"}, dup)
	if err != nil {
		t.Fatal(err)
	}
	if w2.Slug == w1.Slug {
		t.Fatalf("collision not resolved: both got %q", w1.Slug)
	}
	if !strings.HasPrefix(w2.Slug, "dup-") {
		t.Fatalf("fallback slug = %q, want dup-<id>", w2.Slug)
	}

	if st := Load(); len(st.Workspaces) != 2 || st.Workspaces[0].Slug == st.Workspaces[1].Slug {
		t.Fatalf("stored workspaces not both-present-and-unique: %+v", st.Workspaces)
	}
}

// TestAddUniqueWorkspace_Sequential: a gen that never repeats yields all-unique
// slugs across many creates.
func TestAddUniqueWorkspace_Sequential(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	i := 0
	gen := func() string { i++; return fmt.Sprintf("n%d", i) }

	seen := map[string]bool{}
	for k := 0; k < 5; k++ {
		w, err := AddUniqueWorkspace(Workspace{}, gen)
		if err != nil {
			t.Fatal(err)
		}
		if seen[w.Slug] {
			t.Fatalf("duplicate slug %q on create %d", w.Slug, k)
		}
		seen[w.Slug] = true
	}
}
