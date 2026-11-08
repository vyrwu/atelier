package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// stateVersion is bumped only when the on-disk shape changes incompatibly.
const stateVersion = 1

// State is the single durable JSON file: the workspace index + PR cache. It is
// a cache and an index (NFR-R2), never the only record of anything — worktrees
// derive from disk, PRs from GitHub.
type State struct {
	Version    int         `json:"version"`
	Workspaces []Workspace `json:"workspaces"`
}

// Load reads the state file. A missing or unreadable/old-version file yields an
// empty state rather than an error — atelier must always start, and a view of an
// empty list is harmless. Nothing that writes may use it: see Update.
func Load() *State {
	s, err := load()
	if err != nil {
		return &State{Version: stateVersion}
	}
	return s
}

// Read is Load for a caller that acts on what it reads: a state file that can't
// be trusted is an error rather than an empty list, which would read as every
// space being gone.
func Read() (*State, error) { return load() }

// load reads the state file, distinguishing "there is none yet" (an empty state)
// from "there is one, but it can't be trusted" (an error): unreadable, corrupt,
// or written by another version. Only the first is safe to build on and save.
func load() (*State, error) {
	data, err := os.ReadFile(StatePath())
	if os.IsNotExist(err) {
		return &State{Version: stateVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", StatePath(), err)
	}
	if s.Version != stateVersion {
		return nil, fmt.Errorf("%s is version %d; this atelier reads %d", StatePath(), s.Version, stateVersion)
	}
	return &s, nil
}

// Find returns the workspace with the given slug, or nil.
func (s *State) Find(slug string) *Workspace {
	for i := range s.Workspaces {
		if s.Workspaces[i].Slug == slug {
			return &s.Workspaces[i]
		}
	}
	return nil
}

// FindBySession returns the workspace bound to a tmux session name, or nil.
func (s *State) FindBySession(session string) *Workspace {
	for i := range s.Workspaces {
		if s.Workspaces[i].Session == session {
			return &s.Workspaces[i]
		}
	}
	return nil
}

// save writes the state atomically (temp + rename). Callers reach it through
// Update, which serialises the read-modify-write under a file lock (NFR-R4).
func (s *State) save() error {
	s.Version = stateVersion
	sort.SliceStable(s.Workspaces, func(i, j int) bool {
		return s.Workspaces[i].Created.After(s.Workspaces[j].Created)
	})
	path := StatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// A fresh 0600 file also avoids reusing an old, more permissive .tmp file.
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// Update runs fn against the current state under an exclusive lock, then saves
// atomically. The UI, the builder, and the MCP server call this; the lock
// serialises them so a concurrent write never corrupts the file (NFR-R4).
func Update(fn func(*State)) error {
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	// Build only on a state that was read cleanly. Saving over one that failed to
	// read would replace every space with an empty list.
	s, err := load()
	if err != nil {
		return err
	}
	fn(s)
	return s.save()
}

// AddUniqueWorkspace assigns w a unique Slug (and Session) from gen and appends
// it, all under the state lock so concurrent creates can't collide. Falls back
// to a random suffix if gen keeps colliding. Returns w with its assigned slug.
func AddUniqueWorkspace(w Workspace, gen func() string) (Workspace, error) {
	err := Update(func(s *State) {
		taken := map[string]bool{}
		for i := range s.Workspaces {
			taken[s.Workspaces[i].Slug] = true
		}
		slug := ""
		for tries := 0; tries < 50 && slug == ""; tries++ {
			if cand := gen(); !taken[cand] {
				slug = cand
			}
		}
		if slug == "" {
			slug = gen() + "-" + shortID()
		}
		w.Slug = slug
		w.Session = slug
		s.Workspaces = append(s.Workspaces, w)
	})
	return w, err
}

// RemoveWorkspace drops a workspace by slug and saves.
func RemoveWorkspace(slug string) error {
	return Update(func(s *State) {
		out := s.Workspaces[:0]
		for _, w := range s.Workspaces {
			if w.Slug != slug {
				out = append(out, w)
			}
		}
		s.Workspaces = out
	})
}

// lock takes the state file's exclusive lock (see LockFile).
func lock() (func(), error) { return LockFile(StatePath() + ".lock") }

// LockPRs serialises writes of swept PR state: a sweep holds it from reading
// the cache to writing its result, so a PR state change saved meanwhile waits
// and lands after it rather than being overwritten by the older answer.
func LockPRs() (func(), error) { return LockFile(filepath.Join(CacheDir(), "prs.lock")) }

// LockFile takes an exclusive advisory lock on the file at path, creating it if
// need be, and returns the unlock. Blocks until acquired. It only serialises
// processes that take the same lock — atelier's own.
func LockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// #nosec G304 -- Locks the owner-configured state/cache path used by this atelier instance.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { _ = funlock(f); _ = f.Close() }, nil
}

// TryLockFile takes the lock at path only if nobody holds it: ok is false when
// another process does. The kernel drops the lock with its holder, so a crashed
// holder never leaves it taken.
func TryLockFile(path string) (unlock func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	// #nosec G304 -- Locks the owner-configured state/cache path used by this atelier instance.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := tryFlock(f); err != nil {
		_ = f.Close()
		return nil, false, nil
	}
	return func() { _ = funlock(f); _ = f.Close() }, true, nil
}

// EnsureMaitre creates the one operator record under the state lock. Reopening
// preserves its selected agent and conversation, even if the default changed.
func EnsureMaitre(agent string) (Workspace, error) {
	var w Workspace
	err := Update(func(s *State) {
		if existing := s.Find(MaitreSlug); existing != nil {
			w = *existing
			return
		}
		w = Workspace{Slug: MaitreSlug, Session: MaitreSlug, Title: MaitreTitle, Agent: agent, Created: time.Now()}
		// A missing index record must not make existing transcripts too old
		// to resume. As in GC adoption, the original creation time is unknown.
		if fi, err := os.Stat(w.Root()); err == nil && fi.IsDir() {
			w.Created = time.Time{}
		}
		s.Workspaces = append(s.Workspaces, w)
	})
	return w, err
}
