// Package claudeproj locates Claude Code's per-project transcript files.
//
// Claude stores conversation transcripts at
// <config>/projects/<encoded-cwd>/<session-id>.jsonl. Both the claude
// tool (deciding --resume) and the workspaces tool (deciding whether to
// auto-open Claude on recover) need to resolve these, so the logic lives
// here rather than being duplicated or creating a tool→tool import.
package claudeproj

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ProjectsDir returns the directory Claude Code stores per-project
// transcripts under. Honors CLAUDE_CONFIG_DIR (which relocates the whole
// ~/.claude tree, e.g. to ~/.config/claude), falling back to ~/.claude.
// Without the env check, lookups glob an empty path and every session id
// looks stale — so --resume never fires for users who set the env var.
func ProjectsDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// ProjectSlug encodes an absolute cwd into Claude's per-project transcript
// directory name. Claude replaces every "/" AND "." with "-", keeping the
// leading separator (so "/Users/a/code/.worktrees/x" becomes
// "-Users-a-code--worktrees-x"). A prior encoder trimmed the leading slash
// and ignored ".", so it missed every worktree path under ".worktrees" —
// the exact dirs atelier workspaces live in.
func ProjectSlug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// SessionIDFromPath extracts the session UUID from a transcript path like
// <config>/projects/<encoded-cwd>/<uuid>.jsonl. Returns "" if the filename
// isn't a transcript.
func SessionIDFromPath(transcriptPath string) string {
	base := filepath.Base(transcriptPath)
	const suffix = ".jsonl"
	if !strings.HasSuffix(base, suffix) {
		return ""
	}
	return strings.TrimSuffix(base, suffix)
}

// TranscriptExists reports whether Claude has a saved transcript for the
// session id. The encoded cwd isn't known here, so it globs all projects.
func TranscriptExists(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	projects := ProjectsDir()
	if projects == "" {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(projects, "*", sessionID+".jsonl"))
	if err != nil {
		return false
	}
	return len(matches) > 0
}

// LatestTranscriptPath returns the newest .jsonl transcript path for cwd,
// or "" if none.
func LatestTranscriptPath(cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	projects := ProjectsDir()
	if projects == "" {
		return "", nil
	}
	dir := filepath.Join(projects, ProjectSlug(cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var latest os.DirEntry
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if latest == nil {
			latest = e
			continue
		}
		li, _ := latest.Info()
		ei, _ := e.Info()
		if ei.ModTime().After(li.ModTime()) {
			latest = e
		}
	}
	if latest == nil {
		return "", nil
	}
	return filepath.Join(dir, latest.Name()), nil
}

// LatestSessionID returns the session id of the most recent transcript for
// cwd, or "" if none. Ground-truth resume pointer that survives statestore
// pruning (soft-close) and predates atelier tracking a window.
func LatestSessionID(cwd string) string {
	t, _ := LatestTranscriptPath(cwd)
	return SessionIDFromPath(t)
}
