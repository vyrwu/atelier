package seed

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

// hydrateAcme hydrates the built-in scenario into an isolated temp root.
func hydrateAcme(t *testing.T) *Layout {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	sc, err := Builtin("acme-platform")
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}
	l, err := Hydrate(root, sc, Options{AI: "claude"})
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	return l
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestBuiltin_AcmePlatformParses(t *testing.T) {
	sc, err := Builtin("acme-platform")
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}
	if sc.Name != "acme-platform" {
		t.Errorf("name = %q", sc.Name)
	}
	if len(sc.Repos) < 10 {
		t.Errorf("repos = %d, want >= 10", len(sc.Repos))
	}
	if sc.Workspaces[0].CreatedAt == 0 {
		t.Error("createdAt not parsed from YAML duration string")
	}

	var windows, attn, tagged int
	prStates := map[string]int{}
	for _, ws := range sc.Workspaces {
		for _, w := range ws.Windows {
			windows++
			if w.Recap == "" {
				t.Errorf("%s:%s has no recap", ws.Session, w.Name)
			}
			if w.Attention {
				attn++
			}
			if w.PR != "" {
				prStates[w.PR]++
			}
			if w.Tag != "" {
				tagged++
			}
		}
	}
	if windows < 10 {
		t.Errorf("windows = %d, want >= 10", windows)
	}
	if attn == 0 {
		t.Error("no attention windows")
	}
	if tagged == 0 {
		t.Error("no tagged windows (M-t demo)")
	}
	for _, s := range []string{"open", "draft", "merged", "closed"} {
		if prStates[s] == 0 {
			t.Errorf("no window with PR state %q", s)
		}
	}
}

func TestBuiltin_Unknown(t *testing.T) {
	if _, err := Builtin("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown scenario")
	}
}

func TestLoad_ValidatesUnknownRepoSlug(t *testing.T) {
	_, err := Load([]byte(`
name: bad
repos:
  - slug: a/b
    files: {README.md: "x\n"}
workspaces:
  - session: a/b
    repoSlug: a/nope
    kind: worktree
    windows:
      - {name: main, branch: main}
`))
	if err == nil || !strings.Contains(err.Error(), "unknown repo") {
		t.Fatalf("expected unknown-repo validation error, got %v", err)
	}
}

func TestLoad_RejectsBadPRState(t *testing.T) {
	_, err := Load([]byte(`
name: bad
repos:
  - slug: a/b
    files: {README.md: "x\n"}
workspaces:
  - session: a/b
    repoSlug: a/b
    kind: worktree
    windows:
      - {name: main, branch: main, pr: bogus}
`))
	if err == nil || !strings.Contains(err.Error(), "pr=") {
		t.Fatalf("expected pr-state validation error, got %v", err)
	}
}

func TestHydrate_ReposAndWorktreesAreRealGit(t *testing.T) {
	l := hydrateAcme(t)
	sc, _ := Builtin("acme-platform")
	for _, r := range sc.Repos {
		work := filepath.Join(l.CodeRoot, r.Slug)
		if got := gitOut(t, work, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
			t.Errorf("%s: HEAD = %q, want main", r.Slug, got)
		}
		for _, wt := range r.Worktrees {
			wtPath := filepath.Join(l.WorktreeRoot, r.Slug, wt.Branch)
			if got := gitOut(t, wtPath, "rev-parse", "--abbrev-ref", "HEAD"); got != wt.Branch {
				t.Errorf("%s worktree %s: HEAD = %q", r.Slug, wt.Branch, got)
			}
		}
	}
}

func TestHydrate_TerraformDivergence(t *testing.T) {
	l := hydrateAcme(t)
	work := filepath.Join(l.CodeRoot, "acme-platform/terraform-infra")
	gitOut(t, work, "fetch", "-q", "origin")
	behind := gitOut(t, work, "rev-list", "--count", "main..origin/main")
	ahead := gitOut(t, work, "rev-list", "--count", "origin/main..main")
	if ahead != "2" || behind != "1" {
		t.Errorf("terraform divergence = ahead %s / behind %s, want ahead 2 / behind 1", ahead, behind)
	}
}

func TestHydrate_HelmWorktreeDirty(t *testing.T) {
	l := hydrateAcme(t)
	wt := filepath.Join(l.WorktreeRoot, "acme-platform/helm-charts/feat/bump-ingress-nginx")
	if status := gitOut(t, wt, "status", "--porcelain"); status == "" {
		t.Error("helm worktree expected dirty (values drift), got clean")
	}
	if chart := gitOut(t, wt, "show", "HEAD:charts/ingress-nginx/Chart.yaml"); !strings.Contains(chart, "4.12.1") {
		t.Errorf("ingress bump not committed on branch:\n%s", chart)
	}
}

func TestHydrate_SoftClosedMarker(t *testing.T) {
	l := hydrateAcme(t)
	marker := filepath.Join(l.WorktreeRoot, "acme-platform/platform-scripts/fix/ci-cache-key/.atelier-soft-closed")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("soft-closed marker missing: %v", err)
	}
}

func TestHydrate_SeedsStatestore(t *testing.T) {
	hydrateAcme(t)
	st, err := statestore.Load()
	if err != nil || st == nil {
		t.Fatalf("statestore.Load: %v (nil=%v)", err, st == nil)
	}
	if len(st.Workspaces) < 10 {
		t.Fatalf("workspaces = %d, want >= 10", len(st.Workspaces))
	}
	if st.LastActiveSession == "" {
		t.Error("last_active not seeded")
	}

	w := st.FindWindow("acme-platform/helm-charts", "feat/bump-ingress-nginx")
	if w == nil {
		t.Fatal("helm-charts window missing from state")
	}
	if !w.Attention {
		t.Error("helm-charts window: attention not seeded")
	}
	if !strings.Contains(w.Recap, "ingress-nginx") {
		t.Errorf("recap = %q, want it to mention ingress-nginx", w.Recap)
	}
	if w.RecapTs == 0 {
		t.Error("recap_ts not set")
	}
	// Forge badge seeded as metadata → restore stamps @forge_state for the
	// immediate first render (the mock forge re-affirms it from the fixture).
	if w.Metadata["forge.state"] != "open" {
		t.Errorf("forge.state = %q, want open", w.Metadata["forge.state"])
	}
	// Per-window age: the first window inherits the workspace createdAt (9m);
	// the second declares its own (24m), so both are non-zero and distinct —
	// this is what makes every picker row show its own age, not a blank.
	if w.CreatedAt == 0 {
		t.Error("first helm window: CreatedAt not seeded")
	}
	redis := st.FindWindow("acme-platform/helm-charts", "feat/redis-pdb")
	if redis == nil {
		t.Fatal("helm-charts feat/redis-pdb window missing from state")
	}
	if redis.CreatedAt == 0 {
		t.Error("second helm window: CreatedAt not seeded (would show blank age)")
	}
	if redis.CreatedAt == w.CreatedAt {
		t.Errorf("both helm windows share CreatedAt %d; want per-window ages", w.CreatedAt)
	}

	var attn, total int
	for _, ws := range st.Workspaces {
		for _, win := range ws.Windows {
			total++
			if win.Attention {
				attn++
			}
		}
	}
	if attn == 0 || attn == total {
		t.Errorf("attention windows = %d of %d, want a mix", attn, total)
	}
}

func TestHydrate_WritesConfigWithIntegrations(t *testing.T) {
	l := hydrateAcme(t)
	data, err := os.ReadFile(filepath.Join(l.ConfigHome, "atelier", "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	cfg := string(data)
	for _, want := range []string{l.CodeRoot, `ai    = "claude"`, `forge = "mock"`, "[tools.lazygit]", `launch       = "lazygit"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.toml missing %q:\n%s", want, cfg)
		}
	}
}

func TestHydrate_SeedsK8sContextFromKubeconfig(t *testing.T) {
	root := t.TempDir()
	// A fake kubeconfig with a current-context, isolated to this test.
	kube := filepath.Join(root, "kubeconfig")
	if err := os.WriteFile(kube, []byte("apiVersion: v1\nkind: Config\ncurrent-context: kind-demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kube)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	sc, _ := Builtin("acme-platform")
	l, err := Hydrate(root, sc, Options{AI: "claude"})
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(l.ConfigHome, "atelier", "k8s", "contexts.yaml"))
	if err != nil {
		t.Fatalf("read contexts.yaml: %v", err)
	}
	cfg := string(data)
	if !strings.Contains(cfg, `context: "kind-demo"`) {
		t.Errorf("contexts.yaml missing current-context kind-demo:\n%s", cfg)
	}
	if !strings.Contains(cfg, kube) {
		t.Errorf("contexts.yaml initCmd should copy the real kubeconfig %q:\n%s", kube, cfg)
	}
}

func TestHydrate_WritesMockForgeFixture(t *testing.T) {
	l := hydrateAcme(t)
	data, err := os.ReadFile(filepath.Join(l.ConfigHome, "atelier", "mock-forge.json"))
	if err != nil {
		t.Fatalf("read mock-forge.json: %v", err)
	}
	var fixture map[string]string
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	// The mock forge classifies by worktree Cwd; the helm ingress worktree
	// must map to "open" so the badge refresh reproduces it offline.
	cwd := filepath.Join(l.WorktreeRoot, "acme-platform/helm-charts/feat/bump-ingress-nginx")
	if fixture[cwd] != "open" {
		t.Errorf("fixture[%q] = %q, want open", cwd, fixture[cwd])
	}
	states := map[string]int{}
	for _, s := range fixture {
		states[s]++
	}
	for _, s := range []string{"open", "draft", "merged", "closed"} {
		if states[s] == 0 {
			t.Errorf("fixture has no %q state", s)
		}
	}
}

func TestLayout_EnvIsolatesReposAndStripsTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-fake,123,0")
	l := hydrateAcme(t)
	m := map[string]string{}
	for _, e := range l.Env() {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	for k, want := range map[string]string{
		"ATELIER_CODE_ROOT":       l.CodeRoot,
		"ATELIER_WORKTREE_ROOT":   l.WorktreeRoot,
		"ATELIER_MULTI_REPO_ROOT": l.MultiRoot,
		"XDG_CONFIG_HOME":         l.ConfigHome,
		"XDG_CACHE_HOME":          l.CacheHome,
		"GIT_CONFIG_GLOBAL":       l.GitConfig,
	} {
		if m[k] != want {
			t.Errorf("env %s = %q, want %q", k, m[k], want)
		}
	}
	for _, e := range l.Env() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_") {
			t.Errorf("env should strip TMUX*, found %q", e)
		}
	}
	if !strings.HasPrefix(m["PATH"], l.BinDir+string(os.PathListSeparator)) {
		t.Errorf("PATH should be prefixed with sandbox bin %s, got %q", l.BinDir, m["PATH"])
	}
}
