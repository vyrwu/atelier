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
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

const (
	// Session-scoped options.
	OptRepoPath = "@repo_path"

	// Window-scoped options — attention + recap (FR-5.1 / 5.2).
	OptAttention = "@needs_attention"
	OptRecap     = "@attention_recap"
	// OptRecapTs is the unix-epoch second when the current @attention_recap
	// was written. Used by the session picker (FR-2.2) to render "· 30s"
	// freshness suffix alongside the recap line.
	OptRecapTs = "@attention_recap_ts"

	// OptCreatedTs is the unix-epoch second the workspace window was first
	// created. Stamped once at creation (StampCreatedTs) and never mutated
	// on reopen/restore, so the picker's Age sort reads a true "how old is
	// this workspace" signal for GC decisions — NOT a last-touched clock.
	OptCreatedTs = "@created_ts"

	// Window-scoped options — async pull freshness (FR-7).
	//   OptWorkspaceFreshnessTs — unix epoch of the most recent successful fetch.
	//   OptWorkspaceBehind      — count of commits in origin/<default> not in this branch.
	//   OptWorkspaceAhead       — count of commits in this branch not in origin/<default>.
	//   OptWorkspacePullError   — short error message from the last failed _bg-pull.
	//
	// Set by `atelier tools workspaces _bg-pull` after every workspace
	// open. Read by the status-line freshness segment and (later) by
	// the picker freshness column.
	OptWorkspaceFreshnessTs = "@workspace_freshness_ts"
	OptWorkspaceBehind      = "@workspace_behind"
	OptWorkspaceAhead       = "@workspace_ahead"
	OptWorkspacePullError   = "@workspace_pull_error"

	// OptWorkspaceTag is a user-assigned label that groups workspaces
	// across repos/branches (client, initiative, theme). At most one tag
	// per window. The tmux window option is the source of truth (survives
	// session restarts); its value is mirrored to the statestore under
	// TagMetadataKey so restore re-stamps it. The picker derives the tag's
	// color from its name (stable hash → palette) — no color is persisted.
	OptWorkspaceTag = "@workspace_tag"

	// AtelierSessionPrefix marks sessions atelier manages as popups; these
	// are filtered out of workspace listings.
	AtelierSessionPrefix = "_atelier_"
)

// TagMetadataKey is the statestore Metadata key that mirrors
// OptWorkspaceTag. Restore maps it back to the tmux option via
// statestore.MetadataKeyToOptionName ("workspace.tag" → "@workspace_tag").
const TagMetadataKey = "workspace.tag"

// Workspace is a tmux window + cwd + derived/persisted metadata.
type Workspace struct {
	PaneID    string `json:"pane_id"`
	SessionID string `json:"session_id"`
	WindowID  string `json:"window_id"`
	Session   string `json:"session"` // session name
	Name      string `json:"name"`    // window name
	Cwd       string `json:"cwd"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Attention bool   `json:"attention,omitempty"`
	Recap     string `json:"recap,omitempty"`
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

// SetRecap writes a short recap string to the workspace's window. Stamps
// @attention_recap_ts (unix epoch) alongside so the session picker can
// show freshness ("· 30s" / "· 2h"). Clearing the recap also clears ts.
// Mirrors recap + ts to statestore so they survive tmux server restart.
func SetRecap(h *tmuxhost.Client, windowID, recap string) error {
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
	ts := time.Now().Unix()
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

// SetTag assigns (or clears, when tag == "") the workspace tag on
// windowID's window and mirrors it to the statestore so it survives a
// tmux server restart. One tag per window: setting a new value replaces
// the previous one. Mirrored via the generic metadata bag under
// TagMetadataKey — restore re-stamps @workspace_tag from it.
func SetTag(h *tmuxhost.Client, windowID, tag string) error {
	if tag == "" {
		if err := h.UnsetWindowOption(windowID, OptWorkspaceTag); err != nil {
			return err
		}
	} else {
		if err := h.SetWindowOption(windowID, OptWorkspaceTag, tag); err != nil {
			return err
		}
	}
	// Best-effort cache mirror (same discipline as SetRecap/stampForge):
	// losing this write only costs the tag on the next tmux restart.
	_ = PersistWindowMetadata(h, windowID, TagMetadataKey, tag)
	return nil
}

// persistWindowOption resolves windowID → (session_name, window_name)
// and mutates the cached Window record. Best-effort: failures only log
// to debug — losing a single persistence write does not justify
// failing the user-visible operation.
//
// Scoped to atelier-managed sessions: if the session does not have
// @repo_path or @ai_workspace_kind stamped, we skip the cache
// write. Without this, claude's notify-attention hook firing inside
// a random user-created tmux session (no atelier metadata) would leak
// that session into the cache, and restore would resurrect it on
// every tmux start.
func persistWindowOption(h *tmuxhost.Client, windowID string, mutate func(*statestore.Window)) {
	session, window, err := resolveWindowIdentity(h, windowID)
	if err != nil || session == "" || window == "" {
		return
	}
	repoPath, kind := sessionAtelierScope(h, session)
	if repoPath == "" && kind == "" {
		return // not atelier-managed; do not pollute the cache
	}
	// Backfill the workspace's scope alongside the window mutation so
	// the resulting record survives the load/save filter (which drops
	// workspaces with neither RepoPath nor Kind).
	_ = statestore.UpdateWorkspace(session, func(ws *statestore.Workspace) {
		if ws.RepoPath == "" {
			ws.RepoPath = repoPath
		}
		if ws.Kind == "" {
			ws.Kind = kind
		}
	})
	_ = statestore.UpdateWindow(session, window, mutate)
}

// sessionAtelierScope reads the session's atelier metadata. Returns
// (repoPath, kind) — at least one must be non-empty for the session
// to be considered atelier-managed. The caller uses these to backfill
// the cache record's scope so it doesn't get filtered out.
func sessionAtelierScope(h *tmuxhost.Client, sessionName string) (repoPath, kind string) {
	if out, err := h.Run("show-option", "-t", sessionName, "-qv", OptRepoPath); err == nil {
		repoPath = strings.TrimSpace(string(out))
	}
	// @ai_workspace_kind is a WINDOW option (plugin metadata, AI namespace)
	// set on window 1 of multi-repo sessions. Sample that window.
	//
	// TODO(plugins-refactor): this is the last remaining plugin-namespace
	// leak in core's "is this atelier-managed?" detection. When the
	// workspaces tool moves into its own module (task #75), the right
	// cut is a generic `@atelier_session=1` marker stamped at session
	// creation by core, removing the need to peek at any plugin
	// namespace from here.
	if out, err := h.Run("show-window-options", "-v", "-t", sessionName+":1",
		statestore.MetadataKeyToOptionName("ai.workspace_kind")); err == nil {
		kind = strings.TrimSpace(string(out))
	}
	return repoPath, kind
}

// sessionIsAtelierManaged is a thin convenience over sessionAtelierScope.
func sessionIsAtelierManaged(h *tmuxhost.Client, sessionName string) bool {
	repoPath, kind := sessionAtelierScope(h, sessionName)
	return repoPath != "" || kind != ""
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
	Session    string // tmux session name (the persistence key)
	RepoPath   string // repo root; empty for multi-repo sessions
	Kind       string // "worktree" | "multi-repo"
	WindowName string // tmux window name (the per-window key)
	Cwd        string // worktree path the window opened at
	Branch     string // informational
	// CreatedTs is the unix-epoch second the window was created. Zero =
	// unknown; RegisterCreatedWorkspace defaults it to now. Mirrored to
	// the statestore so restore re-stamps @created_ts and the Age sort
	// survives a tmux restart.
	CreatedTs int64
	// Metadata is plugin-namespaced window state to persist alongside
	// the window — e.g. {"ai.prompt": "build foo", "ai.workspace_kind": "worktree"}.
	// Empty/nil = no plugin metadata.
	Metadata map[string]string
}

// StampCreatedTs sets @created_ts on the target window if not already set,
// returning the effective creation timestamp (the existing value when
// present, else now). Idempotent: reopening or rebuilding a workspace
// preserves its original age, so the picker's Age sort stays a true
// "how old is this workspace" GC signal. windowTarget is a tmux window id
// (`@N`) or a `=session:window` reference. Best-effort — the returned
// timestamp is used for the statestore mirror regardless of a write error.
func StampCreatedTs(h *tmuxhost.Client, windowTarget string) int64 {
	if v := readCreatedTs(h, windowTarget); v > 0 {
		return v
	}
	now := time.Now().Unix()
	if _, err := h.Run("set-option", "-w", "-t", windowTarget,
		OptCreatedTs, strconv.FormatInt(now, 10)); err != nil {
		debuglog.LogErr("workspace.StampCreatedTs set @created_ts", err)
	}
	debuglog.Logf("workspace.StampCreatedTs: window=%s created=%d", windowTarget, now)
	return now
}

func readCreatedTs(h *tmuxhost.Client, windowTarget string) int64 {
	out, err := h.Run("show-option", "-w", "-t", windowTarget, "-qv", OptCreatedTs)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return v
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
	createdTs := info.CreatedTs
	if createdTs == 0 {
		createdTs = time.Now().Unix()
	}
	_ = statestore.UpdateWorkspace(info.Session, func(ws *statestore.Workspace) {
		if info.RepoPath != "" {
			ws.RepoPath = info.RepoPath
		}
		if info.Kind != "" {
			ws.Kind = info.Kind
		}
	})
	_ = statestore.UpdateWindow(info.Session, info.WindowName, func(w *statestore.Window) {
		w.Cwd = info.Cwd
		w.Branch = info.Branch
		// Seed CreatedTs at creation time, only when unset — don't
		// overwrite the original age when a later flow re-registers the
		// same window (reopen, restamp). The picker's Age sort is a
		// "how old is this workspace" GC signal, not a last-touched clock.
		if w.CreatedTs == 0 {
			w.CreatedTs = createdTs
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
