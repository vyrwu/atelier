// Package statestore persists atelier's per-workspace tmux state to a
// JSON cache file so workspaces survive a tmux server restart.
//
// What this is:
//
//   - A single JSON file at $XDG_CACHE_HOME/atelier/state.json. The name
//     is fixed — deterministic across relaunches and isolated from any
//     legacy hostname-keyed cache by construction. See Path for why it is
//     neither hostname- nor socket-keyed.
//   - Atomic via write-to-temp + rename.
//   - Versioned. Version mismatch on read → treat as empty rather than
//     attempt migration (kept deliberately simple until v2 happens).
//
// What this isn't:
//
//   - A general key-value store for plugins. The schema is fixed to
//     atelier-managed workspaces, windows, and a small set of globals.
//   - A backup / DR system. It captures what tmux loses on restart, not
//     the user's git state or filesystem.
//   - Read-modify-write operations (UpdateWorkspace, UpdateWindow,
//     SetLastActiveSession, etc.) are serialized across processes
//     via flock(2) on a sibling lockfile. Without this, two atelier
//     processes performing concurrent Load+mutate+Save would clobber
//     each other's mutations (e.g. RegisterCreatedWorkspace racing
//     with a detached _bg-pull stamping freshness on another window).
//
// Honest limitations:
//
//   - Stateful TUI navigation (k9s view + scroll, lazygit cursor,
//     pgcli history) is in-process memory of those tools and CAN NOT
//     be restored. We restore the workspace + the tool's context
//     (k8s active context, claude session id) — the tool itself
//     restarts fresh.
//   - Two atelier versions on one machine writing the same cache file
//     (dev build + installed build) is a dev-only foot-gun; the
//     hostname namespace doesn't protect against it.
//   - Schema v2 wipes v1 cache. Migration plumbing added when v2 ships.
package statestore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/vyrwu/atelier/internal/debuglog"
)

// sessionNames extracts session names for diagnostic logging.
func sessionNames(ws []Workspace) []string {
	names := make([]string, 0, len(ws))
	for _, w := range ws {
		names = append(names, w.SessionName)
	}
	return names
}

// withWriteLock holds an exclusive flock(2) on a sibling lockfile of
// the state file while fn runs. Serializes read-modify-write
// operations across atelier processes — without this, a detached
// subprocess (_bg-pull / _forge-refresh stamping window state) and the
// main atelier binary (running RegisterCreatedWorkspace, OpenDefaultBranch,
// etc.) race on the cache file and the second writer clobbers the first's
// mutations.
//
// Read-only callers (Load on its own, for restore) don't need this
// — only operations that load, mutate, then save.
//
// Lockfile lives next to the state file as `<state>.lock`. Created
// on demand with mode 0600 (same as the state file itself, since the
// state contains workspace metadata).
func withWriteLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("statestore: mkdir for lock: %w", err)
	}
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("statestore: open lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("statestore: flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

// SchemaVersion is the current schema version. Bumped only when on-disk
// fields change in a way old readers can't handle.
//
// v2 (current): typed plugin-specific fields (`ClaudePrompt`,
// `ClaudeWorkspaceKind`, `ClaudeActiveSessionID`) removed from Window.
// Replaced by a generic `Metadata map[string]string` keyed by
// `<plugin>.<field>` convention. Core no longer knows about plugin-
// specific schemas; plugins own their key namespaces.
const SchemaVersion = 2

// State is the root persisted shape. One file per host.
type State struct {
	// SchemaVersion lets readers detect old caches and skip them.
	SchemaVersion int `json:"schema_version"`

	// Hostname recorded for diagnostics — readers don't enforce this
	// matches; the FILENAME is hostname-scoped, this is informational.
	Hostname string `json:"hostname,omitempty"`

	// CapturedAt is the unix epoch (seconds) of the last successful Save.
	CapturedAt int64 `json:"captured_at,omitempty"`

	// Workspaces is the full set of atelier-managed workspaces. Each
	// workspace is one tmux session containing one or more windows.
	// Keyed in the file (in JSON it's an array, but UpdateWindow looks
	// up by session_name).
	Workspaces []Workspace `json:"workspaces,omitempty"`

	// Globals is a small set of cross-workspace tmux globals that
	// atelier owns (k8s active context, pg active endpoint).
	Globals map[string]string `json:"globals,omitempty"`

	// LastActiveSession is the name of the workspace the user had
	// focus on most recently. The bundled launcher attaches to this
	// session on next launch instead of the bare "default" so the
	// user resumes where they left off. Updated by the
	// client-session-changed hook via stamp-last-active.
	//
	// Empty = no last-active known yet (first launch); launcher
	// falls back to "default".
	LastActiveSession string `json:"last_active_session,omitempty"`
}

// Workspace is one atelier-managed tmux session — either a single-repo
// session (Kind=worktree, RepoPath set) or a multi-repo session
// (Kind=multi-repo).
type Workspace struct {
	SessionName string   `json:"session_name"`
	RepoPath    string   `json:"repo_path,omitempty"` // empty for multi-repo
	Kind        string   `json:"kind,omitempty"`      // "worktree" | "multi-repo" | ""
	Windows     []Window `json:"windows,omitempty"`
}

// Window is one tmux window in an atelier workspace — typically a git
// worktree branch.
//
// Core owns the intrinsic fields (Name, Cwd, Branch) plus a small set
// of cross-plugin primitives that core itself renders (Attention,
// Recap, RecapTs — surfaced in the picker and statusline). Everything
// else is plugin-namespaced metadata in the Metadata bag.
type Window struct {
	// Name is the tmux window name; together with SessionName this is
	// the persistent identity (tmux $/@ IDs are reassigned on every
	// server restart so we can't key on them).
	Name string `json:"name"`

	// Cwd is the worktree path; new-window -c restores this on resume.
	Cwd string `json:"cwd,omitempty"`

	// Branch (informational; the worktree at Cwd is the source of truth).
	Branch string `json:"branch,omitempty"`

	// CreatedTs mirrors the @created_ts tmux window option (unix epoch of
	// when the window was created). Persisted so restore re-stamps it and
	// the picker's Age sort survives a tmux restart — without this a
	// restored workspace would look brand-new. Stamped once at creation
	// and never mutated.
	CreatedTs int64 `json:"created_ts,omitempty"`

	// Attention is the @needs_attention flag — a generic
	// "this window wants the user's eyes" signal. Core renders it
	// in the picker + statusline; ANY plugin can write it (today
	// only the AI plugin does, but that's not a core assumption).
	Attention bool `json:"attention,omitempty"`

	// Recap, RecapTs match @attention_recap / @attention_recap_ts —
	// generic per-window "what was happening here" string the picker
	// renders next to Attention. Plugin-written, core-rendered.
	Recap   string `json:"recap,omitempty"`
	RecapTs int64  `json:"recap_ts,omitempty"`

	// Metadata is a plugin-namespaced bag of per-window state. Keys
	// follow the convention `<plugin>.<field>` (e.g. `ai.prompt`,
	// `ai.workspace_kind`, `ai.active_session_id`). Each plugin owns
	// its namespace; core never inspects keys.
	//
	// Restore stamps every Metadata key as a tmux window option
	// `@<plugin>_<field>` (dots in the metadata key become
	// underscores in the tmux option name) so plugins can read
	// their state through the standard `tmux show-options` path
	// after a server restart without consulting the cache directly.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// stateFileName is the fixed cache filename. Deliberately NOT keyed by
// hostname or tmux socket — see Path.
const stateFileName = "state.json"

// Path returns the canonical state-file path. $XDG_CACHE_HOME defaults to
// $HOME/.cache. The filename is FIXED (not hostname- or socket-keyed):
//
//   - Hostname-keying (the original) silently split one machine's state
//     across several files as the network-dependent hostname flapped
//     (.local / .localdomain / .home) — a different workspace set every
//     time the network changed.
//   - Socket-keying fixed the flap but made the key depend on an env var
//     (ATELIER_TMUX_SOCKET) whose value differs between a launcher and the
//     subprocesses it spawns, and across a relaunch onto a fresh test
//     socket — non-deterministic in exactly the seams persistence must be
//     reliable in.
//
// A fixed name is deterministic, survives relaunch, and stays isolated
// from any legacy hostname-keyed cache by construction (different file).
func Path() string {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(cache, "atelier", stateFileName)
}

// Load reads the cache file. Returns (nil, nil) if absent or schema
// mismatch — callers treat that as "no prior state, start fresh."
// Malformed JSON returns the error.
func Load() (*State, error) {
	return loadFrom(Path())
}

func loadFrom(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			debuglog.Logf("statestore.Load: path=%s ABSENT (no prior state)", path)
			return nil, nil
		}
		return nil, fmt.Errorf("statestore: read %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("statestore: parse %s: %w", path, err)
	}
	if s.SchemaVersion != SchemaVersion {
		// Old or future cache — treat as empty rather than crash.
		// Future versions can add migration here.
		debuglog.Logf("statestore.Load: path=%s SCHEMA-MISMATCH v%d≠v%d → treated as empty",
			path, s.SchemaVersion, SchemaVersion)
		return nil, nil
	}
	// Same filter as Save: drop any non-atelier entries on read so a
	// cache poisoned by older code paths (pre-scope-fix, or test
	// seeds) doesn't keep resurrecting random sessions on restore.
	s.Workspaces = filterAtelierManaged(s.Workspaces)
	debuglog.Logf("statestore.Load: path=%s workspaces=%d sessions=%v last_active=%q",
		path, len(s.Workspaces), sessionNames(s.Workspaces), s.LastActiveSession)
	return &s, nil
}

// filterAtelierManaged returns only workspaces with RepoPath or Kind
// set — the atelier-managed scope. Non-atelier sessions that leaked
// in via SetRecap / SetAttention write-through on random windows
// (claude hook firing in a non-atelier session, manual seeds, etc.)
// are dropped silently.
func filterAtelierManaged(ws []Workspace) []Workspace {
	out := ws[:0]
	for _, w := range ws {
		if w.RepoPath == "" && w.Kind == "" {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Save writes the state atomically. Empty state still writes (records
// "atelier has been here, nothing to restore yet" — distinguishable
// from "no cache").
func Save(s *State) error {
	return saveTo(Path(), s)
}

func saveTo(path string, s *State) error {
	if s == nil {
		s = &State{}
	}
	s.SchemaVersion = SchemaVersion
	if s.Hostname == "" {
		s.Hostname, _ = os.Hostname()
	}
	// Filter to atelier-managed workspaces only. By the user's spec, an
	// atelier workspace is one with RepoPath OR Kind set. Anything else
	// (random user tmux sessions, stale seeds from testing) does not
	// belong in the cache — it would otherwise be restored on every
	// tmux start, polluting the workspace list.
	s.Workspaces = filterAtelierManaged(s.Workspaces)
	// CapturedAt stamped by caller via now() or left at zero — caller
	// owns the timestamp (Save shouldn't lie about WHEN by reading the
	// clock implicitly). Callers that want a fresh stamp set it before
	// calling Save.
	debuglog.Logf("statestore.Save: path=%s workspaces=%d sessions=%v last_active=%q",
		path, len(s.Workspaces), sessionNames(s.Workspaces), s.LastActiveSession)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("statestore: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("statestore: marshal: %w", err)
	}
	// Atomic write: write-to-temp + rename. If we crash mid-write the
	// temp file is orphaned (cleaned on next mkdir or by user) but the
	// real cache file is intact at its previous good state.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("statestore: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("statestore: write tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("statestore: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("statestore: close tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("statestore: rename: %w", err)
	}
	return nil
}

// UpdateWindow finds (or creates) the workspace + window record and
// applies `mutate`. Persists the result atomically. If `mutate` is nil,
// just ensures the workspace+window record exists.
//
// This is the WORKHORSE for write-through: SetRecap / SetAttention /
// notify-attention all call UpdateWindow with a small closure.
func UpdateWindow(sessionName, windowName string, mutate func(*Window)) error {
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			s = &State{}
		}
		ws := findOrAppendWorkspace(s, sessionName)
		w := findOrAppendWindow(ws, windowName)
		if mutate != nil {
			mutate(w)
		}
		return Save(s)
	})
}

// UpdateWorkspace finds (or creates) the workspace record and applies
// `mutate`. Used when registering a fresh workspace (set RepoPath, Kind).
func UpdateWorkspace(sessionName string, mutate func(*Workspace)) error {
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			s = &State{}
		}
		ws := findOrAppendWorkspace(s, sessionName)
		if mutate != nil {
			mutate(ws)
		}
		return Save(s)
	})
}

// UpdateGlobal sets one global key. Pass value="" to delete.
// SetLastActiveSession writes the name of the workspace the user
// most recently focused on. Called from the client-session-changed
// hook via `atelier internal stamp-last-active`. The bundled
// launcher reads this on startup to resume the prior workspace
// instead of dumping the user on a bare "default" session.
//
// Empty session name no-ops (which also clears the field — useful
// when the active session is the bundled "default" itself).
func SetLastActiveSession(session string) error {
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			s = &State{}
		}
		s.LastActiveSession = session
		return Save(s)
	})
}

func UpdateGlobal(key, value string) error {
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			s = &State{}
		}
		if s.Globals == nil {
			s.Globals = map[string]string{}
		}
		if value == "" {
			delete(s.Globals, key)
		} else {
			s.Globals[key] = value
		}
		return Save(s)
	})
}

// RemoveSession drops a workspace from the cache entirely. Called by
// the session-closed tmux hook.
func RemoveSession(sessionName string) error {
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			return nil
		}
		before := len(s.Workspaces)
		out := s.Workspaces[:0]
		for _, ws := range s.Workspaces {
			if ws.SessionName != sessionName {
				out = append(out, ws)
			}
		}
		s.Workspaces = out
		debuglog.Logf("statestore.RemoveSession: session=%q dropped=%d (%d→%d) path=%s",
			sessionName, before-len(out), before, len(out), Path())
		return Save(s)
	})
}

// RemoveWindow drops one window from a workspace. If the workspace ends
// up with zero windows, it's removed entirely (an empty session is
// meaningless to restore).
//
// Holds the write lock across load→mutate→save like every other
// mutator: without it, this read-modify-write interleaves with a
// concurrent locked write (e.g. RegisterCreatedWorkspace's UpdateWindow)
// and the stale-read save clobbers the other writer's mutations —
// e.g. a freshly-persisted window's ai.prompt metadata silently lost.
func RemoveWindow(sessionName, windowName string) error {
	debuglog.Logf("statestore.RemoveWindow: session=%q window=%q path=%s", sessionName, windowName, Path())
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			return nil
		}
		for i := range s.Workspaces {
			ws := &s.Workspaces[i]
			if ws.SessionName != sessionName {
				continue
			}
			out := ws.Windows[:0]
			for _, w := range ws.Windows {
				if w.Name != windowName {
					out = append(out, w)
				}
			}
			ws.Windows = out
			// Drop the now-empty workspace inline rather than calling
			// RemoveSession — that re-enters withWriteLock on the same
			// lockfile and would self-deadlock.
			if len(ws.Windows) == 0 {
				s.Workspaces = append(s.Workspaces[:i], s.Workspaces[i+1:]...)
			}
			break
		}
		return Save(s)
	})
}

// RenameWindow updates a window's Name in place. Called by the
// window-renamed hook. Holds the write lock across load→mutate→save
// (see RemoveWindow) so a concurrent locked write isn't clobbered.
func RenameWindow(sessionName, oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			return nil
		}
		for i := range s.Workspaces {
			ws := &s.Workspaces[i]
			if ws.SessionName != sessionName {
				continue
			}
			for j := range ws.Windows {
				if ws.Windows[j].Name == oldName {
					ws.Windows[j].Name = newName
					return Save(s)
				}
			}
		}
		return nil
	})
}

// FindWindow returns the Window record for a given (session, window)
// pair, or nil if not present.
func (s *State) FindWindow(sessionName, windowName string) *Window {
	if s == nil {
		return nil
	}
	for i := range s.Workspaces {
		ws := &s.Workspaces[i]
		if ws.SessionName != sessionName {
			continue
		}
		for j := range ws.Windows {
			if ws.Windows[j].Name == windowName {
				return &ws.Windows[j]
			}
		}
	}
	return nil
}

func findOrAppendWorkspace(s *State, sessionName string) *Workspace {
	for i := range s.Workspaces {
		if s.Workspaces[i].SessionName == sessionName {
			return &s.Workspaces[i]
		}
	}
	s.Workspaces = append(s.Workspaces, Workspace{SessionName: sessionName})
	return &s.Workspaces[len(s.Workspaces)-1]
}

func findOrAppendWindow(ws *Workspace, name string) *Window {
	for i := range ws.Windows {
		if ws.Windows[i].Name == name {
			return &ws.Windows[i]
		}
	}
	ws.Windows = append(ws.Windows, Window{Name: name})
	return &ws.Windows[len(ws.Windows)-1]
}
