package workspaces

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

var ansiRE = regexp.MustCompile(`\033\[[0-9;]*m`)

// visibleWidth counts display cells, ignoring ANSI SGR sequences. The Nerd
// Font PR glyphs are single-cell.
func visibleWidth(s string) int { return len([]rune(ansiRE.ReplaceAllString(s, ""))) }

// TestFormatSessionDisplay_BadgeOrder is the output-level guard that was
// missing when PR #11 silently moved the badge after the workspace name (the
// helper-level test that would have caught it was deleted in the same commit).
// It asserts the visible column order — time < icon < badge < workspace — on
// the fully assembled row, so it survives any refactor of how the badge is
// produced.
func TestFormatSessionDisplay_BadgeOrder(t *testing.T) {
	badge := forgeBadgeColumn(renderForgeBadge(string(integration.ForgeOpen)))
	glyph := ansiRE.ReplaceAllString(badge, "")[:1] // the PR glyph rune, ANSI-stripped
	row := formatSessionDisplay("1d ", "O ", badge, "", "36", "myrepo", "mybranch", " · recap")
	vis := ansiRE.ReplaceAllString(row, "")

	iIcon := strings.Index(vis, "O")
	iBadge := strings.Index(vis, glyph)
	iName := strings.Index(vis, "myrepo")
	if iIcon < 0 || iBadge <= iIcon || iName <= iBadge {
		t.Errorf("column order wrong: icon@%d badge@%d name@%d in %q (want icon<badge<name)",
			iIcon, iBadge, iName, vis)
	}
}

// TestForgeBadgeColumn_FixedWidth locks the layout fix: the forge badge sits
// in a fixed 2-cell slot (between the attention icon and the workspace name)
// so the name column stays aligned whether or not a workspace has a PR. A
// present badge must not keep renderForgeBadge's leading space (the slot sits
// flush after the icon).
func TestForgeBadgeColumn_FixedWidth(t *testing.T) {
	empty := forgeBadgeColumn("")
	if visibleWidth(empty) != 2 {
		t.Errorf("empty slot width = %d, want 2 (%q)", visibleWidth(empty), empty)
	}
	for _, st := range []integration.ForgeState{
		integration.ForgeOpen, integration.ForgeDraft,
		integration.ForgeMerged, integration.ForgeClosed,
	} {
		got := forgeBadgeColumn(renderForgeBadge(string(st)))
		if visibleWidth(got) != 2 {
			t.Errorf("%s slot width = %d, want 2 (%q)", st, visibleWidth(got), got)
		}
		if len(got) > 0 && got[0] == ' ' {
			t.Errorf("%s slot should not keep a leading space: %q", st, got)
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

func TestRenderForgeBadge(t *testing.T) {
	// Each real state renders a non-empty spliceable token (leading space +
	// ANSI); none/unknown renders empty so the picker shows no badge.
	for _, st := range []integration.ForgeState{
		integration.ForgeOpen, integration.ForgeDraft,
		integration.ForgeMerged, integration.ForgeClosed,
	} {
		if got := renderForgeBadge(string(st)); got == "" {
			t.Errorf("renderForgeBadge(%q) should be non-empty", st)
		}
	}
	for _, empty := range []string{"", "garbage"} {
		if got := renderForgeBadge(empty); got != "" {
			t.Errorf("renderForgeBadge(%q) = %q, want empty", empty, got)
		}
	}
}

func TestForgeFresh(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fresh := now.Add(-time.Second).Unix() // within TTL
	stale := now.Add(-2 * forgeRefreshTTL).Unix()
	if !forgeFresh(now, itoa(fresh)) {
		t.Error("timestamp within TTL should be fresh")
	}
	if forgeFresh(now, itoa(stale)) {
		t.Error("timestamp beyond TTL should be stale")
	}
	for _, bad := range []string{"", "notanumber", "0", "-5"} {
		if forgeFresh(now, bad) {
			t.Errorf("forgeFresh(%q) should be stale", bad)
		}
	}
}

func TestParseForgeRow(t *testing.T) {
	s, w, ok := parseForgeRow("sess\twin\tdisplay")
	if !ok || s != "sess" || w != "win" {
		t.Errorf("parseForgeRow ok=%v s=%q w=%q", ok, s, w)
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
