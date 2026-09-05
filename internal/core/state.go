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
// empty state rather than an error — atelier must always start.
func Load() *State {
	data, err := os.ReadFile(StatePath())
	if err != nil {
		return &State{Version: stateVersion}
	}
	var s State
	if json.Unmarshal(data, &s) != nil || s.Version != stateVersion {
		return &State{Version: stateVersion}
	}
	return &s
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
	s := Load()
	fn(s)
	return s.save()
}

// AddWorkspace inserts (or replaces) a workspace and saves.
func AddWorkspace(w Workspace) error {
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

// lock takes an exclusive advisory lock on a sidecar file, returning an unlock
// func. Blocks until acquired.
func lock() (func(), error) {
	path := StatePath() + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := flock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock state: %w", err)
	}
	return func() { _ = funlock(f); _ = f.Close() }, nil
}
