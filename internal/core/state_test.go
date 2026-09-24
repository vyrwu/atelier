package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// NFR-R4: the UI and the hook CLI both write state, so Update must serialise the
// whole read-modify-write. Without the lock around it, concurrent appends read
// the same base state and overwrite each other — the classic lost update.
func TestUpdateSerialisesConcurrentWrites(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const n = 16

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Update(func(s *State) {
				s.Workspaces = append(s.Workspaces, Workspace{Slug: fmt.Sprintf("w%02d", i)})
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	st := Load()
	if len(st.Workspaces) != n {
		t.Fatalf("stored %d workspaces after %d concurrent appends — updates were lost", len(st.Workspaces), n)
	}
	seen := map[string]bool{}
	for _, w := range st.Workspaces {
		if seen[w.Slug] {
			t.Fatalf("duplicate slug %q", w.Slug)
		}
		seen[w.Slug] = true
	}
}

// Load must always hand back a usable state: atelier has to start even when the
// file is missing, corrupt, or written by an incompatible version.
func TestLoadUnreadableYieldsEmptyState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	if st := Load(); st == nil || len(st.Workspaces) != 0 || st.Version != stateVersion {
		t.Fatalf("missing file: %+v", st)
	}

	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"version": 1, "workspaces": [`,                                // truncated
		`{"version": 999, "workspaces": [{"slug": "from-the-future"}]}`, // newer shape
		`{"version": 0, "workspaces": [{"slug": "from-the-past"}]}`,     // older shape
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if st := Load(); len(st.Workspaces) != 0 || st.Version != stateVersion {
			t.Errorf("state %q loaded as %+v, want an empty current-version state", body, st)
		}
	}
}

// The list is stored newest-first, which is the order every view renders.
func TestSaveSortsNewestFirst(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base := time.Now()
	for i, d := range []time.Duration{-2 * time.Hour, -1 * time.Hour, 0} {
		if err := putWorkspace(Workspace{Slug: fmt.Sprintf("w%d", i), Created: base.Add(d)}); err != nil {
			t.Fatal(err)
		}
	}
	got := Load().Workspaces
	if len(got) != 3 || got[0].Slug != "w2" || got[2].Slug != "w0" {
		t.Fatalf("order = %v, want newest first", slugsOf(got))
	}
}

// putWorkspace (the tests' way in) replaces by slug rather than appending a
// second record — the slug is the identity everything else is named for.
func TestPutWorkspaceReplacesBySlug(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := putWorkspace(Workspace{Slug: "dup", Title: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := putWorkspace(Workspace{Slug: "dup", Title: "second"}); err != nil {
		t.Fatal(err)
	}
	st := Load()
	if len(st.Workspaces) != 1 || st.Workspaces[0].Title != "second" {
		t.Fatalf("got %+v, want one workspace titled \"second\"", st.Workspaces)
	}
}

func TestRemoveWorkspace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, slug := range []string{"a", "b", "c"} {
		if err := putWorkspace(Workspace{Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveWorkspace("b"); err != nil {
		t.Fatal(err)
	}
	if got := slugsOf(Load().Workspaces); len(got) != 2 || got[0] == "b" || got[1] == "b" {
		t.Fatalf("after remove: %v", got)
	}
	if err := RemoveWorkspace("nope"); err != nil { // removing what isn't there is not an error
		t.Fatal(err)
	}
	if len(Load().Workspaces) != 2 {
		t.Fatal("removing an absent slug changed the state")
	}
}

// Find and FindBySession must return a pointer INTO the state slice: callers
// mutate through it inside Update and expect the change to be saved.
func TestFindReturnsMutablePointer(t *testing.T) {
	s := &State{Workspaces: []Workspace{
		{Slug: "a", Session: "sess-a"},
		{Slug: "b", Session: "sess-b"},
	}}

	if got := s.Find("missing"); got != nil {
		t.Errorf("Find(missing) = %+v, want nil", got)
	}
	if got := s.FindBySession("missing"); got != nil {
		t.Errorf("FindBySession(missing) = %+v, want nil", got)
	}

	s.Find("a").Title = "titled"
	if s.Workspaces[0].Title != "titled" {
		t.Error("Find returned a copy — mutations through it are lost")
	}
	s.FindBySession("sess-b").Retired = true
	if !s.Workspaces[1].Retired {
		t.Error("FindBySession returned a copy — mutations through it are lost")
	}
}

// Retired is the zero value, so state written before the Trash view existed
// loads as active with no migration.
func TestRetiredIsZeroValue(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"version": 1, "workspaces": [{"slug": "legacy", "session": "legacy"}]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	w := Load().Find("legacy")
	if w == nil || w.Retired {
		t.Fatalf("legacy workspace = %+v, want active", w)
	}
}

func slugsOf(ws []Workspace) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Slug
	}
	return out
}

// A state file that exists but can't be trusted — corrupt, or from another
// version — must survive a write. Update used to build on Load's empty
// fallback and save it, replacing every space with nothing.
func TestUpdateRefusesToOverwriteAnUnreadableState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"version": 1, "workspaces": [{"slug": "a"`,             // truncated
		`{"version": 2, "workspaces": [{"slug": "from-newer"}]}`, // a newer atelier's
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := Update(func(s *State) { called = true }); err == nil {
			t.Errorf("%q: Update succeeded on a state it could not read", body)
		}
		if called {
			t.Errorf("%q: Update ran its change against an empty stand-in", body)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Errorf("%q: file was rewritten to %q", body, got)
		}
	}
	// No file yet is the ordinary first run, and fine to write.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := putWorkspace(Workspace{Slug: "first"}); err != nil {
		t.Errorf("first write with no state file: %v", err)
	}
}

// putWorkspace inserts or replaces a workspace by slug — test setup through the
// same Update path everything else writes by.
func putWorkspace(w Workspace) error {
	return Update(func(s *State) {
		for i := range s.Workspaces {
			if s.Workspaces[i].Slug == w.Slug {
				s.Workspaces[i] = w
				return
			}
		}
		s.Workspaces = append(s.Workspaces, w)
	})
}
