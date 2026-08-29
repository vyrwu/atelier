// Package workspace defines the workspace primitive: a tmux window + its
// cwd + per-window metadata. Workspaces are first-class in atelier — the
// core understands them directly so tools can ask "where am I?" via
// `atelier workspace info` without depending on any specific tool.
//
// Note this primitive is intentionally narrow. The opinionated UX around
// workspaces (fzf-pick a repo, create a git worktree, clone-from-URL,
// session picker with attention sorting) lives in the workspaces *tool*,
// not here.
package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

const (
	// Session-scoped workspace-identity options (the intent-workspace model).
	// A workspace IS a tmux session; these stamp what it means.
	//
	//   OptWorkspaceID     — the stable slug; equals the session name. Its
	//                        presence is THE marker that a session is an
	//                        atelier-managed workspace (see Listable). Every
	//                        atelier flow stamps it at creation.
	//   OptWorkspaceTitle  — the human, renameable label (M-r edits this).
	//   OptWorkspaceIntent — the free-text task the user typed at M-n.
	//   OptWorkspaceRoot   — the workspace's dedicated dir (~/ateliers/<slug>)
	//                        that worktrees symlink into and the agent runs from.
	//
	// They resolve when read at window scope too (tmux option inheritance:
	// window → session → global), so the picker/topology read `#{@workspace_id}`
	// per window and get the session's value.
	OptWorkspaceID     = "@workspace_id"
	OptWorkspaceTitle  = "@workspace_title"
	OptWorkspaceIntent = "@workspace_intent"
	OptWorkspaceRoot   = "@workspace_root"

	// OptWorkspaceDriver marks the ONE driver-agent window in a workspace
	// (window-scoped, "1"). The picker groups by session and renders the
	// driver's attention/recap; the refresh loop writes attention only on the
	// driver; the `multiple_drivers` invariant counts these. Inspection shells
	// the user opens in the same session don't carry it, so they never count
	// as a second agent or steal the workspace's attention slot.
	OptWorkspaceDriver = "@workspace_driver"

	// OptRepoPath is an optional single-repo hint (session-scoped). A workspace
	// spans repos, so this is no longer identity — some flows read it to locate
	// a repo for a one-repo workspace.
	OptRepoPath = "@repo_path"

	// Window-scoped options — attention + recap (FR-5.1 / 5.2).
	OptAttention = "@needs_attention"
	OptRecap     = "@attention_recap"
	// OptRecapTs is the unix-epoch second when the current @attention_recap
	// was written. Used by the session picker (FR-2.2) to render "· 30s"
	// freshness suffix alongside the recap line.
	OptRecapTs = "@attention_recap_ts"

	// OptAgentStatus is the 3-state agent status the picker renders as a
	// colored dot: AgentBlocked (yellow — waiting on you), AgentRunning (blue —
	// actively working / waiting on its sub-agent), or unset/AgentIdle (no dot).
	// Ephemeral: written by the refresh loop from the transcript classification,
	// NOT persisted — a restart must not resurrect a stale "running".
	OptAgentStatus = "@agent_status"

	// OptWorkspaceCreatedTs is the unix epoch (seconds) when the workspace
	// window was first created. Stamped by the lifecycle primitive at
	// creation time, persisted in the statestore, and re-stamped by
	// restore so the M-s picker's age column shows actual workspace age
	// rather than recency-of-visit.
	OptWorkspaceCreatedTs = "@workspace_created_ts"

	// OptWorkspaceTag is a user-assigned label that groups workspaces
	// across repos/branches (client, initiative, theme). At most one tag
	// per window. The tmux window option is the source of truth (survives
	// session restarts); its value is mirrored to the statestore under
	// TagMetadataKey so restore re-stamps it. The picker derives the tag's
	// color from its name (stable hash → palette) — no color is persisted.
	OptWorkspaceTag = "@workspace_tag"

	// OptScopePin is the M-s picker's "pinned scope" — a search-query
	// prefix the user locks (M-p) so the picker opens pre-filtered to a
	// focused context (one repo, one tag) instead of an empty query.
	//
	// Deliberately a tmux GLOBAL option and NOT mirrored to the
	// statestore: the pin is session-lived — it lives exactly as long as
	// the tmux server (the atelier session) and MUST NOT survive a full
	// atelier restart. `atelier stop` kills the server and drops it, with
	// no persistence/restore code to maintain. See GetScopePin /
	// SetScopePin.
	OptScopePin = "@atelier_scope_pin"

	// AtelierSessionPrefix marks sessions atelier manages as popups; these
	// are filtered out of workspace listings.
	AtelierSessionPrefix = "_atelier_"
)

// TagMetadataKey is the statestore Metadata key that mirrors
// OptWorkspaceTag. Restore maps it back to the tmux option via
// statestore.MetadataKeyToOptionName ("workspace.tag" → "@workspace_tag").
const TagMetadataKey = "workspace.tag"

// Workspace is a tmux window + cwd + derived/persisted metadata. In the
// intent-workspace model the session-scoped identity fields (ID/Title/Intent/
// Root) describe the workspace the window belongs to; the window fields
// (Name/Cwd/…) describe the individual window (driver agent or shell).
type Workspace struct {
	PaneID    string `json:"pane_id"`
	SessionID string `json:"session_id"`
	WindowID  string `json:"window_id"`
	Session   string `json:"session"` // session name (== workspace id/slug)
	Name      string `json:"name"`    // window name
	Cwd       string `json:"cwd"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Attention bool   `json:"attention,omitempty"`
	Recap     string `json:"recap,omitempty"`

	// Workspace-identity (session-scoped) fields.
	ID     string `json:"id,omitempty"`     // @workspace_id
	Title  string `json:"title,omitempty"`  // @workspace_title
	Intent string `json:"intent,omitempty"` // @workspace_intent
	Root   string `json:"root,omitempty"`   // @workspace_root
}

// AsJSON renders the workspace as JSON for `atelier workspace info`.
func (w *Workspace) AsJSON() ([]byte, error) {
	return json.MarshalIndent(w, "", "  ")
}

// Target returns a tmux target spec for the workspace (session:window).
func (w *Workspace) Target() string {
	return fmt.Sprintf("%s:%s", w.SessionID, w.WindowID)
}

// List returns every tmux window across all sessions, filtering out
// atelier-managed popup sessions.
func List(h *tmuxhost.Client) ([]Workspace, error) {
	out, err := h.Run("list-windows", "-a",
		"-F", joinFields(
			"#{pane_id}", "#{session_id}", "#{window_id}",
			"#{session_name}", "#{window_name}", "#{pane_current_path}",
		))
	if err != nil {
		return nil, err
	}
	var workspaces []Workspace
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		w, ok := parseWorkspaceLine(line)
		if !ok {
			continue
		}
		if strings.HasPrefix(w.Session, AtelierSessionPrefix) {
			continue
		}
		enrichWithGit(&w)
		enrichWithMetadata(h, &w)
		workspaces = append(workspaces, w)
	}
	return workspaces, nil
}

// Info returns the workspace for the given pane (or the current pane if "").
func Info(h *tmuxhost.Client, paneID string) (*Workspace, error) {
	args := []string{"display-message", "-p"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
	args = append(args, joinFields(
		"#{pane_id}", "#{session_id}", "#{window_id}",
		"#{session_name}", "#{window_name}", "#{pane_current_path}",
	))
	out, err := h.Run(args...)
	if err != nil {
		return nil, err
	}
	w, ok := parseWorkspaceLine(strings.TrimSpace(string(out)))
	if !ok {
		return nil, fmt.Errorf("display-message returned unparseable output: %q", out)
	}
	if strings.HasPrefix(w.Session, AtelierSessionPrefix) {
		return nil, fmt.Errorf("pane %s is in an atelier popup, not a workspace", w.PaneID)
	}
	enrichWithGit(&w)
	enrichWithMetadata(h, &w)
	return &w, nil
}

// Create opens a new tmux window at dir, named name, in the given session
// (or the current session if sessionTarget is empty).
func Create(h *tmuxhost.Client, dir, name, sessionTarget string) error {
	args := []string{"new-window"}
	if sessionTarget != "" {
		args = append(args, "-t", sessionTarget)
	}
	args = append(args, "-n", name, "-c", dir)
	_, err := h.Run(args...)
	return err
}

// Switch switches the active client to the given target (session:window).
func Switch(h *tmuxhost.Client, target string) error {
	_, err := h.Run("switch-client", "-t", target)
	return err
}

// Delete kills the window containing the given pane (or current pane if "").
func Delete(h *tmuxhost.Client, paneID string) error {
	args := []string{"kill-window"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
	_, err := h.Run(args...)
	return err
}

// SetAttention raises the @needs_attention flag on the workspace's window
// AND mirrors it to the statestore cache so the flag survives tmux server
// restarts (FR-5.2 + the broader persistence story).
func SetAttention(h *tmuxhost.Client, windowID string, on bool) error {
	if on {
		if err := h.SetWindowOption(windowID, OptAttention, "1"); err != nil {
			return err
		}
	} else {
		if err := h.UnsetWindowOption(windowID, OptAttention); err != nil {
			return err
		}
	}
	persistWindowOption(h, windowID, func(w *statestore.Window) {
		w.Attention = on
	})
	return nil
}

// Agent status values for OptAgentStatus (and the AI adapter's classification).
const (
	AgentBlocked = "blocked" // waiting on the user — raises attention
	AgentRunning = "running" // actively working / waiting on a sub-agent
	AgentIdle    = "idle"    // finished or nothing to do — no dot
)

// SetAgentStatus records the 3-state agent status for the picker's colored dot
// and derives @needs_attention from it — only AgentBlocked needs the user, so
// only it feeds the status-line ⏺ rollup and the clear-on-visit hook. AgentIdle
// (or "") clears the dot. @agent_status itself is deliberately NOT persisted;
// the refresh loop re-derives it, so a restart never shows a stale "running".
func SetAgentStatus(h *tmuxhost.Client, windowID, status string) error {
	if status == "" || status == AgentIdle {
		_ = h.UnsetWindowOption(windowID, OptAgentStatus)
	} else if err := h.SetWindowOption(windowID, OptAgentStatus, status); err != nil {
		return err
	}
	return SetAttention(h, windowID, status == AgentBlocked)
}

// SetRecap writes a short recap string to the workspace's window, stamped with
// the current time. See SetRecapTS.
func SetRecap(h *tmuxhost.Client, windowID, recap string) error {
	return SetRecapTS(h, windowID, recap, time.Now().Unix())
}

// SetRecapTS writes a recap and stamps @attention_recap_ts to the given unix
// epoch. The refresh loop passes the source transcript's mtime so the timestamp
// keys the "already summarized this state" throttle on transcript change (and
// the picker's "· 30s" age reflects when the agent last did something, not when
// we happened to summarize). Clearing the recap also clears ts. Mirrors recap +
// ts to statestore so they survive tmux server restart.
func SetRecapTS(h *tmuxhost.Client, windowID, recap string, ts int64) error {
	if recap == "" {
		_ = h.UnsetWindowOption(windowID, OptRecapTs)
		if err := h.UnsetWindowOption(windowID, OptRecap); err != nil {
			return err
		}
		persistWindowOption(h, windowID, func(w *statestore.Window) {
			w.Recap = ""
			w.RecapTs = 0
		})
		return nil
	}
	if err := h.SetWindowOption(windowID, OptRecap, recap); err != nil {
		return err
	}
	if err := h.SetWindowOption(windowID, OptRecapTs, strconv.FormatInt(ts, 10)); err != nil {
		return err
	}
	persistWindowOption(h, windowID, func(w *statestore.Window) {
		w.Recap = recap
		w.RecapTs = ts
	})
	return nil
}

// Listable reports whether a window belongs to an atelier-managed workspace:
// its session carries a @workspace_id. Windows without it are raw tmux windows
// or spent popups.
//
// This is the single inclusion predicate shared by the M-s picker (which lists
// workspaces) and the status-line attention rollup (which counts them).
// Keeping ONE predicate is load-bearing: when the picker and the rollup
// diverged, a workspace could count toward the ⏺ badge yet never appear in the
// picker — a phantom "notification with no workspace". Delegates to
// state.Listable so the picker, the rollup, and the invariants can't disagree.
func Listable(workspaceID string) bool {
	return state.Listable(workspaceID)
}

// SetTag assigns (or clears, when tag == "") a workspace's grouping tag. The
// tag is a WORKSPACE-level (session-scoped) label now — one tag per workspace,
// resolved at window scope via tmux option inheritance so the picker reads it
// per row. Mirrored to the statestore Workspace so it survives a tmux restart.
func SetTag(h *tmuxhost.Client, session, tag string) error {
	if tag == "" {
		if _, err := h.Run("set-option", "-t", session, "-u", OptWorkspaceTag); err != nil {
			return err
		}
	} else if _, err := h.Run("set-option", "-t", session, OptWorkspaceTag, tag); err != nil {
		return err
	}
	// Best-effort cache mirror (losing it only costs the tag on next restart).
	_ = statestore.UpdateWorkspace(session, func(ws *statestore.Workspace) { ws.Tag = tag })
	return nil
}

// StampWorkspaceIdentity writes the workspace-identity options (id, title,
// intent, root) on the SESSION and mirrors title/intent/root into the
// statestore so a rename or restore survives a tmux server restart. The id
// equals the session name and is the marker Listable keys on. Empty title
// falls back to the id at render time; empty intent/root are legitimate
// (a workspace with no task text yet, or a not-yet-materialized root).
func StampWorkspaceIdentity(h *tmuxhost.Client, session, id, title, intent, root string) error {
	if id == "" {
		return fmt.Errorf("workspace.StampWorkspaceIdentity: id required")
	}
	if _, err := h.Run("set-option", "-t", session, OptWorkspaceID, id); err != nil {
		return err
	}
	set := func(opt, val string) {
		if val == "" {
			return
		}
		if _, err := h.Run("set-option", "-t", session, opt, val); err != nil {
			debuglog.LogErr("workspace.StampWorkspaceIdentity "+opt, err)
		}
	}
	set(OptWorkspaceTitle, title)
	set(OptWorkspaceIntent, intent)
	set(OptWorkspaceRoot, root)
	_ = statestore.UpdateWorkspace(session, func(ws *statestore.Workspace) {
		if title != "" {
			ws.Title = title
		}
		if intent != "" {
			ws.Intent = intent
		}
		if root != "" {
			ws.Root = root
		}
	})
	return nil
}

// SetTitle renames a workspace's human label (the M-r rename). The session
// NAME — the tmux target every switch/kill depends on — is untouched; only
// @workspace_title and the cached Title move. Empty title clears the option
// so the picker falls back to the id/slug.
func SetTitle(h *tmuxhost.Client, session, title string) error {
	if title == "" {
		if _, err := h.Run("set-option", "-t", session, "-u", OptWorkspaceTitle); err != nil {
			return err
		}
	} else if _, err := h.Run("set-option", "-t", session, OptWorkspaceTitle, title); err != nil {
		return err
	}
	_ = statestore.UpdateWorkspace(session, func(ws *statestore.Workspace) { ws.Title = title })
	return nil
}

// GetScopePin returns the M-s picker's pinned scope query, or "" when no
// scope is pinned. Read on every picker open to pre-seed the query and
// light the "Pinned" footer badge. See OptScopePin.
func GetScopePin(h *tmuxhost.Client) string {
	pin, _ := h.ShowGlobalOption(OptScopePin)
	return pin
}

// SetScopePin pins (or clears, when query == "") the M-s picker scope.
// Stored as a tmux global option only — session-lived by construction,
// never mirrored to the statestore, so it evaporates on `atelier stop`
// (kill-server) rather than surviving a full atelier restart. See
// OptScopePin.
func SetScopePin(h *tmuxhost.Client, query string) error {
	if query == "" {
		return h.UnsetGlobalOption(OptScopePin)
	}
	return h.SetGlobalOption(OptScopePin, query)
}

// persistWindowOption resolves windowID → (session_name, window_name)
// and mutates the cached Window record. Best-effort: failures only log
// to debug — losing a single persistence write does not justify
// failing the user-visible operation.
//
// Scoped to atelier-managed sessions: if the session does not carry a
// @workspace_id, we skip the cache write. Without this, an agent hook
// firing inside a random user-created tmux session (no atelier metadata)
// would leak that session into the cache, and restore would resurrect it
// on every tmux start.
func persistWindowOption(h *tmuxhost.Client, windowID string, mutate func(*statestore.Window)) {
	session, window, err := resolveWindowIdentity(h, windowID)
	if err != nil || session == "" || window == "" {
		return
	}
	id, title, intent, root := sessionWorkspaceIdentity(h, session)
	if id == "" {
		return // not atelier-managed; do not pollute the cache
	}
	// A session stamped with @workspace_id but not yet a title/intent/root (a
	// launcher heal, or a write-through that races creation) would otherwise be
	// dropped by the load/save filter (statestore.managed() ignores the id).
	// Seed a fallback Title from the id so the record — and this write-through —
	// survives. Only seed empty fields; never clobber a title the user renamed.
	if title == "" {
		title = id
	}
	// Backfill the workspace identity alongside the window mutation.
	_ = statestore.UpdateWorkspace(session, func(ws *statestore.Workspace) {
		if ws.Title == "" {
			ws.Title = title
		}
		if ws.Intent == "" {
			ws.Intent = intent
		}
		if ws.Root == "" {
			ws.Root = root
		}
	})
	_ = statestore.UpdateWindow(session, window, mutate)
}

// sessionWorkspaceIdentity reads a session's workspace-identity options.
// A non-empty id marks the session as an atelier-managed workspace.
func sessionWorkspaceIdentity(h *tmuxhost.Client, sessionName string) (id, title, intent, root string) {
	get := func(opt string) string {
		out, err := h.Run("show-option", "-t", sessionName, "-qv", opt)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get(OptWorkspaceID), get(OptWorkspaceTitle), get(OptWorkspaceIntent), get(OptWorkspaceRoot)
}

// sessionIsAtelierManaged reports whether a session carries a @workspace_id.
func sessionIsAtelierManaged(h *tmuxhost.Client, sessionName string) bool {
	id, _, _, _ := sessionWorkspaceIdentity(h, sessionName)
	return id != ""
}

// SetPersistedGlobal sets a tmux global option AND mirrors the value
// to the on-disk statestore in one call. Used by tools whose
// active-context state (k8s active context, pg active endpoint) needs
// to survive tmux server restarts.
//
// Pre-extraction, k8s and pg each had two-line set-then-mirror
// sequences inline — a copy-paste pattern that risked one half being
// forgotten when a new tool was added (tmux side present, cache side
// missing = silent persistence gap on restart).
//
// Passing value="" deletes both the tmux global AND the cached entry.
func SetPersistedGlobal(h *tmuxhost.Client, key, value string) error {
	if value == "" {
		if err := h.UnsetGlobalOption(key); err != nil {
			return err
		}
	} else {
		if err := h.SetGlobalOption(key, value); err != nil {
			return err
		}
	}
	// Cache mirror is best-effort — losing a single statestore write is
	// at worst "this tool's context doesn't restore after the next tmux
	// crash" — not enough to fail the user-visible operation.
	_ = statestore.UpdateGlobal(key, value)
	return nil
}

// PersistWindowMetadata mirrors a plugin-namespaced metadata entry
// into the statestore cache so restore can later re-stamp it as a
// tmux window option. Best-effort. Skipped silently if the host
// session is not atelier-managed (the same tool can fire from any
// session's popup, not just atelier-created ones).
//
// Key follows the `<plugin>.<field>` convention (e.g. `ai.active_session_id`).
// Core never inspects the key contents — plugins own their namespaces.
func PersistWindowMetadata(h *tmuxhost.Client, windowID, key, value string) error {
	session, window, err := resolveWindowIdentity(h, windowID)
	if err != nil || session == "" || window == "" {
		return err
	}
	if !sessionIsAtelierManaged(h, session) {
		return nil
	}
	return statestore.UpdateWindow(session, window, func(w *statestore.Window) {
		if w.Metadata == nil {
			w.Metadata = map[string]string{}
		}
		w.Metadata[key] = value
	})
}

// NewWorkspaceInfo captures everything statestore needs to persist a
// freshly-created workspace + window so the resulting cache entry can
// be restored verbatim after a tmux server restart.
//
// Plugin-specific window state goes in Metadata under the `<plugin>.<field>`
// convention; restore re-stamps every entry as a tmux window option
// `@<plugin>_<field>` on rehydrate.
type NewWorkspaceInfo struct {
	Session    string // tmux session name == workspace id/slug (persistence key)
	Title      string // human, renameable label
	Intent     string // the free-text task the user typed at M-n
	Root       string // ~/ateliers/<slug> — the workspace's dedicated dir
	RepoPath   string // optional single-repo hint
	WindowName string // tmux window name (the per-window key)
	Cwd        string // the driver window's cwd (the workspace root)
	Branch     string // informational
	// Metadata is plugin-namespaced window state to persist alongside
	// the window — e.g. {"ai.prompt": "build foo"}. Empty/nil = none.
	Metadata map[string]string
}

// RegisterCreatedWorkspace mirrors a freshly-created workspace + window
// into the statestore cache. Call at the END of any workspace-creation
// flow so the cache reflects the as-built state including all stamped
// metadata.
//
// Best-effort: a statestore write failure is logged via debuglog
// (statestore handles that internally) but never aborts the creation.
// The cost of losing one cache write is at most "this workspace
// doesn't restore after the next tmux crash" — annoying, not broken.
func RegisterCreatedWorkspace(info NewWorkspaceInfo) {
	now := time.Now().Unix()
	_ = statestore.UpdateWorkspace(info.Session, func(ws *statestore.Workspace) {
		if info.Title != "" {
			ws.Title = info.Title
		}
		if info.Intent != "" {
			ws.Intent = info.Intent
		}
		if info.Root != "" {
			ws.Root = info.Root
		}
		if info.RepoPath != "" {
			ws.RepoPath = info.RepoPath
		}
		// Seed CreatedAt at creation time. Without this, a workspace
		// the user creates and then M-q's WITHOUT switching away
		// would have no created_at in the cache.
		// On next launch the picker would show no age for it,
		// confusing the user into thinking persistence is broken.
		//
		// Only seed when unset — don't overwrite the original creation
		// timestamp if RegisterCreatedWorkspace is called again later.
		if ws.CreatedAt == 0 {
			ws.CreatedAt = now
		}
	})
	_ = statestore.UpdateWindow(info.Session, info.WindowName, func(w *statestore.Window) {
		w.Cwd = info.Cwd
		w.Branch = info.Branch
		// Mirror the per-window @workspace_created_ts (stamped in tmux by
		// the creation flow) so the picker's age column survives restart
		// for THIS window, not just the session's first. Only seed when
		// unset — don't clobber the original creation time on re-register.
		if w.CreatedAt == 0 {
			w.CreatedAt = now
		}
		if len(info.Metadata) > 0 {
			if w.Metadata == nil {
				w.Metadata = map[string]string{}
			}
			for k, v := range info.Metadata {
				w.Metadata[k] = v
			}
		}
	})
}

// resolveWindowIdentity returns the persistent (session_name, window_name)
// for a tmux window ID. The statestore keys on names, not @IDs, because
// IDs get reassigned on every tmux server restart.
func resolveWindowIdentity(h *tmuxhost.Client, windowID string) (sessionName, windowName string, err error) {
	out, err := h.DisplayMessageAt(windowID, "#{session_name}|#{window_name}")
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("display-message returned unexpected output: %q", out)
	}
	return parts[0], parts[1], nil
}

func parseWorkspaceLine(line string) (Workspace, bool) {
	fields := strings.SplitN(line, "\t", 6)
	if len(fields) < 6 {
		return Workspace{}, false
	}
	return Workspace{
		PaneID:    fields[0],
		SessionID: fields[1],
		WindowID:  fields[2],
		Session:   fields[3],
		Name:      fields[4],
		Cwd:       fields[5],
	}, true
}

func joinFields(fields ...string) string {
	return strings.Join(fields, "\t")
}

func enrichWithGit(w *Workspace) {
	if w.Cwd == "" {
		return
	}
	top, err := gitOutput(w.Cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	w.Repo = filepath.Base(top)
	if branch, err := gitOutput(w.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		w.Branch = branch
	}
}

func enrichWithMetadata(h *tmuxhost.Client, w *Workspace) {
	if v, _ := h.GetWindowOption(w.WindowID, OptAttention); v == "1" {
		w.Attention = true
	}
	if v, _ := h.GetWindowOption(w.WindowID, OptRecap); v != "" {
		w.Recap = v
	}
	// The @workspace_* options are SESSION-scoped; they resolve at window scope
	// only through tmux option inheritance, which `display-message '#{@opt}'`
	// honors but `show-window-options -v` (GetWindowOption) does NOT — the
	// latter errors on a session option. Read them via inheritedOption.
	w.ID = inheritedOption(h, w.WindowID, OptWorkspaceID)
	w.Title = inheritedOption(h, w.WindowID, OptWorkspaceTitle)
	w.Intent = inheritedOption(h, w.WindowID, OptWorkspaceIntent)
	w.Root = inheritedOption(h, w.WindowID, OptWorkspaceRoot)
}

// inheritedOption reads an option at a window target with tmux's
// window→session→global inheritance (via display-message), so a SESSION-scoped
// @option stamped on the session resolves when read against one of its windows.
// GetWindowOption cannot do this (show-window-options rejects session options).
// Empty string on any error or unset.
func inheritedOption(h *tmuxhost.Client, windowID, opt string) string {
	out, err := h.DisplayMessageAt(windowID, "#{"+opt+"}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// DefaultBranch returns the repo's default branch: symbolic-ref origin/HEAD
// if available, else main, else master, else "main".
func DefaultBranch(repoPath string) (string, error) {
	out, err := gitOutput(repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		ref := strings.TrimSpace(out)
		if i := strings.Index(ref, "/"); i >= 0 {
			return ref[i+1:], nil
		}
		return ref, nil
	}
	for _, b := range []string{"main", "master"} {
		if _, err := gitOutput(repoPath, "rev-parse", "--verify", b); err == nil {
			return b, nil
		}
	}
	return "main", nil
}

// PullDefault fetches and fast-forwards the default branch on the given
// repo path. Uses `pull --rebase` if currently on the default branch,
// `fetch origin <branch>` otherwise — avoids accidental merges.
func PullDefault(repoPath string) error {
	branch, err := DefaultBranch(repoPath)
	if err != nil {
		return err
	}
	current, err := gitOutput(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	if current == branch {
		return runGit(repoPath, "pull", "--rebase")
	}
	return runGit(repoPath, "fetch", "origin", branch)
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}
