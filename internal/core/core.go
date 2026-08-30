// Package core is atelier's domain: the four entities from the design doc
// (Workspace, Worktree, agent status, PR), the paths they live under, and the
// slug derivation. It has no dependencies on tmux, git, or the UI — everything
// else builds on it.
package core

import (
	"crypto/rand"
	"encoding/base32"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PRState / CIState / Review are the closed enums the PR view renders.
type PRState string

const (
	PROpen   PRState = "open"
	PRDraft  PRState = "draft"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed"
)

type CIState string

const (
	CIPass    CIState = "pass"
	CIFail    CIState = "fail"
	CIPending CIState = "pending"
	CINone    CIState = "none"
)

type Review string

const (
	ReviewApproved Review = "approved"
	ReviewChanges  Review = "changes_requested"
	ReviewRequired Review = "review_required"
	ReviewNone     Review = "none"
)

// PR is a GitHub pull request associated with a workspace by head branch.
// It is a cache: ground truth is GitHub (NFR-R2).
type PR struct {
	Repo       string  `json:"repo"`
	Number     int     `json:"number"`
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	State      PRState `json:"state"`
	CI         CIState `json:"ci"`
	Review     Review  `json:"review"`
	Registered bool    `json:"registered,omitempty"`
}

// Workspace is one task: a directory, a title, a tmux session, and an agent.
// Worktrees are NOT stored here — they are derived from the directory on read.
type Workspace struct {
	Slug         string    `json:"slug"`  // immutable; the directory name
	Title        string    `json:"title"` // generated, mutable, display only
	Intent       string    `json:"intent"`
	Created      time.Time `json:"created"`
	Session      string    `json:"session"` // tmux session name
	PRs          []PR      `json:"prs,omitempty"`
	PRsRefreshed time.Time `json:"prs_refreshed,omitempty"`
	// Retired marks a workspace deactivated: its processes are killed but the
	// directory, worktrees, and record are kept so it can be restored. Zero value
	// (false = active), so existing state loads as active with no migration.
	Retired bool `json:"retired,omitempty"`
}

// Active reports whether the workspace is in the working set (not retired).
func (w Workspace) Active() bool { return !w.Retired }

// Root is the workspace directory path (~/ateliers/<slug>).
func (w Workspace) Root() string { return filepath.Join(AteliersRoot(), w.Slug) }

// Worktree is a git worktree belonging to a workspace, derived from the
// filesystem (a `.git` *file* marks it). Never stored.
type Worktree struct {
	Repo   string // "owner/repo" — the directory under the workspace root
	Branch string // the checked-out branch
	Path   string // absolute path to the worktree
}

// AgentStatus is per-session runtime state (NOT persisted in State): the agent
// is working, blocked on you, idle (finished, not waiting), or gone (no live
// process). Only Blocked draws attention (FR-B1).
type AgentStatus string

const (
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusIdle    AgentStatus = "idle"
	StatusGone    AgentStatus = "gone"
)

// --- paths -----------------------------------------------------------------

func home() string { h, _ := os.UserHomeDir(); return h }

func xdg(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return filepath.Join(home(), def)
}

// AteliersRoot is where workspace directories live. $ATELIER_ROOT overrides the
// config/default (~/ateliers). Read from env only here; config wiring sets the
// env before use so this stays dependency-free.
func AteliersRoot() string {
	if v := os.Getenv("ATELIER_ROOT"); v != "" {
		return expand(v)
	}
	return filepath.Join(home(), "ateliers")
}

// StatePath is the single durable state file (XDG_STATE_HOME/atelier/state.json).
func StatePath() string {
	return filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "atelier", "state.json")
}

// ConfigPath is the single config file (XDG_CONFIG_HOME/atelier/config.toml).
func ConfigPath() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "atelier", "config.toml")
}

// WorkspaceGuidePath is the central, user-editable per-workspace instruction
// file. Each new workspace's CLAUDE.md is a symlink to it, so edits here reach
// every workspace at once.
func WorkspaceGuidePath() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "atelier", "WORKSPACE_CLAUDE.md")
}

// CacheDir holds ephemeral runtime state (agent status), never durable.
func CacheDir() string { return filepath.Join(xdg("XDG_CACHE_HOME", ".cache"), "atelier") }

// expand resolves a leading ~ to the home dir.
func expand(p string) string {
	if p == "~" {
		return home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), p[2:])
	}
	return p
}

// --- slug ------------------------------------------------------------------

// Slug derives an immutable directory name from intent text: a few kebab-cased
// words plus a short random suffix for uniqueness. The suffix means two
// workspaces with the same intent never collide (FR-A1, §2 immutable slug).
func Slug(intent string) string {
	base := kebab(intent)
	if base == "" {
		base = "workspace"
	}
	return base + "-" + shortID()
}

func kebab(s string) string {
	var b strings.Builder
	lastDash := true // avoid leading dash
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if wc := wordCount(b.String()); wc >= 6 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func wordCount(s string) int {
	n := 0
	for _, part := range strings.Split(s, "-") {
		if part != "" {
			n++
		}
	}
	return n
}

// shortID returns 4 lowercase base32 chars from crypto/rand.
func shortID() string {
	var buf [3]byte
	_, _ = rand.Read(buf[:])
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf[:]))[:4]
}
