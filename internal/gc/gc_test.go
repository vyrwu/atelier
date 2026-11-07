package gc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// sandbox points every path atelier reads at a fresh temp dir, and tmux at a
// socket with no server, so nothing here can reach a real space.
func sandbox(t *testing.T) (root string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "ateliers")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATELIER_ROOT", root)
	t.Setenv("ATELIER_SOCKET", "atelier-gc-test-"+filepath.Base(base))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(base, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(base, "codex"))
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
	} {
		t.Setenv(k, v)
	}
	return root
}

func put(t *testing.T, ws ...core.Workspace) {
	t.Helper()
	if err := core.Update(func(s *core.State) {
		for _, w := range ws {
			if w.Session == "" {
				w.Session = w.Slug
			}
			s.Workspaces = append(s.Workspaces, w)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func slugs(t *testing.T) []string {
	t.Helper()
	s, err := core.Read()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, w := range s.Workspaces {
		out = append(out, w.Slug)
	}
	return out
}

func touch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	// #nosec G703 -- Callers supply fixture paths rooted in sandbox(t) under t.TempDir.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- Callers supply fixture paths rooted in sandbox(t) under t.TempDir.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo makes a source repository with a commit, pushed to a bare origin, and
// returns it.
func repo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	origin, src := filepath.Join(base, "origin.git"), filepath.Join(base, "src")
	run(t, base, "init", "-q", "--bare", "-b", "main", origin)
	run(t, base, "init", "-q", "-b", "main", src)
	run(t, src, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, src, "remote", "add", "origin", origin)
	run(t, src, "push", "-q", "-u", "origin", "main")
	return src
}

// space lays out a space the way atelier makes one, with a worktree of src on
// branch, and returns the worktree.
func space(t *testing.T, root, slug, src, branch string) string {
	t.Helper()
	wt := filepath.Join(root, slug, "repo", branch)
	run(t, src, "worktree", "add", "-q", "-b", branch, wt, "origin/main")
	return wt
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30d": 30 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute, "0d": 0,
	} {
		if got, err := ParseAge(in); err != nil || got != want {
			t.Errorf("ParseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "1.5d", "-1d", "-3h", "30", "thirty days", "106752d", "213504d", "15251w", "-106752d", "9223372036854775807w"} {
		if _, err := ParseAge(in); err == nil {
			t.Errorf("ParseAge(%q) accepted", in)
		}
	}
}

// A space's age is from its last sign of life, not its birth: one made a month
// ago that its agent worked in an hour ago isn't stale.
func TestLastActive(t *testing.T) {
	sandbox(t)
	now := time.Now().Truncate(time.Second)
	w := core.Workspace{Slug: "s", Session: "s", Created: now.Add(-30 * 24 * time.Hour)}
	if got := LastActive(w); !got.Equal(w.Created) {
		t.Errorf("untouched = %v, want created", got)
	}
	w.SwappedAt = now.Add(-20 * 24 * time.Hour)
	if got := LastActive(w); !got.Equal(w.SwappedAt) {
		t.Errorf("after a swap = %v, want the swap", got)
	}
	hook := now.Add(-time.Hour)
	touch(t, filepath.Join(core.CacheDir(), "agents", "s"), hook)
	if got := LastActive(w); !got.Equal(hook) {
		t.Errorf("after a hook = %v, want the hook's %v", got, hook)
	}
	w.RetiredAt = now
	if got := LastActive(w); !got.Equal(now) {
		t.Errorf("after retiring = %v, want the retire", got)
	}
}

// A purge takes the Trash only unless told otherwise, never the space it runs
// in, and the stalest first.
func TestPlan(t *testing.T) {
	sandbox(t)
	now := time.Now()
	ago := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }
	s := &core.State{Workspaces: []core.Workspace{
		{Slug: "old-trash", Created: ago(90), Retired: true, RetiredAt: ago(40)},
		{Slug: "older-trash", Created: ago(90), Retired: true, RetiredAt: ago(60)},
		{Slug: "new-trash", Created: ago(90), Retired: true, RetiredAt: ago(2)},
		{Slug: "old-active", Created: ago(50)},
		{Slug: "here", Created: ago(50)},
	}}
	plan := func(p Policy) string {
		var out []string
		candidates, err := p.Plan(s, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range candidates {
			out = append(out, c.Slug)
		}
		return strings.Join(out, " ")
	}
	if got, want := plan(Policy{Age: 30 * 24 * time.Hour}), "older-trash old-trash"; got != want {
		t.Errorf("trash = %q, want %q", got, want)
	}
	if got, want := plan(Policy{Age: 30 * 24 * time.Hour, Active: true, Keep: []string{"here"}}), "older-trash old-active old-trash"; got != want {
		t.Errorf("--all = %q, want %q", got, want)
	}
	if got, want := plan(Policy{}), "older-trash old-trash new-trash"; got != want {
		t.Errorf("no age = %q, want the whole trash, %q", got, want)
	}
}

// Unsaved is what stands between a purge and lost work, so it errs toward
// keeping: anything not on a remote, or in a pull request, counts.
func TestUnsaved(t *testing.T) {
	root := sandbox(t)
	src := repo(t)
	wt := space(t, root, "s", src, "feat")
	w := core.Workspace{Slug: "s"}
	if u := Unsaved(w); len(u) != 0 {
		t.Fatalf("fresh worktree: %v", u)
	}
	// User display preferences must not hide work from a destructive operation.
	run(t, wt, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if u := Unsaved(w); len(u) != 1 || !strings.Contains(u[0], "uncommitted") {
		t.Fatalf("untracked file: %v", u)
	}
	run(t, wt, "add", ".")
	run(t, wt, "commit", "-q", "-m", "work")
	if u := Unsaved(w); len(u) != 1 || !strings.Contains(u[0], "1 unpushed") || !strings.HasPrefix(u[0], "repo/feat: ") {
		t.Fatalf("unpushed commit: %v", u)
	}
	head := run(t, wt, "rev-parse", "HEAD")
	w.PRs = []core.PR{{HeadOID: head, State: core.PRMerged}}
	if u := Unsaved(w); len(u) != 0 {
		t.Fatalf("HEAD is a PR's head, which GitHub keeps: %v", u)
	}
	w.PRs = nil
	run(t, wt, "push", "-q", "origin", "feat")
	if u := Unsaved(w); len(u) != 0 {
		t.Fatalf("pushed: %v", u)
	}
	// A worktree git can't read is assumed to hold work.
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /nowhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if u := Unsaved(w); len(u) != 1 || !strings.Contains(u[0], "unreadable") {
		t.Fatalf("unreadable worktree: %v", u)
	}
}

// Purge reads the space again before deleting it: restored or given new work
// since the plan was shown, it's kept.
func TestPurge(t *testing.T) {
	root := sandbox(t)
	src := repo(t)
	old := time.Now().Add(-60 * 24 * time.Hour)
	for _, slug := range []string{"gone", "restored", "busy"} {
		space(t, root, slug, src, "b-"+slug)
		put(t, core.Workspace{Slug: slug, Created: old, Retired: true, RetiredAt: old})
	}
	touch(t, filepath.Join(core.CacheDir(), "agents", "gone"), old)
	p := Policy{Age: 30 * 24 * time.Hour}
	candidates, err := p.Plan(core.Load(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(candidates); got != 3 {
		t.Fatalf("planned %d, want 3", got)
	}
	// Meanwhile: one is restored, one gets work.
	if err := core.Update(func(s *core.State) { s.Find("restored").Retired = false }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "busy", "repo", "b-busy", "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if why, err := p.Purge("restored", time.Now()); err != nil || why == "" {
		t.Errorf("restored space: skipped=%q err=%v, want kept", why, err)
	}
	if why, err := p.Purge("busy", time.Now()); err != nil || !strings.Contains(why, "unsaved") {
		t.Errorf("busy space: skipped=%q err=%v, want kept for unsaved work", why, err)
	}
	if why, err := p.Purge("gone", time.Now()); err != nil || why != "" {
		t.Fatalf("stale space: skipped=%q err=%v", why, err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone")); !os.IsNotExist(err) {
		t.Error("directory left behind")
	}
	if _, err := os.Stat(filepath.Join(core.CacheDir(), "agents", "gone")); !os.IsNotExist(err) {
		t.Error("status file left behind")
	}
	if got := strings.Join(slugs(t), " "); strings.Contains(got, "gone") {
		t.Errorf("record left behind: %s", got)
	}
	// Its worktree went through git: the source repo no longer lists it.
	if out := run(t, src, "worktree", "list"); strings.Contains(out, "b-gone") {
		t.Errorf("source repo still lists the worktree:\n%s", out)
	}
	if why, _ := p.Purge("gone", time.Now()); why != "already gone" {
		t.Errorf("purging twice: %q", why)
	}

	p.Force = true
	if why, err := p.Purge("busy", time.Now()); err != nil || why != "" {
		t.Errorf("--force: skipped=%q err=%v", why, err)
	}
}

// A restore owns the state lock while updating the record. Purge must wait
// before it reads or removes anything, then see the restored record.
func TestPurgeWaitsForRestore(t *testing.T) {
	root := sandbox(t)
	w := core.Workspace{Slug: "restoring", Session: "restoring", Created: time.Now().Add(-time.Hour), Retired: true}
	put(t, w)
	if err := os.Mkdir(filepath.Join(root, w.Slug), 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, err := core.LockFile(core.StatePath() + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	type result struct {
		skipped string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		skipped, err := (Policy{}).Purge(w.Slug, time.Now())
		done <- result{skipped, err}
	}()
	// Give the competing purge an opportunity to reach its destructive step.
	select {
	case r := <-done:
		t.Fatalf("purge finished while restoration held the state lock: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(w.Root()); err != nil {
		t.Error("purge removed the space while restoration held the state lock")
	}
	w.Retired = false
	data, _ := json.Marshal(core.State{Version: 1, Workspaces: []core.Workspace{w}})
	if err := os.WriteFile(core.StatePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	unlock()
	unlock = nil
	select {
	case r := <-done:
		if r.err != nil || r.skipped != "changed since listed" {
			t.Errorf("purge after restore: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("purge did not finish after restoration unlocked state")
	}
	if s := core.Load(); s.Find(w.Slug) == nil {
		t.Fatal("purge dropped the restored record")
	}
}

// A slug becomes a path under the root; the state file is hand-editable, so a
// bad one must never let a delete reach the root or above it.
func TestDeleteRefusesBadSlugs(t *testing.T) {
	root := sandbox(t)
	keep := filepath.Join(root, "keep")
	if err := os.Mkdir(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"", ".", "..", "../x", "a/b", ".hidden"} {
		if err := Delete(core.Workspace{Slug: slug}); err == nil {
			t.Errorf("Delete(%q) went ahead", slug)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a refused delete removed something")
	}
}

// Session names also form cache paths. A hand-edited record must not turn
// status cleanup into a delete outside the agent cache directory.
func TestDeleteRefusesBadSessions(t *testing.T) {
	root := sandbox(t)
	sentinel := filepath.Join(core.CacheDir(), "sentinel")
	touch(t, sentinel, time.Now())
	// Ensure agents exists so the pre-fix ../sentinel path can be resolved.
	if err := os.MkdirAll(filepath.Join(core.CacheDir(), "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "keep")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"../sentinel", "", ".", "..", "a/b"} {
		if err := Delete(core.Workspace{Slug: "keep", Session: session}); err == nil {
			t.Errorf("Delete with session %q went ahead", session)
		}
	}
	for _, path := range []string{dir, sentinel} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("a refused delete removed %s", path)
		}
	}
}

func TestSweepPreservesSwapLocks(t *testing.T) {
	sandbox(t)
	path := filepath.Join(core.CacheDir(), "locks", "swap-gone.lock")
	unused := filepath.Join(core.CacheDir(), "locks", "swap-unused.lock")
	touch(t, unused, time.Now().Add(-24*time.Hour))
	unlock, err := core.LockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	for _, dry := range []bool{true, false} {
		r, err := Sweep(dry, time.Now())
		if err != nil || r.Cache != 0 {
			t.Errorf("busy lock (dry=%v): report %+v, error %v; want no removal reported", dry, r, err)
		}
		for _, lock := range []string{path, unused} {
			if _, err := os.Stat(lock); err != nil {
				t.Errorf("removed lock file %s", lock)
			}
		}
	}
}

func TestSweep(t *testing.T) {
	root := sandbox(t)
	now := time.Now()
	old := now.Add(-24 * time.Hour)
	mk := func(slug string, guide bool) string {
		dir := filepath.Join(root, slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if guide {
			if err := os.Symlink(core.WorkspaceGuidePath(), filepath.Join(dir, "CLAUDE.md")); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	mk("kept", true)
	put(t,
		core.Workspace{Slug: "kept", Created: old},
		core.Workspace{Slug: "vanished", Created: old, Retired: true},
		core.Workspace{Slug: "being-made", Created: now}, // its directory is moments away
	)
	orphan := mk("orphan", true)
	orphanAt := now.Add(-72 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(orphan, orphanAt, orphanAt); err != nil {
		t.Fatal(err)
	}
	mk("not-a-space", false)
	mk(".hidden", true)

	cache := core.CacheDir()
	touch(t, filepath.Join(cache, "agents", "kept"), now)
	touch(t, filepath.Join(cache, "agents", "orphan"), now)
	touch(t, filepath.Join(cache, "agents", "vanished"), old)
	touch(t, filepath.Join(cache, "agents", "long-deleted"), old)
	touch(t, filepath.Join(cache, "locks", "swap-kept.lock"), now)
	touch(t, filepath.Join(cache, "locks", "swap-vanished.lock"), old)
	touch(t, filepath.Join(cache, "locks", "swap-long-deleted.lock"), old)
	touch(t, filepath.Join(cache, "locks", "settings.json.lock"), old) // not a space's
	touch(t, filepath.Join(cache, "worktrees", "fresh"), now)
	touch(t, filepath.Join(cache, "worktrees", "stale"), now.Add(-48*time.Hour))

	claude := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	cfg, _ := json.Marshal(map[string]any{"projects": map[string]any{
		filepath.Join(root, "kept"): map[string]any{}, filepath.Join(root, "vanished"): map[string]any{},
	}})
	touch(t, claude, now)
	// #nosec G703 -- sandbox(t) sets CLAUDE_CONFIG_DIR to this test's temporary directory.
	if err := os.WriteFile(claude, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(core.StatePath())
	dry, err := Sweep(true, now)
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(core.StatePath()); string(after) != string(before) {
		t.Fatal("a dry run changed the state")
	}
	if _, err := os.Stat(filepath.Join(cache, "agents", "long-deleted")); err != nil {
		t.Fatal("a dry run removed a cache file")
	}

	r, err := Sweep(false, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.Records, " ") != "vanished" || strings.Join(r.Adopted, " ") != "orphan" || r.Cache != 3 ||
		len(r.Projects) != 1 || len(r.Sessions) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if strings.Join(dry.Records, " ") != "vanished" || strings.Join(dry.Adopted, " ") != "orphan" || dry.Cache != 3 || len(dry.Projects) != 1 {
		t.Errorf("dry report %+v differs from the real one %+v", dry, r)
	}

	s, _ := core.Read()
	if got := strings.Join(slugs(t), " "); strings.Contains(got, "vanished") || !strings.Contains(got, "being-made") {
		t.Errorf("records = %s; want vanished dropped, being-made kept", got)
	}
	a := s.Find("orphan")
	if a == nil || !a.Retired || !a.Created.IsZero() || !a.RetiredAt.Equal(orphanAt) || a.Session != "orphan" {
		t.Fatalf("adopted = %+v; want in the Trash, aged from its directory's mtime", a)
	}
	for _, f := range []string{"agents/kept", "agents/orphan", "locks/swap-kept.lock", "locks/swap-vanished.lock", "locks/swap-long-deleted.lock", "locks/settings.json.lock", "worktrees/fresh"} {
		if _, err := os.Stat(filepath.Join(cache, f)); err != nil {
			t.Errorf("%s was removed", f)
		}
	}
	for _, f := range []string{"agents/long-deleted", "agents/vanished", "worktrees/stale"} {
		if _, err := os.Stat(filepath.Join(cache, f)); !os.IsNotExist(err) {
			t.Errorf("%s was kept", f)
		}
	}

	if again, err := Sweep(false, now); err != nil || !again.Empty() {
		t.Errorf("second sweep = %+v, %v; want nothing left", again, err)
	}
}

// With the root itself missing (an unmounted volume), every space looks
// deleted. The sweep must judge none of them by their directories.
func TestSweepMissingRoot(t *testing.T) {
	root := sandbox(t)
	put(t, core.Workspace{Slug: "a", Created: time.Now().Add(-time.Hour)})
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	r, err := Sweep(false, time.Now())
	if err != nil || len(r.Records) != 0 {
		t.Fatalf("report = %+v, %v; want no records dropped", r, err)
	}
	if got := slugs(t); len(got) != 1 {
		t.Fatalf("records = %v", got)
	}
}

// A state file that can't be read must stop the sweep: read as empty, every
// directory would look orphaned and every cache file stale.
func TestSweepUnreadableState(t *testing.T) {
	sandbox(t)
	touch(t, core.StatePath(), time.Now())
	if err := os.WriteFile(core.StatePath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(core.CacheDir(), "agents", "someone"), time.Now())
	if _, err := Sweep(false, time.Now()); err == nil {
		t.Fatal("swept over an unreadable state")
	}
	if _, err := os.Stat(filepath.Join(core.CacheDir(), "agents", "someone")); err != nil {
		t.Error("removed a cache file it couldn't judge")
	}
}

// A space deleted by hand leaves its source repo listing a worktree that's
// gone; the sweep finds that repo through the space's surviving worktrees.
func TestSweepPrunesSourceRepos(t *testing.T) {
	root := sandbox(t)
	src := repo(t)
	space(t, root, "live", src, "a")
	put(t, core.Workspace{Slug: "live", Created: time.Now()})
	dead := space(t, root, "dead", src, "b")
	if err := os.RemoveAll(filepath.Dir(filepath.Dir(dead))); err != nil {
		t.Fatal(err)
	}
	r, err := Sweep(false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Worktrees) != 1 {
		t.Fatalf("pruned %v, want the one dead worktree", r.Worktrees)
	}
	if out := run(t, src, "worktree", "list"); strings.Contains(out, "/dead/") || !strings.Contains(out, "/live/") {
		t.Errorf("worktree list after prune:\n%s", out)
	}
}
