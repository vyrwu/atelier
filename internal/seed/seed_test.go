package seed

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/adapters/mock"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/workspace"
)

// hydrateAcme hydrates the built-in scenario into an isolated temp root.
func hydrateAcme(t *testing.T) *Layout {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("ATELIER_WORKSPACE_ROOT", filepath.Join(root, "ateliers"))

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
	if len(sc.Workspaces) < 4 {
		t.Fatalf("workspaces = %d, want >= 4", len(sc.Workspaces))
	}
	if sc.Workspaces[0].CreatedAt == 0 {
		t.Error("createdAt not parsed from YAML duration string")
	}

	var attn, tagged, multiRepo int
	prStates := map[string]int{}
	prCI := map[string]int{}
	for _, ws := range sc.Workspaces {
		if ws.Title == "" {
			t.Errorf("%s has no title (filterAtelierManaged would drop it)", ws.Slug)
		}
		if ws.Intent == "" {
			t.Errorf("%s has no intent", ws.Slug)
		}
		if ws.Recap == "" && len(ws.Worktrees) > 0 {
			t.Errorf("%s has worktrees but no driver recap", ws.Slug)
		}
		if ws.Attention {
			attn++
		}
		if ws.Tag != "" {
			tagged++
		}
		if len(ws.Worktrees) > 1 {
			multiRepo++
		}
		for _, pr := range ws.PRs {
			if pr.State != "" {
				prStates[pr.State]++
			}
			if pr.CI != "" {
				prCI[pr.CI]++
			}
		}
	}
	if attn == 0 {
		t.Error("no attention workspaces")
	}
	if tagged == 0 {
		t.Error("no tagged workspaces (M-t demo)")
	}
	if multiRepo == 0 {
		t.Error("no multi-repo workspaces (the new-model demo)")
	}
	for _, s := range []string{"open", "draft", "merged", "closed"} {
		if prStates[s] == 0 {
			t.Errorf("no PR with state %q", s)
		}
	}
	for _, s := range []string{"pass", "fail", "pending"} {
		if prCI[s] == 0 {
			t.Errorf("no PR with CI %q", s)
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
    worktrees:
      - {branch: feat/x}
workspaces:
  - slug: ws
    title: Bad
    worktrees:
      - {repoSlug: a/nope, branch: feat/x}
`))
	if err == nil || !strings.Contains(err.Error(), "unknown repo") {
		t.Fatalf("expected unknown-repo validation error, got %v", err)
	}
}

func TestLoad_ValidatesUnknownWorktreeBranch(t *testing.T) {
	_, err := Load([]byte(`
name: bad
repos:
  - slug: a/b
    files: {README.md: "x\n"}
    worktrees:
      - {branch: feat/x}
workspaces:
  - slug: ws
    title: Bad
    worktrees:
      - {repoSlug: a/b, branch: feat/nope}
`))
	if err == nil || !strings.Contains(err.Error(), "no matching worktree") {
		t.Fatalf("expected worktree-branch validation error, got %v", err)
	}
}

func TestLoad_RequiresWorkspaceTitle(t *testing.T) {
	_, err := Load([]byte(`
name: bad
repos:
  - slug: a/b
    files: {README.md: "x\n"}
workspaces:
  - slug: ws
`))
	if err == nil || !strings.Contains(err.Error(), "missing title") {
		t.Fatalf("expected missing-title validation error, got %v", err)
	}
}

func TestLoad_RejectsBadPRState(t *testing.T) {
	_, err := Load([]byte(`
name: bad
repos:
  - slug: a/b
    files: {README.md: "x\n"}
    worktrees:
      - {branch: feat/x}
workspaces:
  - slug: ws
    title: Bad
    worktrees:
      - {repoSlug: a/b, branch: feat/x}
    prs:
      - {number: 1, repoSlug: a/b, state: bogus}
`))
	if err == nil || !strings.Contains(err.Error(), "state=") {
		t.Fatalf("expected pr-state validation error, got %v", err)
	}
}

func TestLoad_RejectsBadPRCI(t *testing.T) {
	_, err := Load([]byte(`
name: bad
repos:
  - slug: a/b
    files: {README.md: "x\n"}
    worktrees:
      - {branch: feat/x}
workspaces:
  - slug: ws
    title: Bad
    worktrees:
      - {repoSlug: a/b, branch: feat/x}
    prs:
      - {number: 1, repoSlug: a/b, state: open, ci: bogus}
`))
	if err == nil || !strings.Contains(err.Error(), "ci=") {
		t.Fatalf("expected pr-ci validation error, got %v", err)
	}
}

func TestLoad_RejectsUnknownLastActive(t *testing.T) {
	_, err := Load([]byte(`
name: bad
lastActive: nope
repos:
  - slug: a/b
    files: {README.md: "x\n"}
workspaces:
  - slug: ws
    title: Bad
`))
	if err == nil || !strings.Contains(err.Error(), "lastActive") {
		t.Fatalf("expected lastActive validation error, got %v", err)
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
			wtPath := filepath.Join(l.WorktreeRoot, r.Slug, workspace.WorktreeDirName(wt.Branch))
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
	wt := filepath.Join(l.WorktreeRoot, "acme-platform/helm-charts/feat-bump-ingress-nginx")
	if status := gitOut(t, wt, "status", "--porcelain"); status == "" {
		t.Error("helm worktree expected dirty (values drift), got clean")
	}
	if chart := gitOut(t, wt, "show", "HEAD:charts/ingress-nginx/Chart.yaml"); !strings.Contains(chart, "4.12.1") {
		t.Errorf("ingress bump not committed on branch:\n%s", chart)
	}
}

func TestHydrate_SoftClosedMarker(t *testing.T) {
	l := hydrateAcme(t)
	marker := filepath.Join(l.WorktreeRoot, "acme-platform/platform-scripts/fix-ci-cache-key/.atelier-soft-closed")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("soft-closed marker missing: %v", err)
	}
}

func TestHydrate_SeedsStatestore(t *testing.T) {
	l := hydrateAcme(t)
	st, err := statestore.Load()
	if err != nil || st == nil {
		t.Fatalf("statestore.Load: %v (nil=%v)", err, st == nil)
	}
	if len(st.Workspaces) < 4 {
		t.Fatalf("workspaces = %d, want >= 4", len(st.Workspaces))
	}
	if st.LastActiveSession != "service-catalog-ownership" {
		t.Errorf("last_active = %q, want service-catalog-ownership", st.LastActiveSession)
	}

	// The multi-repo EKS upgrade: title/intent/root set, three worktrees
	// symlinked in, three PRs, driver window with attention + recap.
	ws := st.FindWorkspace("eks-1-30-upgrade")
	if ws == nil {
		t.Fatal("eks-1-30-upgrade workspace missing from state")
	}
	if ws.Title == "" || ws.Intent == "" {
		t.Errorf("eks workspace: title=%q intent=%q, want both set", ws.Title, ws.Intent)
	}
	if ws.Root == "" || !strings.HasSuffix(ws.Root, filepath.Join("ateliers", "eks-1-30-upgrade")) {
		t.Errorf("eks workspace root = %q, want <root>/ateliers/eks-1-30-upgrade", ws.Root)
	}
	if len(ws.Worktrees) != 3 {
		t.Errorf("eks workspace worktrees = %d, want 3", len(ws.Worktrees))
	}
	if len(ws.PRs) != 3 {
		t.Errorf("eks workspace PRs = %d, want 3", len(ws.PRs))
	}
	// Multi-repo => no single RepoPath hint.
	if ws.RepoPath != "" {
		t.Errorf("multi-repo workspace RepoPath = %q, want empty", ws.RepoPath)
	}
	if len(ws.Windows) != 1 {
		t.Fatalf("eks workspace windows = %d, want 1 (the driver)", len(ws.Windows))
	}
	driver := ws.Windows[0]
	if !driver.Attention {
		t.Error("eks driver: attention not seeded")
	}
	if !strings.Contains(driver.Recap, "1.30") {
		t.Errorf("eks driver recap = %q, want it to mention 1.30", driver.Recap)
	}
	if driver.RecapTs == 0 {
		t.Error("eks driver: recap_ts not set")
	}
	if driver.CreatedAt == 0 {
		t.Error("eks driver: CreatedAt not seeded")
	}
	if driver.Metadata["ai.prompt"] != ws.Intent {
		t.Errorf("eks driver ai.prompt = %q, want the intent", driver.Metadata["ai.prompt"])
	}

	// Worktree symlinks materialized: <root>/<repo-name>/<flat-branch> -> real
	// path (branch slashes flattened to dashes — flat within a repo).
	link := filepath.Join(ws.Root, "terraform-infra", "feat-eks-1-30-upgrade")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("worktree symlink missing: %v", err)
	}
	wantTarget := filepath.Join(l.WorktreeRoot, "acme-platform/terraform-infra/feat-eks-1-30-upgrade")
	if target != wantTarget {
		t.Errorf("symlink %q -> %q, want %q", link, target, wantTarget)
	}

	// Single-repo workspace: RepoPath hint set to the code-root checkout.
	single := st.FindWorkspace("service-catalog-ownership")
	if single == nil {
		t.Fatal("service-catalog-ownership workspace missing")
	}
	wantRepoPath := filepath.Join(l.CodeRoot, "acme-platform/service-catalog")
	if single.RepoPath != wantRepoPath {
		t.Errorf("single-repo RepoPath = %q, want %q", single.RepoPath, wantRepoPath)
	}

	// A mix of attention across workspaces (driver-window level).
	var attn int
	for _, w := range st.Workspaces {
		if len(w.Windows) > 0 && w.Windows[0].Attention {
			attn++
		}
	}
	if attn == 0 || attn == len(st.Workspaces) {
		t.Errorf("attention workspaces = %d of %d, want a mix", attn, len(st.Workspaces))
	}
	// The last-active workspace must NOT be an attention one.
	if la := st.FindWorkspace(st.LastActiveSession); la != nil && len(la.Windows) > 0 && la.Windows[0].Attention {
		t.Error("lastActive points at an attention workspace; should reveal attention via M-s")
	}
}

func TestHydrate_WritesConfigWithIntegrations(t *testing.T) {
	l := hydrateAcme(t)
	data, err := os.ReadFile(filepath.Join(l.ConfigHome, "atelier", "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	cfg := string(data)
	for _, want := range []string{l.CodeRoot, l.WorkspaceRoot, "workspace_root", "[ai]", `provider = "claude"`, "[forge]", `provider = "mock"`, "[tools.lazygit]", `launch       = "atelier tools workspaces worktree-open lazygit"`} {
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
	t.Setenv("ATELIER_WORKSPACE_ROOT", filepath.Join(root, "ateliers"))

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
	var fixture map[string][]mock.ForgePR
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	// The mock forge queries the real worktree dir (repoQueryDir); the helm
	// ingress worktree must map to an open PR so the badge refresh reproduces
	// it offline.
	dir := filepath.Join(l.WorktreeRoot, "acme-platform/helm-charts/feat-bump-ingress-nginx")
	entries := fixture[dir]
	if len(entries) != 1 || entries[0].State != "open" {
		t.Errorf("fixture[%q] = %+v, want a single open PR", dir, entries)
	}
	if entries[0].Repo != "acme-platform/helm-charts" || entries[0].Branch != "feat/bump-ingress-nginx" {
		t.Errorf("fixture PR repo/branch = %q/%q, want acme-platform/helm-charts/feat/bump-ingress-nginx",
			entries[0].Repo, entries[0].Branch)
	}
	states := map[string]int{}
	for _, prs := range fixture {
		for _, pr := range prs {
			states[pr.State]++
		}
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
		"ATELIER_WORKSPACE_ROOT":  l.WorkspaceRoot,
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
