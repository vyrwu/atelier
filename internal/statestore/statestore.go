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
//   - Versioned. A v2 cache is migrated to the current schema on read
//     (migrateFromV2); any other version mismatch is treated as empty.
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
//     with the stamp-last-active hook process).
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
//   - Schema v3 (the intent-workspace redesign) MIGRATES a v2 cache rather
//     than wiping it — users have live workspaces. See migrateFromV2. Any
//     other version mismatch (a far-future cache, or the never-shipped v1)
//     is treated as empty.
package statestore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// operations across atelier processes — without this, the
// stamp-last-active hook subprocess and the main atelier binary
// (running RegisterCreatedWorkspace, OpenDefaultBranch, etc.) race
// on the cache file and the second writer clobbers the first's
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
// v3 (current): the intent-workspace redesign. A Workspace is no longer "a
// tmux session that happens to be a repo" — it gains Title, Intent, Root, and
// two owned sets (Worktrees, PRs). The old `Kind` ("worktree" | "multi-repo")
// discriminator is gone: every workspace is just a workspace. Migrated from
// v2 by migrateFromV2 (repo-session → single-workspace with a derived title).
//
// v2: typed plugin-specific fields (`ClaudePrompt`, `ClaudeWorkspaceKind`,
// `ClaudeActiveSessionID`) removed from Window, replaced by a generic
// `Metadata map[string]string` keyed by `<plugin>.<field>` convention.
const SchemaVersion = 3

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

	// AIUsage is the cumulative token accounting for background AI calls
	// (recaps, naming). Written by the claude adapter after each metered call
	// and surfaced by `atelier ai usage`. Nil until the first call is
	// recorded. A top-level field (not a per-workspace one) because it
	// measures the tool, not any single workspace.
	AIUsage *AIUsage `json:"ai_usage,omitempty"`
}

// AIUsageCounts is a running tally of token spend. Same shape at the total
// and per-task levels.
type AIUsageCounts struct {
	Calls               int64   `json:"calls,omitempty"`
	InputTokens         int64   `json:"input_tokens,omitempty"`
	OutputTokens        int64   `json:"output_tokens,omitempty"`
	CacheCreationTokens int64   `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int64   `json:"cache_read_tokens,omitempty"`
	CostUSD             float64 `json:"cost_usd,omitempty"`
}

func (c *AIUsageCounts) add(d AIUsageCounts) {
	c.Calls += d.Calls
	c.InputTokens += d.InputTokens
	c.OutputTokens += d.OutputTokens
	c.CacheCreationTokens += d.CacheCreationTokens
	c.CacheReadTokens += d.CacheReadTokens
	c.CostUSD += d.CostUSD
}

// AIUsage is cumulative background-AI token spend since SinceTS, broken down
// by task ("recap", "naming"). ResetAIUsage restarts the measurement window.
type AIUsage struct {
	// SinceTS is the unix epoch (seconds) counting started — set on the first
	// recorded call and on every reset, so `atelier ai usage` can derive a
	// per-hour burn rate.
	SinceTS int64                    `json:"since_ts,omitempty"`
	Total   AIUsageCounts            `json:"total"`
	ByTask  map[string]AIUsageCounts `json:"by_task,omitempty"`
}

// Workspace is one atelier-managed tmux session — an INTENT. Its identity is
// the SessionName (= the workspace slug = the `@workspace_id` tmux option =
// the tmux session name, all the same string). It carries a human Title (what
// M-r renames), the Intent text the user typed at M-n, a dedicated Root
// directory, and two owned sets the agent produces: Worktrees and PRs.
//
// The Windows list holds the driver agent window plus any inspection shells
// the user opened. Per-agent state (recap, attention, ai.active_session_id)
// stays addressed by window — a workspace has exactly one driver window (the
// `multiple_drivers` invariant), but per-window is where that state lives so
// the door to multiple agents (WS-8) stays open without a schema bump.
type Workspace struct {
	// SessionName is the workspace id/slug and the tmux session name — the
	// persistence key. tmux mangles '.'/':' to '_', so it's canonicalized.
	SessionName string `json:"session_name"`

	// Title is the human, renameable label shown in the M-s picker. Distinct
	// from SessionName so a rename (M-r) never moves the tmux target.
	Title string `json:"title,omitempty"`

	// Intent is the free-text task description the user entered at M-n ("what
	// are we doing today?"). Seeds the driver agent's first prompt.
	Intent string `json:"intent,omitempty"`

	// Summary is the workspace-level rollup line shown under the title in the
	// M-s picker ("PRs completed, work pending your action"). Written by the
	// daemon's SummarizeWorkspace pass (WS-7), change-detection gated.
	Summary string `json:"summary,omitempty"`
	// SummaryHash is the content hash of the inputs the last Summary was
	// derived from (driver recap + PR states), so the daemon re-summarizes
	// only when an input changed.
	SummaryHash string `json:"summary_hash,omitempty"`

	// Root is the workspace's dedicated directory (~/ateliers/<slug>) that
	// worktrees are symlinked into and the driver agent runs from. Empty until
	// materialized on first use (a v2-migrated workspace has none yet).
	Root string `json:"root,omitempty"`

	// RepoPath is an optional single-repo convenience hint (the repo a
	// one-repo workspace was seeded from). A workspace spans repos, so this is
	// no longer identity — just a hint some flows read.
	RepoPath string `json:"repo_path,omitempty"`

	// Tag is the workspace's grouping label (client/initiative/subsystem),
	// mirroring @workspace_tag. Restore re-stamps it on the session.
	Tag string `json:"tag,omitempty"`

	// Windows is the driver agent window + inspection shells.
	Windows []Window `json:"windows,omitempty"`

	// Worktrees is the set of git worktrees this workspace's agent produced —
	// materialized under Root as symlinks. See the Worktree type.
	Worktrees []Worktree `json:"worktrees,omitempty"`

	// PRs is the workspace's set of registered / discovered pull requests,
	// surfaced by the M-c Changes view. See the PR type.
	PRs []PR `json:"prs,omitempty"`

	// CreatedAt is the unix epoch when the workspace was first created.
	// Persisted so the picker's age column survives restarts.
	CreatedAt int64 `json:"created_at,omitempty"`
}

// Worktree is one git worktree owned by a workspace: a repo + branch checkout
// living at its real repo-local Path (git bookkeeping unchanged) and
// symlinked into the workspace Root at Link. The agent works through the link;
// the invariants keep Link and Path in sync.
type Worktree struct {
	Repo   string `json:"repo"`           // "owner/repo"
	Branch string `json:"branch"`         // may contain slashes (feat/foo)
	Path   string `json:"path"`           // real worktree dir (~/code/.worktrees/...)
	Link   string `json:"link,omitempty"` // symlink under the workspace Root
}

// PR mirrors integration.PullRequest for persistence. statestore stays
// dependency-free (it must not import the kernel ports), so the workspaces
// kernel converts between this and integration.PullRequest at the seam. One
// entry per registered or forge-discovered pull request in the workspace.
type PR struct {
	Number         int    `json:"number"`
	Repo           string `json:"repo"` // "owner/repo"
	Title          string `json:"title,omitempty"`
	State          string `json:"state,omitempty"`           // open|draft|merged|closed
	CI             string `json:"ci,omitempty"`              // pass|fail|pending|none
	ReviewDecision string `json:"review_decision,omitempty"` // approved|changes_requested|review_required|none
	Comments       int    `json:"comments,omitempty"`
	URL            string `json:"url,omitempty"`
	Branch         string `json:"branch,omitempty"`
	UpdatedAt      int64  `json:"updated_at,omitempty"`
	// Registered is true for a PR the agent explicitly registered (atelier pr
	// register) rather than one discovered by branch-match. The forge sweep
	// refreshes registered PRs' fields but never drops them for lack of a
	// branch match.
	Registered bool `json:"registered,omitempty"`
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

	// CreatedAt mirrors this window's @workspace_created_ts option (unix
	// epoch of when the worktree window was created). Persisted PER WINDOW
	// so the picker's age column survives restarts for every window — a
	// session with multiple worktree windows otherwise shows a blank age
	// on all but the first after restore (only the session-level
	// Workspace.CreatedAt used to be re-stamped). Restore falls back to
	// the enclosing Workspace.CreatedAt when this is unset (state written
	// before per-window created_at existed).
	CreatedAt int64 `json:"created_at,omitempty"`

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
// CanonicalSessionName normalizes a session identifier to the form tmux
// actually stores: tmux silently rewrites '.' and ':' to '_' in session
// names (they're target-syntax delimiters). Every statestore key is
// canonicalized through this on both read and write so a name derived
// from a repo slug ("owner/repo.dk") matches the same name read back
// from live tmux ("owner/repo_dk"). Without it, deleting a dotted-slug
// workspace no-ops — the raw stored key never equals the mangled key
// the picker/hooks pass — and the entry resurrects on restart.
//
// This is the single implementation; workspace.SessionName delegates
// here (statestore is the lower, import-cycle-free package).
func CanonicalSessionName(name string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(name)
}

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
		// v2 → v3: migrate in place (users have live workspaces — WS-9). Any
		// other mismatch (never-shipped v1, a far-future cache) is treated as
		// empty rather than crash.
		if s.SchemaVersion == 2 {
			migrated, err := migrateFromV2(data)
			if err != nil {
				debuglog.Logf("statestore.Load: path=%s v2→v3 migration failed (%v) → treated as empty", path, err)
				return nil, nil
			}
			debuglog.Logf("statestore.Load: path=%s MIGRATED v2→v3 workspaces=%d", path, len(migrated.Workspaces))
			s = *migrated
			// Fall through to the shared filter/canonicalize pass below.
		} else {
			debuglog.Logf("statestore.Load: path=%s SCHEMA-MISMATCH v%d≠v%d → treated as empty",
				path, s.SchemaVersion, SchemaVersion)
			return nil, nil
		}
	}
	// Same filter as Save: drop any non-atelier entries on read so a
	// cache poisoned by older code paths (pre-scope-fix, or test
	// seeds) doesn't keep resurrecting random sessions on restore.
	s.Workspaces = filterAtelierManaged(s.Workspaces)
	// Canonicalize keys on read so legacy rows persisted with a raw
	// dotted/colon slug (e.g. "owner/repo.dk") match the mangled name
	// tmux and the pickers use ("owner/repo_dk"). Migrates in place —
	// the next Save rewrites the file in canonical form.
	for i := range s.Workspaces {
		s.Workspaces[i].SessionName = CanonicalSessionName(s.Workspaces[i].SessionName)
	}
	s.LastActiveSession = CanonicalSessionName(s.LastActiveSession)
	debuglog.Logf("statestore.Load: path=%s workspaces=%d sessions=%v last_active=%q",
		path, len(s.Workspaces), sessionNames(s.Workspaces), s.LastActiveSession)
	return &s, nil
}

// filterAtelierManaged returns only workspaces carrying workspace identity —
// the atelier-managed scope. A leaked non-atelier session (a claude hook
// firing in some random tmux session, a manual seed) has no id/title/intent/
// root/repo/worktrees and is dropped silently, so restore never resurrects it.
func filterAtelierManaged(ws []Workspace) []Workspace {
	out := ws[:0]
	for _, w := range ws {
		if !w.managed() {
			continue
		}
		out = append(out, w)
	}
	return out
}

// managed reports whether a workspace record carries any workspace identity.
// The SessionName alone doesn't count (a leaked record has one); it needs a
// real marker atelier stamps — a slug-derived title/intent/root, a repo hint,
// or materialized worktrees.
func (w *Workspace) managed() bool {
	return w.Title != "" || w.Intent != "" || w.Root != "" ||
		w.RepoPath != "" || len(w.Worktrees) > 0
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
// `mutate`. Used when registering a fresh workspace (set Title, Intent, Root).
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
	session = CanonicalSessionName(session)
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

// AddAIUsage folds one metered AI call into the cumulative counters, under
// the write lock like every other mutator. task ("recap" | "naming" | …) is
// tallied separately so `atelier ai usage` can attribute burn; nowTS stamps
// SinceTS on the first-ever call. A zero-value delta no-ops.
func AddAIUsage(task string, d AIUsageCounts, nowTS int64) error {
	if (d == AIUsageCounts{}) {
		return nil
	}
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			s = &State{}
		}
		if s.AIUsage == nil {
			s.AIUsage = &AIUsage{SinceTS: nowTS}
		}
		if s.AIUsage.SinceTS == 0 {
			s.AIUsage.SinceTS = nowTS
		}
		s.AIUsage.Total.add(d)
		if task != "" {
			if s.AIUsage.ByTask == nil {
				s.AIUsage.ByTask = map[string]AIUsageCounts{}
			}
			c := s.AIUsage.ByTask[task]
			c.add(d)
			s.AIUsage.ByTask[task] = c
		}
		return Save(s)
	})
}

// ResetAIUsage zeroes the AI usage counters and restarts the measurement
// window at nowTS. Used by `atelier ai usage --reset` to measure burn over a
// fresh interval.
func ResetAIUsage(nowTS int64) error {
	return withWriteLock(Path(), func() error {
		s, err := Load()
		if err != nil {
			return err
		}
		if s == nil {
			s = &State{}
		}
		s.AIUsage = &AIUsage{SinceTS: nowTS}
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
	sessionName = CanonicalSessionName(sessionName)
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
	sessionName = CanonicalSessionName(sessionName)
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
	sessionName = CanonicalSessionName(sessionName)
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
	sessionName = CanonicalSessionName(sessionName)
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

// FindWorkspace returns the Workspace record for a session name, or nil.
func (s *State) FindWorkspace(sessionName string) *Workspace {
	if s == nil {
		return nil
	}
	sessionName = CanonicalSessionName(sessionName)
	for i := range s.Workspaces {
		if s.Workspaces[i].SessionName == sessionName {
			return &s.Workspaces[i]
		}
	}
	return nil
}

func findOrAppendWorkspace(s *State, sessionName string) *Workspace {
	sessionName = CanonicalSessionName(sessionName)
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
