package workspaces

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/integration"
)

// TestForgeWorkspaceCwd_CanonicalNotPaneCwd guards bug #2: the forge badge
// must resolve a PR from the workspace's canonical worktree (repo+branch),
// never the live pane cwd. A bare "zsh" window (launcher-created, cwd pointing
// at an unrelated repo) has no matching worktree → no canonical dir → no
// badge, so it can't surface another workspace's PR.
func TestForgeWorkspaceCwd_CanonicalNotPaneCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ATELIER_WORKTREE_ROOT", root)

	if cwd, ok := forgeWorkspaceCwd("vyrwu/aws-athena", "zsh", ""); ok {
		t.Errorf("bare zsh window must have no canonical worktree, got %q", cwd)
	}

	wt := filepath.Join(root, "vyrwu/aws-athena", "test-wip")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := forgeWorkspaceCwd("vyrwu/aws-athena", "test-wip", ""); !ok || got != wt {
		t.Errorf("forgeWorkspaceCwd = (%q,%v), want (%q,true)", got, ok, wt)
	}
}

// TestForgeWorkspaceCwd_DottedRepoName guards the "cloudnativedenmark.dk"
// display/path bug: the worktree dir keeps the repo's real name (with the
// dot), but tmux mangles '.'→'_' in the session name. forgeWorkspaceCwd must
// build the canonical path from @repo_path (dot intact), NOT the mangled
// session — otherwise a dotted repo silently loses its forge badge.
func TestForgeWorkspaceCwd_DottedRepoName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ATELIER_WORKTREE_ROOT", root)

	repoPath := "/home/me/code/github/cloudnativedenmark/cloudnativedenmark.dk"
	mangledSession := "cloudnativedenmark/cloudnativedenmark_dk" // as tmux stores it
	wt := filepath.Join(root, "cloudnativedenmark/cloudnativedenmark.dk", "feat-x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := forgeWorkspaceCwd(mangledSession, "feat-x", repoPath)
	if !ok || got != wt {
		t.Errorf("forgeWorkspaceCwd = (%q,%v), want (%q,true) — must use @repo_path not the mangled session", got, ok, wt)
	}
}

func TestRepoSlugFromPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/home/me/code/github/cloudnativedenmark/cloudnativedenmark.dk", "cloudnativedenmark/cloudnativedenmark.dk"},
		{"/x/github/vyrwu/atelier", "vyrwu/atelier"},
		{"", ""},
		{"/", ""},
		{"toplevel", ""}, // no owner segment
	}
	for _, c := range cases {
		if got := repoSlugFromPath(c.in); got != c.want {
			t.Errorf("repoSlugFromPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestForgeStateRank(t *testing.T) {
	// open < draft < merged < closed < (none/unknown = last).
	cases := []struct {
		state string
		want  int
	}{
		{string(integration.ForgeOpen), 0},
		{string(integration.ForgeDraft), 1},
		{string(integration.ForgeMerged), 2},
		{string(integration.ForgeClosed), 3},
		{"", 4},
		{"garbage", 4},
		{"  open  ", 0}, // trimmed
	}
	for _, c := range cases {
		if got := forgeStateRank(c.state); got != c.want {
			t.Errorf("forgeStateRank(%q) = %d, want %d", c.state, got, c.want)
		}
	}
}

func TestForgeFresh(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fresh := now.Add(-time.Second).Unix() // within TTL
	stale := now.Add(-2 * forgeRefreshTTL).Unix()
	if !forgeFresh(now, itoa(fresh), forgeRefreshTTL) {
		t.Error("timestamp within TTL should be fresh")
	}
	if forgeFresh(now, itoa(stale), forgeRefreshTTL) {
		t.Error("timestamp beyond TTL should be stale")
	}
	// TTL is a parameter: the same timestamp is fresh under a longer TTL and
	// stale under a shorter one — the point of parameterizing it per caller.
	midAge := now.Add(-90 * time.Second).Unix()
	if forgeFresh(now, itoa(midAge), 1*time.Minute) {
		t.Error("90s old should be stale under a 1m TTL")
	}
	if !forgeFresh(now, itoa(midAge), 5*time.Minute) {
		t.Error("90s old should be fresh under a 5m TTL")
	}
	for _, bad := range []string{"", "notanumber", "0", "-5"} {
		if forgeFresh(now, bad, forgeRefreshTTL) {
			t.Errorf("forgeFresh(%q) should be stale", bad)
		}
	}
}

func TestParseForgeRow(t *testing.T) {
	s, w, ok := parseForgeRow("sess\twin\tdisplay")
	if !ok || s != "sess" || w != "win" {
		t.Errorf("parseForgeRow ok=%v s=%q w=%q", ok, s, w)
	}
	// Multi-line item: the recap lives on a second line inside field 3. The
	// {} bind passes the whole NUL-framed record; SplitN on tab must still
	// recover session/window from the first line.
	s, w, ok = parseForgeRow("sess\twin\ttime  name\n        · recap")
	if !ok || s != "sess" || w != "win" {
		t.Errorf("multi-line parseForgeRow ok=%v s=%q w=%q", ok, s, w)
	}
	for _, bad := range []string{"", "onlyone", "\twin", "sess\t"} {
		if _, _, ok := parseForgeRow(bad); ok {
			t.Errorf("parseForgeRow(%q) should not parse", bad)
		}
	}
}

func itoa(n int64) string {
	// tiny helper to avoid importing strconv just for the test table
	neg := n < 0
	if neg {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
