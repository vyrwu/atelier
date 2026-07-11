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
	Root         string
	CodeRoot     string // <root>/code/github  (owner/repo checkouts)
	WorktreeRoot string // <root>/code/.worktrees/github
	MultiRoot    string // <root>/code  (non-git; safe launch cwd)
	Origins      string // <root>/origins  (bare origins)
	ConfigHome   string // <root>/config  (XDG_CONFIG_HOME)
	CacheHome    string // <root>/cache   (XDG_CACHE_HOME)
	BinDir       string // <root>/bin
	GitConfig    string // <root>/gitconfig
}

func newLayout(root string) *Layout {
	return &Layout{
		Root:         root,
		CodeRoot:     filepath.Join(root, "code", "github"),
		WorktreeRoot: filepath.Join(root, "code", ".worktrees", "github"),
		MultiRoot:    filepath.Join(root, "code"),
		Origins:      filepath.Join(root, "origins"),
		ConfigHome:   filepath.Join(root, "config"),
		CacheHome:    filepath.Join(root, "cache"),
		BinDir:       filepath.Join(root, "bin"),
		GitConfig:    filepath.Join(root, "gitconfig"),
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
		"XDG_CONFIG_HOME":   l.ConfigHome,
		"XDG_CACHE_HOME":    l.CacheHome,
		"GIT_CONFIG_GLOBAL": l.GitConfig,
		"GIT_CONFIG_SYSTEM": os.DevNull,
	} {
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("setenv %s: %w", k, err)
		}
	}
	return nil
}

// Options tunes a hydration. AI is the [integrations] ai adapter the
// sandbox config selects ("claude" for the real agent — the demo default;
// "mock" for offline/no-auth). Forge is always the offline mock adapter.
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
	for _, d := range []string{l.CodeRoot, l.WorktreeRoot, l.Origins, l.ConfigHome, l.CacheHome, l.BinDir} {
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
	// pass "mock" for a no-auth/offline run. forge = "mock" is always
	// atelier's own offline forge adapter, classifying each workspace from
	// the fixture map (writeMockForgeFixture) with no `gh`.
	cfg := fmt.Sprintf(`# atelier demo sandbox — generated by internal/seed. Ephemeral.
[workspaces]
code_root       = %q
worktree_root   = %q
multi_repo_root = %q

[integrations]
ai    = %q
forge = "mock"

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
`, l.CodeRoot, l.WorktreeRoot, l.MultiRoot, ai)
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
func (l *Layout) seedState(sc *Scenario) error {
	now := time.Now().Unix()
	st := &statestore.State{
		CapturedAt:        now,
		LastActiveSession: sc.LastActive,
	}
	// cwd -> forge state, consumed by the mock forge adapter (mock.Status
	// looks a workspace's Cwd up here). This is the real source of truth
	// for the PR badge; the refresh reads it offline, no `gh`.
	forgeFixture := map[string]string{}

	for _, ws := range sc.Workspaces {
		w := statestore.Workspace{
			SessionName: ws.Session,
			RepoPath:    filepath.Join(l.CodeRoot, ws.RepoSlug),
			Kind:        ws.Kind,
			CreatedAt:   ago(now, time.Duration(ws.CreatedAt)),
		}
		for _, win := range ws.Windows {
			cwd := filepath.Join(l.CodeRoot, ws.RepoSlug)
			if win.Worktree != "" {
				cwd = filepath.Join(l.WorktreeRoot, ws.RepoSlug, win.Worktree)
			}
			meta := cloneMeta(win.Metadata)
			ensure := func() {
				if meta == nil {
					meta = map[string]string{}
				}
			}
			if win.PR != "" {
				forgeFixture[cwd] = win.PR
				// Also seed @forge_state directly so the badge renders on the
				// very first picker open, before the (async, offline) refresh
				// re-affirms it from the fixture.
				ensure()
				meta["forge.state"] = win.PR
			}
			if win.Tag != "" {
				// workspace.tag metadata → @workspace_tag on restore (the
				// picker's tag pill + tag-group sort).
				ensure()
				meta[workspace.TagMetadataKey] = win.Tag
			}
			// Per-window age: use the window's own createdAt, else inherit
			// the workspace-level one so EVERY window gets a stamped
			// @workspace_created_ts on restore (not just the session's first).
			winCreated := time.Duration(win.CreatedAt)
			if winCreated == 0 {
				winCreated = time.Duration(ws.CreatedAt)
			}
			sw := statestore.Window{
				Name:      win.Name,
				Cwd:       cwd,
				Branch:    win.Branch,
				Attention: win.Attention,
				Recap:     win.Recap,
				CreatedAt: ago(now, winCreated),
				Metadata:  meta,
			}
			if win.Recap != "" {
				sw.RecapTs = ago(now, time.Duration(win.RecapAge))
			}
			w.Windows = append(w.Windows, sw)
		}
		st.Workspaces = append(st.Workspaces, w)
	}
	if err := l.writeMockForgeFixture(forgeFixture); err != nil {
		return err
	}
	return statestore.Save(st)
}

// writeMockForgeFixture writes the cwd->state map the mock forge adapter
// reads (mock.MockForgeFixturePath, under XDG_CONFIG_HOME/atelier).
func (l *Layout) writeMockForgeFixture(fixture map[string]string) error {
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

func cloneMeta(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
