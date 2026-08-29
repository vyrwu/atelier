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
//
// The scenario schema mirrors the v1 intent-workspace model: a Workspace
// is an INTENT (a task the user handed the agent), not a repo. It owns a
// dedicated Root directory into which one or more repo worktrees are
// symlinked, a driver window (the agent), and a set of PRs. Repos remain
// real git built independently of workspaces; a workspace's worktrees
// reference branches those repos declare.
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
	// LastActive is the workspace slug atelier resumes on launch. Point it at
	// a workspace WITHOUT attention so the attention badge is something the
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

// Workspace is a seeded intent-workspace: a task the agent worked on,
// spanning one or more repo worktrees, with a driver window and PRs. It
// materializes as a statestore.Workspace pointing at a dedicated Root
// directory under the sandbox workspace root, into which the referenced
// worktrees are symlinked.
type Workspace struct {
	Slug    string `yaml:"slug"`    // session name / workspace id (the persistence key)
	Title   string `yaml:"title"`   // human, renameable label shown in M-s
	Intent  string `yaml:"intent"`  // the task text the user typed at M-n
	Summary string `yaml:"summary"` // workspace-level rollup line under the title
	Tag     string `yaml:"tag"`     // grouping label (client/initiative/subsystem)

	// CreatedAt is the workspace age ("ago" duration) — drives the picker's
	// age column and stamps every driver window's created_ts.
	CreatedAt Duration `yaml:"createdAt"`

	// Recap / RecapAge / Attention describe the DRIVER window's state: what
	// the agent was doing (recap), how long ago (recapAge), and whether it
	// finished blocked on the user (attention → ⏺ badge).
	Recap     string   `yaml:"recap"`
	RecapAge  Duration `yaml:"recapAge"`
	Attention bool     `yaml:"attention"`

	// Worktrees are the repo branches this workspace's agent produced —
	// symlinked into the workspace Root. Each references a real Worktree
	// declared under the matching Repo.
	Worktrees []WsWorktree `yaml:"worktrees"`

	// PRs are the pull requests surfaced in the M-c Changes view, classified
	// offline by the mock forge from the seeded fixture.
	PRs []WsPR `yaml:"prs"`
}

// WsWorktree selects one repo branch to symlink into the workspace Root.
// Branch must correspond to a real Worktree declared under RepoSlug; the
// real worktree lives at <worktreeRoot>/<repoSlug>/<branch>.
type WsWorktree struct {
	RepoSlug string `yaml:"repoSlug"`
	Branch   string `yaml:"branch"`
}

// WsPR seeds one pull request on a workspace. State/CI/ReviewDecision use
// the kernel's normalized vocabulary (see internal/integration). Empty
// State = no badge.
type WsPR struct {
	Number         int    `yaml:"number"`
	RepoSlug       string `yaml:"repoSlug"`
	Title          string `yaml:"title"`
	State          string `yaml:"state"`          // open|draft|merged|closed
	CI             string `yaml:"ci"`             // pass|fail|pending
	ReviewDecision string `yaml:"reviewDecision"` // approved|changes_requested|review_required
	Comments       int    `yaml:"comments"`
	Branch         string `yaml:"branch"`
	URL            string `yaml:"url"`
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
	// repo slug → set of declared branches (for worktree cross-checks).
	branches := map[string]map[string]bool{}
	for _, r := range sc.Repos {
		if r.Slug == "" {
			return fmt.Errorf("scenario %q: repo with empty slug", sc.Name)
		}
		if _, dup := branches[r.Slug]; dup {
			return fmt.Errorf("scenario %q: duplicate repo slug %q", sc.Name, r.Slug)
		}
		set := map[string]bool{}
		for _, wt := range r.Worktrees {
			set[wt.Branch] = true
		}
		branches[r.Slug] = set
	}
	slugs := map[string]bool{}
	for _, ws := range sc.Workspaces {
		if ws.Slug == "" {
			return fmt.Errorf("scenario %q: workspace with empty slug", sc.Name)
		}
		if ws.Title == "" {
			// filterAtelierManaged drops a workspace with no identity; Title is
			// the cheapest guarantee it survives Save/Load.
			return fmt.Errorf("scenario %q: workspace %q missing title", sc.Name, ws.Slug)
		}
		if slugs[ws.Slug] {
			return fmt.Errorf("scenario %q: duplicate workspace slug %q", sc.Name, ws.Slug)
		}
		slugs[ws.Slug] = true
		for _, wt := range ws.Worktrees {
			set, ok := branches[wt.RepoSlug]
			if !ok {
				return fmt.Errorf("scenario %q: workspace %q references unknown repo %q", sc.Name, ws.Slug, wt.RepoSlug)
			}
			if !set[wt.Branch] {
				return fmt.Errorf("scenario %q: workspace %q worktree %s@%s has no matching worktree under that repo",
					sc.Name, ws.Slug, wt.RepoSlug, wt.Branch)
			}
		}
		for _, pr := range ws.PRs {
			if _, ok := branches[pr.RepoSlug]; !ok {
				return fmt.Errorf("scenario %q: workspace %q PR references unknown repo %q", sc.Name, ws.Slug, pr.RepoSlug)
			}
			if pr.State != "" && !validForgeState(pr.State) {
				return fmt.Errorf("scenario %q: workspace %q pr state=%q, want one of open|draft|merged|closed", sc.Name, ws.Slug, pr.State)
			}
			if pr.CI != "" && !validCIStatus(pr.CI) {
				return fmt.Errorf("scenario %q: workspace %q pr ci=%q, want one of pass|fail|pending", sc.Name, ws.Slug, pr.CI)
			}
		}
	}
	if sc.LastActive != "" && !slugs[sc.LastActive] {
		return fmt.Errorf("scenario %q: lastActive %q is not a known workspace", sc.Name, sc.LastActive)
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

func validCIStatus(s string) bool {
	switch integration.CIStatus(s) {
	case integration.CIPass, integration.CIFail, integration.CIPending:
		return true
	}
	return false
}
