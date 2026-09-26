package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Update runs fn against the current state under an exclusive lock, then saves
// atomically. Both the UI and the hook CLI call this; the lock serialises them
// so a concurrent write never corrupts the file (NFR-R4).
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

// LockFile takes an exclusive advisory lock on the file at path, creating it if
// need be, and returns the unlock. Blocks until acquired. It only serialises
// processes that take the same lock — atelier's own.
func LockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := flock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { _ = funlock(f); _ = f.Close() }, nil
}
