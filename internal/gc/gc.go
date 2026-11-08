// Package gc deletes spaces and sweeps up after them: one space from the
// Trash, every space untouched for longer than an age, and whatever deleted or
// half-made spaces left behind — records, directories, sessions, and caches.
package gc

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/tmux"
)

// ParseAge reads an age: a number of days ("30d") or weeks ("2w"), or any Go
// duration ("36h", "90m").
func ParseAge(s string) (time.Duration, error) {
	var d time.Duration
	var err error
	if n, ok := strings.CutSuffix(s, "d"); ok {
		d, err = days(n, 1)
	} else if n, ok := strings.CutSuffix(s, "w"); ok {
		d, err = days(n, 7)
	} else {
		d, err = time.ParseDuration(s)
	}
	if err != nil || d < 0 {
		return 0, fmt.Errorf("bad age %q: want e.g. 30d, 2w, or 36h", s)
	}
	return d, nil
}

func days(n string, per int) (time.Duration, error) {
	v, err := strconv.ParseInt(n, 10, 64)
	unit := int64(per) * int64(24*time.Hour)
	if err != nil || v < 0 || v > math.MaxInt64/unit {
		return 0, strconv.ErrRange
	}
	return time.Duration(v * unit), nil
}

// LastActive is when w was last touched: made, swapped, retired, restored, or
// its agent's latest hook event — whichever came last.
func LastActive(w core.Workspace) time.Time {
	t := w.Created
	if t.IsZero() {
		if fi, err := os.Stat(w.Root()); err == nil {
			t = fi.ModTime() // recovered spaces have no reliable creation cutoff
		}
	}
	for _, u := range []time.Time{w.SwappedAt, w.RetiredAt, agent.LastEvent(w.Session)} {
		if u.After(t) {
			t = u
		}
	}
	return t
}

// Unsaved checks worktrees and unrecognized workspace files conservatively.
// Atelier's own guides and agent metadata are excluded; an incomplete scan
// never counts as proof that a workspace is clean.
func Unsaved(w core.Workspace) []string {
	var published []string
	for _, p := range w.PRs {
		if p.HeadOID != "" {
			published = append(published, p.HeadOID)
		}
	}
	var out []string
	root := w.Root()
	if fi, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil || !fi.IsDir() {
		return []string{"unreadable workspace directory"}
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if filepath.Dir(path) == root {
			switch d.Name() {
			case ".atelier", ".claude", ".codex", ".grok", ".cursor", "CLAUDE.md", "AGENTS.md":
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			fi, err := os.Lstat(filepath.Join(path, ".git"))
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			why := "unrecognized repository"
			if fi.Mode().IsRegular() {
				why = git.Unsaved(path, published)
			}
			if why != "" {
				out = append(out, rel+": "+why)
			}
			return filepath.SkipDir
		}
		out = append(out, rel+": file outside a readable worktree")
		return filepath.SkipAll
	})
	if err != nil {
		out = append(out, "incomplete workspace scan: "+err.Error())
	}
	return out
}

// Delete removes w for good: its session, its worktrees through git (so no
// source repository keeps a record of a checkout that's gone), its directory,
// and its record. The record goes last, so a space that fails to delete stays
// listed and can be deleted again.
func Delete(w core.Workspace) error {
	var deleteErr error
	err := core.Update(func(s *core.State) {
		if current := s.Find(w.Slug); current != nil {
			w = *current
		}
		deleteErr = deleteWorkspace(s, w)
	})
	return errors.Join(deleteErr, err)
}

// deleteWorkspace runs with the state lock held, which also serializes Trash
// restoration. Drop the record only after all files are removed successfully.
func deleteWorkspace(s *core.State, w core.Workspace) error {
	if !core.ValidSpaceName(w.Slug) {
		return fmt.Errorf("refusing to delete %q: not a space's directory name", w.Slug)
	}
	if !core.ValidSpaceName(w.Session) {
		return fmt.Errorf("refusing to delete %q: invalid session name %q", w.Slug, w.Session)
	}
	tmux.MoveClients(w.Session, tmux.HomeSession)
	if err := tmux.StopSession(w.Session); err != nil {
		return err
	}
	for _, t := range git.Worktrees(w.Root()) {
		_ = git.RemoveWorktree(t.Path)
	}
	if err := os.RemoveAll(w.Root()); err != nil {
		return err
	}
	_ = agent.ClearStatus(w.Session)
	kept := s.Workspaces[:0]
	for _, entry := range s.Workspaces {
		if entry.Slug != w.Slug {
			kept = append(kept, entry)
		}
	}
	s.Workspaces = kept
	return nil
}

// Policy picks the spaces a purge deletes.
type Policy struct {
	Age    time.Duration // untouched for at least this long
	Active bool          // active spaces too, not only the Trash
	Force  bool          // spaces holding unsaved work too
	Keep   []string      // the caller's enclosing and working-directory spaces
}

// Candidate is a space a policy selects, with any work deleting it would lose.
type Candidate struct {
	core.Workspace
	Idle    time.Duration
	Unsaved []string
	Running bool
}

// Plan is every space p selects in s, stalest first. Those holding unsaved
// work are included, for the caller to show; Purge skips them unless p.Force.
func (p Policy) Plan(s *core.State, now time.Time) ([]Candidate, error) {
	live, err := tmux.ReadSessions()
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, w := range s.Workspaces {
		if p.selects(w, now) {
			out = append(out, Candidate{Workspace: w, Idle: now.Sub(LastActive(w)), Running: slices.Contains(live, w.Session)})
		}
	}
	// A git status per worktree adds up over a Trash of hundreds; run a few at once.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range out {
		wg.Add(1)
		sem <- struct{}{}
		go func(c *Candidate) {
			defer func() { <-sem; wg.Done() }()
			c.Unsaved = Unsaved(c.Workspace)
		}(&out[i])
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Idle > out[j].Idle })
	return out, nil
}

func (p Policy) selects(w core.Workspace, now time.Time) bool {
	return !slices.Contains(p.Keep, w.Slug) && (w.Retired || p.Active) && now.Sub(LastActive(w)) >= p.Age
}

// Purge deletes the space slug if p still selects it. Its record and worktrees
// are read again first, since either may have changed since the plan was shown:
// the space restored, or work added. The state lock spans the final checks and
// deletion, so restoration cannot interleave. skipped says why it was left.
func (p Policy) Purge(slug string, now time.Time) (skipped string, err error) {
	var deleteErr error
	err = core.Update(func(s *core.State) {
		w := s.Find(slug)
		switch {
		case w == nil:
			skipped = "already gone"
			return
		case !p.selects(*w, now):
			skipped = "changed since listed"
			return
		}
		live, err := tmux.ReadSessions()
		if err != nil {
			deleteErr = err
			return
		}
		if slices.Contains(live, w.Session) && !p.Force {
			skipped = "session is running; retire the space first or use --force"
			return
		}
		if u := Unsaved(*w); len(u) > 0 && !p.Force {
			skipped = "unsaved work: " + strings.Join(u, ", ")
			return
		}
		deleteErr = deleteWorkspace(s, *w)
	})
	return skipped, errors.Join(deleteErr, err)
}

// Report is what a sweep found — or, dry, would have.
type Report struct {
	Records       []string // records whose directory is gone, dropped
	Adopted       []string // space directories with no record, put in the Trash
	ActiveAdopted []string // running spaces recovered without stopping their sessions
	Sessions      []string // sessions still running for spaces in the Trash, stopped
	Cache         int      // cache files for spaces that no longer exist, removed
	Projects      []string // Claude project entries for deleted directories, dropped
	Worktrees     []string // source repositories' records of deleted worktrees, pruned
}

// Empty reports whether the sweep found nothing.
func (r Report) Empty() bool {
	return len(r.Records)+len(r.Adopted)+len(r.ActiveAdopted)+len(r.Sessions)+r.Cache+len(r.Projects)+len(r.Worktrees) == 0
}

// grace covers a space being made: its record is written before its directory.
const grace = 10 * time.Minute

// Sweep collects what spaces leave behind. Nothing it removes holds work: a
// directory without a record is put in the Trash, not deleted, where it can be
// restored or purged. With dry set it only reports.
func Sweep(dry bool, now time.Time) (Report, error) {
	var r Report
	var errs []error
	// Cache files are listed before the state is read, so any that belong to a
	// space made meanwhile belong to a record the read sees.
	cache := cacheFiles()
	root := core.AteliersRoot()
	rootOK := isDir(root)
	live := map[string]bool{}
	sessions, err := tmux.ReadSessions()
	if err != nil {
		return r, err
	}
	for _, s := range sessions {
		live[s] = true
	}

	var st *core.State
	var applyErr error
	if dry {
		if st, err = core.Read(); err == nil {
			r.Records, r.Adopted, r.Sessions = triage(st, root, rootOK, live, now)
			applyErr = apply(st, root, &r, live, true)
		}
	} else {
		err = core.Update(func(s *core.State) {
			r.Records, r.Adopted, r.Sessions = triage(s, root, rootOK, live, now)
			applyErr = apply(s, root, &r, live, false)
			st = s
		})
	}
	if err != nil {
		return r, err // without a state to trust, everything else would read as garbage
	}
	errs = append(errs, applyErr)

	r.Cache, err = sweepCache(cache, st, now, dry)
	errs = append(errs, err)
	if rootOK {
		r.Projects, err = agent.PruneProjects(root, dry)
		errs = append(errs, err)
		r.Worktrees, err = pruneWorktrees(root, dry)
		errs = append(errs, err)
	}
	return r, errors.Join(errs...)
}

// triage finds, in s, the records whose directory is gone, the space
// directories with no record, and the retired spaces whose session still runs.
// With the root itself missing — an unmounted volume, say — every space would
// look deleted, so nothing is judged by its directory.
func triage(s *core.State, root string, rootOK bool, live map[string]bool, now time.Time) (records, adopted, sessions []string) {
	for _, w := range s.Workspaces {
		if rootOK && !live[w.Session] && now.Sub(w.Created) > grace && !exists(w.Root()) {
			records = append(records, w.Slug)
		}
		if w.Retired && live[w.Session] {
			sessions = append(sessions, w.Slug)
		}
	}
	if !rootOK {
		return records, adopted, sessions
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() && core.ValidSpaceName(e.Name()) && s.Find(e.Name()) == nil && isSpaceDir(filepath.Join(root, e.Name())) {
			adopted = append(adopted, e.Name())
		}
	}
	return records, adopted, sessions
}

// apply carries out a triage on s, under the state lock. A dry run changes
// only its in-memory copy, so cache checks see the same projected state.
func apply(s *core.State, root string, r *Report, live map[string]bool, dry bool) error {
	var errs []error
	drop := map[string]bool{}
	for _, slug := range r.Records {
		drop[slug] = true
	}
	kept := s.Workspaces[:0]
	for _, w := range s.Workspaces {
		if !drop[w.Slug] {
			kept = append(kept, w)
		}
	}
	s.Workspaces = kept
	stopped := r.Sessions[:0]
	for _, slug := range r.Sessions {
		if dry {
			stopped = append(stopped, slug)
			continue
		}
		w := s.Find(slug)
		tmux.MoveClients(w.Session, tmux.HomeSession)
		if err := tmux.StopSession(w.Session); err != nil {
			errs = append(errs, err)
		} else {
			stopped = append(stopped, slug)
		}
	}
	r.Sessions = stopped
	retired := r.Adopted[:0]
	for _, slug := range r.Adopted {
		// Creation is unknown: using the directory's mtime as a transcript
		// cutoff would exclude conversations older than the last root edit.
		w := core.Workspace{Slug: slug, Session: slug, Title: slug}
		w.Agent = string(agent.RecentKind(w.Root(), agent.Of(core.LoadConfig().Agent)))
		if live[slug] {
			if out, err := tmux.Run("show-options", "-t", slug, "-qv", "@atelier_agent"); err == nil {
				for _, k := range agent.Kinds() {
					if strings.TrimSpace(string(out)) == k.Label() {
						w.Agent = string(k)
					}
				}
			}
		}
		if !live[slug] {
			retired = append(retired, slug)
			w.Retired = true
			if fi, err := os.Stat(filepath.Join(root, slug)); err == nil {
				w.RetiredAt = fi.ModTime()
			}
		} else {
			r.ActiveAdopted = append(r.ActiveAdopted, slug)
		}
		s.Workspaces = append(s.Workspaces, w)
	}
	r.Adopted = retired
	return errors.Join(errs...)
}

// isSpaceDir reports whether dir was made by atelier: it links the central
// guide in as CLAUDE.md or AGENTS.md. Anything else under the root is left be.
func isSpaceDir(dir string) bool {
	guide := filepath.Base(core.WorkspaceGuidePath())
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if target, err := os.Readlink(filepath.Join(dir, name)); err == nil && filepath.Base(target) == guide {
			return true
		}
	}
	return false
}

// freshnessTTL bounds how long a worktree's cached ahead/behind is of any use;
// the overlay recomputes it after five minutes.
const freshnessTTL = 24 * time.Hour

// cacheFiles lists agent status and worktree freshness files. Never unlink
// advisory lock files: a waiting opener can still hold the old inode while a
// new opener creates and locks a different one at the same path.
func cacheFiles() []string {
	var files []string
	for _, pat := range []string{"agents/*", "worktrees/*"} {
		m, _ := filepath.Glob(filepath.Join(core.CacheDir(), pat))
		files = append(files, m...)
	}
	return files
}

// sweepCache removes the files among files that belong to no space in s, and
// worktree freshness readings too old to be used.
func sweepCache(files []string, s *core.State, now time.Time, dry bool) (int, error) {
	known := map[string]bool{}
	for _, w := range s.Workspaces {
		known[w.Session] = true
		known[w.Slug] = true
	}
	n := 0
	var errs []error
	for _, f := range files {
		name := filepath.Base(f)
		switch filepath.Base(filepath.Dir(f)) {
		case "agents":
			if known[name] {
				continue
			}
		case "worktrees":
			if fi, err := os.Stat(f); err != nil || now.Sub(fi.ModTime()) < freshnessTTL {
				continue
			}
		}
		removed, err := removeCache(f, dry)
		errs = append(errs, err)
		if removed {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// removeCache reports only successful (or, dry, possible) removals.
func removeCache(f string, dry bool) (bool, error) {
	var err error
	if dry {
		_, err = os.Lstat(f)
	} else {
		err = os.Remove(f)
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// pruneWorktrees has every source repository a space's worktree belongs to drop
// its records of worktrees whose directories are gone — those of spaces deleted
// by hand, say. It finds them from the worktrees that remain.
func pruneWorktrees(root string, dry bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	var errs []error
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() || !core.ValidSpaceName(e.Name()) {
			continue
		}
		for _, wt := range git.WorktreeDirs(filepath.Join(root, e.Name())) {
			dir := git.SourceGitDir(wt)
			if dir == "" || seen[dir] || !isDir(dir) {
				continue
			}
			seen[dir] = true
			pruned, err := git.PruneWorktrees(dir, dry)
			errs = append(errs, err)
			for _, p := range pruned {
				out = append(out, dir+": "+p)
			}
		}
	}
	return out, errors.Join(errs...)
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return !os.IsNotExist(err)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
