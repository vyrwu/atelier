// Package seed declaratively describes and materializes an isolated,
// ephemeral atelier environment: real git repos + worktrees + a
// pre-populated statestore cache, all under a throwaway root.
//
// Nothing here is a mock. The repos are real git (real commits, a local
// bare origin so `git fetch` freshness works, genuine ahead/behind
// divergence, real uncommitted edits). The workspace state is real
// atelier persistence (the same statestore atelier writes itself),
// seeded with placeholder recap/attention/forge values that point at the
// real worktrees. There is no live agent process — the sandbox configures
// atelier's own `mock` AI adapter, so M-n (create-from-prompt) works
// offline and deterministically, and the seeded recaps stand in for prior
// agent activity.
//
// A scenario is DATA, not code: it is read from a YAML specification
// (bundled under scenarios/, or an external file). Consumed by the demo
// sandbox launcher (sandbox/) and usable from e2e tests.
package seed

import (
	"embed"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/integration"
	"gopkg.in/yaml.v3"
)

//go:embed scenarios/*.yaml
var scenariosFS embed.FS

// Scenario is a complete, declarative description of a seeded atelier
// environment. Hydrate turns one into files on disk.
type Scenario struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// LastActive is the session atelier resumes on launch. Point it at a
	// workspace WITHOUT attention so the attention badge is something the
	// demo reveals (via M-s) rather than lands on.
	LastActive string      `yaml:"lastActive"`
	Repos      []Repo      `yaml:"repos"`
	Workspaces []Workspace `yaml:"workspaces"`
}

// Repo is one real git repository under the sandbox code root. A local
// bare origin is created so pull/fetch (and thus freshness) work offline.
type Repo struct {
	Slug  string            `yaml:"slug"`  // "owner/repo"
	Files map[string]string `yaml:"files"` // committed on main at init, then pushed to origin

	// OriginCommits are pushed to origin AFTER the initial push but never
	// pulled locally — the local checkout ends up N commits BEHIND origin.
	OriginCommits []Commit `yaml:"originCommits"`
	// LocalCommits are committed locally on main but never pushed — the
	// checkout ends up N commits AHEAD of origin.
	LocalCommits []Commit `yaml:"localCommits"`

	Worktrees []Worktree `yaml:"worktrees"`
}

// Commit is a set of file writes plus a message.
type Commit struct {
	Message string            `yaml:"message"`
	Files   map[string]string `yaml:"files"`
}

// Worktree is a `git worktree add`-ed branch under the sandbox worktree
// root at <worktreeRoot>/<repoSlug>/<branch>.
type Worktree struct {
	Branch  string   `yaml:"branch"` // may contain slashes (e.g. "feat/foo")
	Commits []Commit `yaml:"commits"`

	// Dirty are uncommitted working-tree edits left in place so the
	// worktree shows dirty (git status), like work-in-progress.
	Dirty map[string]string `yaml:"dirty"`

	// SoftClosed writes a `.atelier-soft-closed` marker so M-r ranks the
	// worktree at the top of the recover list.
	SoftClosed bool `yaml:"softClosed"`
}

// Workspace is seeded atelier statestore state pointing at the real repos.
type Workspace struct {
	Session   string   `yaml:"session"`   // tmux session name; convention: the repo slug
	RepoSlug  string   `yaml:"repoSlug"`  // which Repo this workspace is for
	Kind      string   `yaml:"kind"`      // "worktree"; filterAtelierManaged needs this or RepoPath
	CreatedAt Duration `yaml:"createdAt"` // "ago": workspace age (drives the picker's age column)
	Windows   []Window `yaml:"windows"`
}

// Window is one seeded window in a workspace.
type Window struct {
	Name   string `yaml:"name"`   // tmux window name; convention: the branch name
	Branch string `yaml:"branch"` //

	// Worktree selects the cwd: if set, cwd = <worktreeRoot>/<repoSlug>/<Worktree>
	// (a feature worktree); if empty, cwd = <codeRoot>/<repoSlug> (the
	// default-branch checkout).
	Worktree string `yaml:"worktree"`

	Attention bool     `yaml:"attention"` // seeds @needs_attention on restore
	Recap     string   `yaml:"recap"`     // seeds @attention_recap
	RecapAge  Duration `yaml:"recapAge"`  // "ago" for @attention_recap_ts

	// CreatedAt is this window's age ("ago" duration → @workspace_created_ts,
	// the picker's per-window age column). Empty inherits the enclosing
	// workspace's createdAt — set it on additional worktree windows so each
	// row shows its own age, not a blank.
	CreatedAt Duration `yaml:"createdAt"`

	// Tag seeds the workspace tag (M-t) — a free-form label the picker
	// renders as a pill and sorts/groups by. Stored as the workspace.tag
	// metadata → @workspace_tag on restore. Empty = untagged.
	Tag string `yaml:"tag"`

	// PR seeds the kernel forge badge: one of open|draft|merged|closed
	// (empty = no PR, no badge). Hydrate stores it as the forge.* metadata
	// the picker reads (@forge_state for the glyph + sort), with a
	// far-future @forge_ts so the picker's offline `gh` refresh leaves it
	// alone. Requires [integrations] forge to be active (the sandbox sets
	// forge = "mock").
	PR string `yaml:"pr"`

	Metadata map[string]string `yaml:"metadata"` // plugin-namespaced (e.g. ai.*)
}

// Duration is a time.Duration that unmarshals from a YAML string like
// "9m" or "1h30m" (via time.ParseDuration).
type Duration time.Duration

// UnmarshalYAML parses a duration string. An empty/absent value is zero.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	if strings.TrimSpace(s) == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Load parses a scenario from YAML bytes and validates it.
func Load(data []byte) (*Scenario, error) {
	var sc Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse scenario: %w", err)
	}
	if err := sc.validate(); err != nil {
		return nil, err
	}
	return &sc, nil
}

// LoadFile reads a scenario from a YAML file on disk.
func LoadFile(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Load(data)
}

// Builtin loads a scenario bundled under scenarios/<name>.yaml.
func Builtin(name string) (*Scenario, error) {
	data, err := scenariosFS.ReadFile("scenarios/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("unknown built-in scenario %q (have: %s)", name, strings.Join(BuiltinNames(), ", "))
	}
	return Load(data)
}

// BuiltinNames lists the bundled scenario names.
func BuiltinNames() []string {
	entries, _ := scenariosFS.ReadDir("scenarios")
	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}

// validate catches the common authoring mistakes in a YAML spec.
func (sc *Scenario) validate() error {
	if sc.Name == "" {
		return fmt.Errorf("scenario: name is required")
	}
	if len(sc.Repos) == 0 {
		return fmt.Errorf("scenario %q: at least one repo is required", sc.Name)
	}
	slugs := map[string]bool{}
	for _, r := range sc.Repos {
		if r.Slug == "" {
			return fmt.Errorf("scenario %q: repo with empty slug", sc.Name)
		}
		slugs[r.Slug] = true
	}
	for _, ws := range sc.Workspaces {
		if !slugs[ws.RepoSlug] {
			return fmt.Errorf("scenario %q: workspace %q references unknown repo %q", sc.Name, ws.Session, ws.RepoSlug)
		}
		for _, w := range ws.Windows {
			if w.PR != "" && !validForgeState(w.PR) {
				return fmt.Errorf("scenario %q: window %q pr=%q, want one of open|draft|merged|closed", sc.Name, w.Name, w.PR)
			}
		}
	}
	return nil
}

func validForgeState(s string) bool {
	switch integration.ForgeState(s) {
	case integration.ForgeOpen, integration.ForgeDraft, integration.ForgeMerged, integration.ForgeClosed:
		return true
	}
	return false
}
