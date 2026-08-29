package seed

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/adapters/mock"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/workspace"
	"gopkg.in/yaml.v3"
)

// Layout is the resolved filesystem layout of a hydrated sandbox under a
// single root. Every path is disposable — delete Root and it's gone.
type Layout struct {
	Root          string
	CodeRoot      string // <root>/code/github  (owner/repo checkouts)
	WorktreeRoot  string // <root>/code/.worktrees/github
	MultiRoot     string // <root>/code  (non-git; safe launch cwd)
	WorkspaceRoot string // <root>/ateliers  (intent-workspace roots; worktree symlinks)
	Origins       string // <root>/origins  (bare origins)
	ConfigHome    string // <root>/config  (XDG_CONFIG_HOME)
	CacheHome     string // <root>/cache   (XDG_CACHE_HOME)
	BinDir        string // <root>/bin
	GitConfig     string // <root>/gitconfig
}

func newLayout(root string) *Layout {
	return &Layout{
		Root:          root,
		CodeRoot:      filepath.Join(root, "code", "github"),
		WorktreeRoot:  filepath.Join(root, "code", ".worktrees", "github"),
		MultiRoot:     filepath.Join(root, "code"),
		WorkspaceRoot: filepath.Join(root, "ateliers"),
		Origins:       filepath.Join(root, "origins"),
		ConfigHome:    filepath.Join(root, "config"),
		CacheHome:     filepath.Join(root, "cache"),
		BinDir:        filepath.Join(root, "bin"),
		GitConfig:     filepath.Join(root, "gitconfig"),
	}
}

// Env returns the environment (KEY=VALUE, layered on os.Environ) that
// isolates every atelier / git / tmux invocation to this sandbox and
// points the workspace picker at the sandbox repos.
func (l *Layout) Env() []string {
	set := map[string]string{
		"XDG_CONFIG_HOME":   l.ConfigHome,
		"XDG_CACHE_HOME":    l.CacheHome,
		"GIT_CONFIG_GLOBAL": l.GitConfig,
		"GIT_CONFIG_SYSTEM": os.DevNull,
		"PATH":              l.BinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		// The M-n / M-s pickers discover repos from these env vars (see
		// workspaceCodeRoot), NOT from config.toml — without them the
		// picker lists the user's real ~/code/github.
		"ATELIER_CODE_ROOT":       l.CodeRoot,
		"ATELIER_WORKTREE_ROOT":   l.WorktreeRoot,
		"ATELIER_MULTI_REPO_ROOT": l.MultiRoot,
		"ATELIER_WORKSPACE_ROOT":  l.WorkspaceRoot,
	}
	out := make([]string, 0, len(os.Environ())+len(set))
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if _, override := set[k]; override {
			continue
		}
		// Strip TMUX* so the sandbox tmux launches even from INSIDE another
		// tmux/atelier session (nested tmux otherwise refuses and the client
		// reports "server terminated unexpectedly").
		if strings.HasPrefix(k, "TMUX") {
			continue
		}
		out = append(out, e)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}

// apply mirrors the isolation env into THIS process so statestore.Save
// (XDG_CACHE_HOME) and git (GIT_CONFIG_*) operate on the sandbox during
// hydration.
func (l *Layout) apply() error {
	for k, v := range map[string]string{
		"XDG_CONFIG_HOME":        l.ConfigHome,
		"XDG_CACHE_HOME":         l.CacheHome,
		"GIT_CONFIG_GLOBAL":      l.GitConfig,
		"GIT_CONFIG_SYSTEM":      os.DevNull,
		"ATELIER_WORKSPACE_ROOT": l.WorkspaceRoot,
	} {
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("setenv %s: %w", k, err)
		}
	}
	return nil
}

// Options tunes a hydration. AI is the [ai] provider the sandbox config
// selects ("claude" for the real agent — the demo default; "mock" for
// offline/no-auth). Forge is always the offline mock adapter.
type Options struct {
	AI string
}

// Hydrate materializes the scenario under root and returns the layout.
// It sets XDG + git env vars on the current process (so Save/git target
// the sandbox); callers launch atelier with Layout.Env().
func Hydrate(root string, sc *Scenario, opts Options) (*Layout, error) {
	if opts.AI == "" {
		opts.AI = "claude"
	}
	l := newLayout(root)
	for _, d := range []string{l.CodeRoot, l.WorktreeRoot, l.WorkspaceRoot, l.Origins, l.ConfigHome, l.CacheHome, l.BinDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	if err := l.apply(); err != nil {
		return nil, err
	}
	if err := l.writeGitConfig(); err != nil {
		return nil, err
	}
	if err := l.writeAtelierConfig(opts.AI); err != nil {
		return nil, err
	}
	if err := l.seedK8sContext(); err != nil {
		return nil, fmt.Errorf("seed k8s: %w", err)
	}
	for i := range sc.Repos {
		if err := l.buildRepo(&sc.Repos[i]); err != nil {
			return nil, fmt.Errorf("repo %s: %w", sc.Repos[i].Slug, err)
		}
	}
	if err := l.seedState(sc); err != nil {
		return nil, fmt.Errorf("seed state: %w", err)
	}
	return l, nil
}

func (l *Layout) writeGitConfig() error {
	const cfg = `[user]
	name = Atelier Demo
	email = demo@atelier.sandbox
[init]
	defaultBranch = main
[commit]
	gpgsign = false
[advice]
	detachedHead = false
`
	return os.WriteFile(l.GitConfig, []byte(cfg), 0o644)
}

func (l *Layout) writeAtelierConfig(ai string) error {
	dir := filepath.Join(l.ConfigHome, "atelier")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// ai defaults to "claude" (the real agent — an authentic M-n demo);
	// pass "mock" for a no-auth/offline run. forge provider "mock" is always
	// atelier's own offline forge adapter, classifying each workspace from
	// the fixture map (writeMockForgeFixture) with no `gh`.
	cfg := fmt.Sprintf(`# atelier demo sandbox — generated by internal/seed. Ephemeral.
[workspaces]
code_root       = %q
worktree_root   = %q
multi_repo_root = %q
workspace_root  = %q

[ai]
provider = %q

[forge]
provider = "mock"

# lazygit as a config launcher (per-workspace git TUI) — shows in M-; and
# on M-g, opens in the workspace's worktree. Requires lazygit on PATH.
[tools.lazygit]
launch       = "lazygit"
popup        = "workspace"
key          = "M-g"
requires     = ["lazygit"]
icon         = "枝"
accent_color = "140"
title        = "Lazygit"
description  = "Per-workspace lazygit"
`, l.CodeRoot, l.WorktreeRoot, l.MultiRoot, l.WorkspaceRoot, ai)
	return os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644)
}

// seedK8sContext wires the sandbox's K9s tool (M-; → K9s) to your real
// kube cluster (e.g. kind) WITHOUT touching your real setup: it writes a
// contexts.yaml with a single context pointing at your kubeconfig's
// current-context, whose initCmd copies your real kubeconfig into the
// sandbox's per-context KUBECONFIG on first open (the original is never
// modified). No kubeconfig / current-context found → no k8s config (M-;
// → K9s simply reports no contexts); the rest of the sandbox is
// unaffected. k9s + a running cluster must exist on the machine.
func (l *Layout) seedK8sContext() error {
	kubeconfig := realKubeconfigPath()
	current := currentKubeContext(kubeconfig)
	if current == "" {
		return nil // no cluster context to point at; skip k8s entirely
	}
	dir := filepath.Join(l.ConfigHome, "atelier", "k8s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The k8s tool runs `k9s --context <context>` under a sandbox-managed
	// KUBECONFIG; initCmd populates it from the real kubeconfig on first
	// launch (tool sets $KUBECONFIG for the popup).
	contexts := fmt.Sprintf(`# atelier demo sandbox — generated by internal/seed.
contexts:
  - name: %q
    context: %q
    initCmd: cp %q "$KUBECONFIG"
`, current, current, kubeconfig)
	return os.WriteFile(filepath.Join(dir, "contexts.yaml"), []byte(contexts), 0o644)
}

// realKubeconfigPath resolves the user's kubeconfig: the first entry of
// $KUBECONFIG, else ~/.kube/config.
func realKubeconfigPath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		if first := strings.Split(v, string(os.PathListSeparator))[0]; first != "" {
			return first
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

// currentKubeContext reads `current-context` from a kubeconfig file.
// Empty if the file is absent/unreadable or has no current-context.
func currentKubeContext(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var kc struct {
		CurrentContext string `yaml:"current-context"`
	}
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return ""
	}
	return strings.TrimSpace(kc.CurrentContext)
}

// buildRepo creates the bare origin, the local checkout with initial
// content, the origin/local divergence, and the worktrees.
func (l *Layout) buildRepo(r *Repo) error {
	origin := filepath.Join(l.Origins, r.Slug+".git")
	work := filepath.Join(l.CodeRoot, r.Slug)
	if err := os.MkdirAll(filepath.Dir(origin), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(work), 0o755); err != nil {
		return err
	}
	if err := git("", "init", "-q", "--bare", origin); err != nil {
		return err
	}
	if err := git("", "init", "-q", "-b", "main", work); err != nil {
		return err
	}
	if err := git(work, "remote", "add", "origin", origin); err != nil {
		return err
	}
	if err := writeFiles(work, r.Files); err != nil {
		return err
	}
	if err := commitAll(work, "chore: initial commit"); err != nil {
		return err
	}
	if err := git(work, "push", "-q", "-u", "origin", "main"); err != nil {
		return err
	}

	// Origin advances (local becomes "behind") via a throwaway clone.
	if len(r.OriginCommits) > 0 {
		tmp := filepath.Join(l.Root, ".tmp-clone-"+strings.ReplaceAll(r.Slug, "/", "-"))
		if err := git("", "clone", "-q", origin, tmp); err != nil {
			return err
		}
		for _, c := range r.OriginCommits {
			if err := writeFiles(tmp, c.Files); err != nil {
				return err
			}
			if err := commitAll(tmp, c.Message); err != nil {
				return err
			}
		}
		if err := git(tmp, "push", "-q", "origin", "main"); err != nil {
			return err
		}
		if err := os.RemoveAll(tmp); err != nil {
			return err
		}
	}

	// Local advances (becomes "ahead") without pushing.
	for _, c := range r.LocalCommits {
		if err := writeFiles(work, c.Files); err != nil {
			return err
		}
		if err := commitAll(work, c.Message); err != nil {
			return err
		}
	}

	for _, wt := range r.Worktrees {
		if err := l.buildWorktree(r.Slug, work, wt); err != nil {
			return fmt.Errorf("worktree %s: %w", wt.Branch, err)
		}
	}
	return nil
}

func (l *Layout) buildWorktree(slug, work string, wt Worktree) error {
	path := filepath.Join(l.WorktreeRoot, slug, wt.Branch)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := git(work, "worktree", "add", "-q", "-b", wt.Branch, path, "main"); err != nil {
		return err
	}
	for _, c := range wt.Commits {
		if err := writeFiles(path, c.Files); err != nil {
			return err
		}
		if err := commitAll(path, c.Message); err != nil {
			return err
		}
	}
	if err := writeFiles(path, wt.Dirty); err != nil {
		return err
	}
	if wt.SoftClosed {
		if err := os.WriteFile(filepath.Join(path, ".atelier-soft-closed"), nil, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// seedState builds the statestore.State from the scenario and Saves it,
// and writes the mock-forge fixture the forge adapter classifies from.
// XDG_CACHE_HOME is already set (apply), so Save writes into the sandbox.
//
// Each scenario workspace becomes one statestore.Workspace (an intent): a
// dedicated Root under the sandbox workspace root, its declared repo
// worktrees symlinked in via workspace.LinkWorktree, one driver window,
// and its PRs. The PRs are also written to the mock-forge fixture keyed by
// the exact dir the forge sweep queries (repoQueryDir), so the offline
// refresh reproduces them.
func (l *Layout) seedState(sc *Scenario) error {
	now := time.Now().Unix()
	st := &statestore.State{
		CapturedAt:        now,
		LastActiveSession: sc.LastActive,
	}
	// repoQueryDir -> the PRs the mock forge should return for that dir. The
	// forge sweep runs `List(dir)` where dir is a worktree's real path (or
	// <codeRoot>/<repo> when the repo has no worktree in the workspace); this
	// map mirrors that key so the offline refresh reproduces every PR.
	forgeFixture := map[string][]mock.ForgePR{}

	for _, ws := range sc.Workspaces {
		root := filepath.Join(l.WorkspaceRoot, ws.Slug)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("workspace %s: mkdir root: %w", ws.Slug, err)
		}
		w := statestore.Workspace{
			SessionName: ws.Slug,
			Title:       ws.Title,
			Intent:      ws.Intent,
			Summary:     ws.Summary,
			Tag:         ws.Tag,
			Root:        root,
			CreatedAt:   ago(now, time.Duration(ws.CreatedAt)),
		}

		// Symlink each declared worktree into the workspace root so the sandbox
		// shows the real <root>/<repo>/<branch> layout. wtByRepoBranch lets PRs
		// resolve their query dir back to the real worktree path.
		wtByRepoBranch := map[string]string{} // "repo\x00branch" -> real worktree path
		wtByRepo := map[string]string{}       // repo -> any worktree path in this ws
		for _, wt := range ws.Worktrees {
			wtPath := filepath.Join(l.WorktreeRoot, wt.RepoSlug, wt.Branch)
			link, err := workspace.LinkWorktree(root, wtPath, wt.RepoSlug, wt.Branch)
			if err != nil {
				return fmt.Errorf("workspace %s: link worktree %s@%s: %w", ws.Slug, wt.RepoSlug, wt.Branch, err)
			}
			w.Worktrees = append(w.Worktrees, statestore.Worktree{
				Repo:   wt.RepoSlug,
				Branch: wt.Branch,
				Path:   wtPath,
				Link:   link,
			})
			wtByRepoBranch[wt.RepoSlug+"\x00"+wt.Branch] = wtPath
			wtByRepo[wt.RepoSlug] = wtPath
		}

		// Single-repo workspace: seed the RepoPath convenience hint at the
		// repo's code-root checkout. A multi-repo workspace spans repos, so no
		// single hint applies.
		if len(ws.Worktrees) == 1 {
			w.RepoPath = filepath.Join(l.CodeRoot, ws.Worktrees[0].RepoSlug)
		}

		// PRs: onto the workspace record AND into the forge fixture at the dir
		// the sweep will query for that repo.
		for _, pr := range ws.PRs {
			w.PRs = append(w.PRs, statestore.PR{
				Number:         pr.Number,
				Repo:           pr.RepoSlug,
				Title:          pr.Title,
				State:          pr.State,
				CI:             pr.CI,
				ReviewDecision: pr.ReviewDecision,
				Comments:       pr.Comments,
				URL:            pr.URL,
				Branch:         pr.Branch,
				UpdatedAt:      ago(now, time.Duration(ws.CreatedAt)),
			})
			key := l.forgeQueryDir(pr, wtByRepoBranch, wtByRepo)
			forgeFixture[key] = append(forgeFixture[key], mock.ForgePR{
				Number:         pr.Number,
				Repo:           pr.RepoSlug,
				Title:          pr.Title,
				State:          pr.State,
				CI:             pr.CI,
				ReviewDecision: pr.ReviewDecision,
				Comments:       pr.Comments,
				URL:            pr.URL,
				Branch:         pr.Branch,
			})
		}

		// The driver window: the agent, running from the workspace root.
		driver := statestore.Window{
			Name:      driverWindowName(ws.Slug),
			Cwd:       root,
			Attention: ws.Attention,
			Recap:     ws.Recap,
			CreatedAt: ago(now, time.Duration(ws.CreatedAt)),
		}
		if ws.Recap != "" {
			driver.RecapTs = ago(now, time.Duration(ws.RecapAge))
		}
		if ws.Intent != "" {
			driver.Metadata = map[string]string{"ai.prompt": ws.Intent}
		}
		w.Windows = append(w.Windows, driver)

		st.Workspaces = append(st.Workspaces, w)
	}
	if err := l.writeMockForgeFixture(forgeFixture); err != nil {
		return err
	}
	return statestore.Save(st)
}

// forgeQueryDir returns the dir the forge sweep (repoQueryDir) will run
// `List` in for a PR's repo: the real worktree path of the worktree that
// backs the PR (matched by branch, else any worktree for the repo), else
// the repo's code-root checkout when the workspace has no worktree for it.
func (l *Layout) forgeQueryDir(pr WsPR, byRepoBranch, byRepo map[string]string) string {
	if pr.Branch != "" {
		if p, ok := byRepoBranch[pr.RepoSlug+"\x00"+pr.Branch]; ok {
			return p
		}
	}
	if p, ok := byRepo[pr.RepoSlug]; ok {
		return p
	}
	return filepath.Join(l.CodeRoot, pr.RepoSlug)
}

// driverWindowName is the driver window's tmux name: the last segment of the
// workspace slug (a readable label), or "agent" when the slug is empty.
func driverWindowName(slug string) string {
	if slug == "" {
		return "agent"
	}
	return filepath.Base(slug)
}

// writeMockForgeFixture writes the repoQueryDir->PRs map the mock forge
// adapter reads (mock.MockForgeFixturePath, under XDG_CONFIG_HOME/atelier).
func (l *Layout) writeMockForgeFixture(fixture map[string][]mock.ForgePR) error {
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(l.ConfigHome, "atelier", mock.MockForgeFixture), data, 0o644)
}

func ago(now int64, d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return now - int64(d.Seconds())
}

// --- small git + fs helpers -------------------------------------------------

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func commitAll(dir, msg string) error {
	if err := git(dir, "add", "-A"); err != nil {
		return err
	}
	return git(dir, "commit", "-q", "-m", msg)
}

func writeFiles(root string, files map[string]string) error {
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
