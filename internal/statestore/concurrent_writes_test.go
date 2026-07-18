package statestore

import (
	"sync"
	"testing"
)

// TestConcurrentWrites_DoNotClobberEachOther locks in the cross-
// process race fix: N goroutines each Load+mutate+Save the cache,
// each touching DIFFERENT fields. Without flock, the second writer
// clobbers the first's mutations (which is exactly how a detached
// _bg-pull's window write was getting lost when RegisterCreatedWorkspace
// ran concurrently).
//
// We use goroutines (intra-process) as a proxy — they share the
// same file just like separate processes do, but the test is
// cheaper. The withWriteLock flock works on a process basis but
// each goroutine acquires/releases its own fd; the test exercises
// the lock semantics.
//
// Without flock, this test fails reliably: about half the time
// one of the mutations is missing from the final state.
func TestConcurrentWrites_DoNotClobberEachOther(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Seed cache with one workspace + window.
	if err := Save(&State{
		Workspaces: []Workspace{{
			SessionName: "ws/test",
			RepoPath:    "/tmp/fake",
			Kind:        "default-branch",
			Windows:     []Window{{Name: "main"}},
		}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Three concurrent writers, each touching a DIFFERENT field. After
	// all complete, ALL three mutations must be visible in the final
	// state. The pre-flock code lost fields due to the load-then-save race.
	var wg sync.WaitGroup
	wg.Add(3)

	// Writer 1: sets the window's CreatedTs (mirrors StampCreatedTs's
	// write-through in RegisterCreatedWorkspace).
	go func() {
		defer wg.Done()
		if err := UpdateWindow("ws/test", "main", func(w *Window) {
			w.CreatedTs = 1700000000
		}); err != nil {
			t.Errorf("writer 1: %v", err)
		}
	}()

	// Writer 2: sets LastActiveSession (simulates stamp-last-active).
	go func() {
		defer wg.Done()
		if err := SetLastActiveSession("ws/test"); err != nil {
			t.Errorf("writer 2: %v", err)
		}
	}()

	// Writer 3: sets RepoPath again (simulates RegisterCreatedWorkspace).
	go func() {
		defer wg.Done()
		if err := UpdateWorkspace("ws/test", func(ws *Workspace) {
			ws.RepoPath = "/tmp/fake-updated"
		}); err != nil {
			t.Errorf("writer 3: %v", err)
		}
	}()
	wg.Wait()

	// All three mutations must land. Pre-flock, at least one
	// would be silently lost on every other run.
	state, err := Load()
	if err != nil || state == nil {
		t.Fatalf("load: %v %v", state, err)
	}
	if state.LastActiveSession != "ws/test" {
		t.Errorf("LastActiveSession = %q, want %q (writer 2 clobbered)",
			state.LastActiveSession, "ws/test")
	}
	if len(state.Workspaces) != 1 {
		t.Fatalf("len(Workspaces) = %d, want 1: %+v", len(state.Workspaces), state.Workspaces)
	}
	ws := state.Workspaces[0]
	if w := state.FindWindow("ws/test", "main"); w == nil || w.CreatedTs != 1700000000 {
		t.Errorf("window CreatedTs = %+v, want 1700000000 (writer 1 clobbered)", w)
	}
	if ws.RepoPath != "/tmp/fake-updated" {
		t.Errorf("RepoPath = %q, want %q (writer 3 clobbered)",
			ws.RepoPath, "/tmp/fake-updated")
	}
}

// TestConcurrentWrites_RemoveRenameDoNotClobberMetadata is the
// regression lock for the ai.prompt loss: RemoveWindow and
// RenameWindow used to do an UNLOCKED load→mutate→save. When they ran
// concurrently with a locked UpdateWindow (as RegisterCreatedWorkspace
// does when persisting a fresh window's ai.prompt/ai.workspace_kind),
// the unlocked writer's stale-read save clobbered the metadata — the
// window kept its keys but lost their values (map[ai.prompt: ...]).
//
// We loop many rounds because the clobber is timing-dependent: one
// round only fails ~half the time, but across 40 rounds the pre-fix
// code loses the metadata essentially every run. With both mutators
// under withWriteLock, it never does.
func TestConcurrentWrites_RemoveRenameDoNotClobberMetadata(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const rounds = 40
	for r := 0; r < rounds; r++ {
		// Fresh state each round: the metadata target plus two decoy
		// windows the unlocked writers churn (a rename source and a
		// removal victim), all doing a full-state read-modify-write.
		if err := Save(&State{
			Workspaces: []Workspace{{
				SessionName: "ws/test",
				RepoPath:    "/tmp/fake",
				Kind:        "worktree",
				Windows: []Window{
					{Name: "keep"},
					{Name: "old"},
					{Name: "victim"},
				},
			}},
		}); err != nil {
			t.Fatalf("round %d seed: %v", r, err)
		}

		var wg sync.WaitGroup
		wg.Add(3)
		// Locked writer: persist the window's metadata (the value that
		// must survive) — mirrors RegisterCreatedWorkspace.
		go func() {
			defer wg.Done()
			_ = UpdateWindow("ws/test", "keep", func(w *Window) {
				if w.Metadata == nil {
					w.Metadata = map[string]string{}
				}
				w.Metadata["ai.prompt"] = "describe the task"
				w.Metadata["ai.workspace_kind"] = "worktree"
			})
		}()
		// Formerly-unlocked writers doing full-state RMW concurrently.
		go func() { defer wg.Done(); _ = RenameWindow("ws/test", "old", "new") }()
		go func() { defer wg.Done(); _ = RemoveWindow("ws/test", "victim") }()
		wg.Wait()

		state, err := Load()
		if err != nil || state == nil {
			t.Fatalf("round %d load: %v %v", r, state, err)
		}
		w := state.FindWindow("ws/test", "keep")
		if w == nil {
			t.Fatalf("round %d: window 'keep' vanished: %+v", r, state.Workspaces)
		}
		if w.Metadata["ai.prompt"] != "describe the task" ||
			w.Metadata["ai.workspace_kind"] != "worktree" {
			t.Fatalf("round %d: metadata clobbered by unlocked writer: %+v", r, w.Metadata)
		}
	}
}

// TestConcurrentWrites_HighContention exercises the lock under
// real contention: many goroutines all updating the same window's
// CreatedTs. The final value should equal one of the writers' values.
// Without flock, writes interleave and the final value is
// unpredictable (often an old value because Load saw stale data).
func TestConcurrentWrites_HighContention(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if err := Save(&State{
		Workspaces: []Workspace{{
			SessionName: "ws/test",
			RepoPath:    "/tmp/x",
			Windows:     []Window{{Name: "main"}},
		}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			_ = UpdateWindow("ws/test", "main", func(w *Window) {
				w.CreatedTs = int64(1700000000 + i)
			})
		}()
	}
	wg.Wait()

	state, err := Load()
	if err != nil || state == nil || len(state.Workspaces) != 1 {
		t.Fatalf("post-contention state corrupt: %v %v", state, err)
	}
	// Final CreatedTs must be one of the values we wrote — i.e. some
	// writer's. Pre-flock, a stale Load could write back an earlier
	// value, leaving it at something we never wrote.
	w := state.FindWindow("ws/test", "main")
	if w == nil {
		t.Fatalf("window 'main' vanished: %+v", state.Workspaces)
	}
	if w.CreatedTs < 1700000000 || w.CreatedTs > 1700000000+N-1 {
		t.Errorf("CreatedTs = %d, expected one of [1700000000..%d] — lock failed",
			w.CreatedTs, 1700000000+N-1)
	}
}
