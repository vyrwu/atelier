package statestore

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// migrateFromV2 maps a schema-v2 cache (repo-sessions) into the v3
// intent-workspace shape. Each v2 workspace becomes a v3 workspace whose
// Title is derived from its repo slug (or its `auto/…` session name), whose
// Intent is recovered from the first window's `ai.prompt` metadata, and whose
// Worktrees are reconstructed from the windows that pointed at a branch
// checkout. Root is left empty — it materializes on first use, exactly as
// WS-9 specifies ("do not wipe; root is materialized on first use").
//
// The v2 Window shape is a strict subset of v3's, so windows carry over
// verbatim (recap, attention, ai.active_session_id all preserved). This is the
// plumbing v2's own doc admitted was never written.
func migrateFromV2(data []byte) (*State, error) {
	type v2Workspace struct {
		SessionName string   `json:"session_name"`
		RepoPath    string   `json:"repo_path"`
		Kind        string   `json:"kind"`
		Windows     []Window `json:"windows"`
		CreatedAt   int64    `json:"created_at"`
	}
	type v2State struct {
		SchemaVersion     int               `json:"schema_version"`
		Hostname          string            `json:"hostname"`
		CapturedAt        int64             `json:"captured_at"`
		Workspaces        []v2Workspace     `json:"workspaces"`
		Globals           map[string]string `json:"globals"`
		LastActiveSession string            `json:"last_active_session"`
		AIUsage           *AIUsage          `json:"ai_usage"`
	}
	var old v2State
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}
	s := &State{
		SchemaVersion:     SchemaVersion,
		Hostname:          old.Hostname,
		CapturedAt:        old.CapturedAt,
		Globals:           old.Globals,
		LastActiveSession: old.LastActiveSession,
		AIUsage:           old.AIUsage,
	}
	for _, ow := range old.Workspaces {
		nw := Workspace{
			SessionName: ow.SessionName,
			RepoPath:    ow.RepoPath,
			CreatedAt:   ow.CreatedAt,
			Windows:     ow.Windows,
			Title:       migratedTitle(ow.SessionName, ow.RepoPath),
			Intent:      migratedIntent(ow.Windows),
			Worktrees:   migratedWorktrees(ow.RepoPath, ow.Windows),
		}
		s.Workspaces = append(s.Workspaces, nw)
	}
	return s, nil
}

// migratedTitle derives a human title for a migrated workspace: the repo slug
// ("owner/repo") for a single-repo session, else the session name with any
// "auto/" prefix stripped and hyphens turned to spaces.
func migratedTitle(sessionName, repoPath string) string {
	if repoPath != "" {
		repo := filepath.Base(repoPath)
		owner := filepath.Base(filepath.Dir(repoPath))
		if repo != "" && repo != "." && owner != "" && owner != "." {
			return owner + "/" + repo
		}
	}
	name := strings.TrimPrefix(sessionName, "auto/")
	name = strings.ReplaceAll(name, "-", " ")
	if name == "" {
		return sessionName
	}
	return name
}

// migratedIntent recovers the original task prompt from the first window that
// carries an `ai.prompt` metadata entry. Empty when none did.
func migratedIntent(windows []Window) string {
	for _, w := range windows {
		if p := w.Metadata["ai.prompt"]; p != "" {
			return p
		}
	}
	return ""
}

// migratedWorktrees reconstructs the worktree set from v2 windows: every
// window whose Cwd is a branch checkout (has a Cwd distinct from the repo
// root) becomes a worktree record. Link is left empty — the layout repair
// sweep re-establishes symlinks once a Root exists.
func migratedWorktrees(repoPath string, windows []Window) []Worktree {
	var out []Worktree
	slug := ""
	if repoPath != "" {
		repo := filepath.Base(repoPath)
		owner := filepath.Base(filepath.Dir(repoPath))
		if repo != "" && owner != "" {
			slug = owner + "/" + repo
		}
	}
	for _, w := range windows {
		if w.Cwd == "" || w.Cwd == repoPath {
			continue
		}
		branch := w.Branch
		if branch == "" {
			branch = w.Name
		}
		out = append(out, Worktree{Repo: slug, Branch: branch, Path: w.Cwd})
	}
	return out
}
