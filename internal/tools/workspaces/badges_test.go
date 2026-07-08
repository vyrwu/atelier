package workspaces

import "testing"

func TestKeyToFzf(t *testing.T) {
	cases := map[string]string{
		"M-o":   "alt-o",
		"M-x":   "alt-x",
		"C-o":   "ctrl-o",
		"enter": "enter", // pass-through
		"esc":   "esc",
		"":      "",
	}
	for in, want := range cases {
		if got := keyToFzf(in); got != want {
			t.Errorf("keyToFzf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBadgeOptionKeys(t *testing.T) {
	specs := []badgeSpec{
		{tool: "a"}, // Option empty — still listed as-is by badgeOptionKeys
	}
	specs[0].Option = "@a_badge"
	specs = append(specs, badgeSpec{tool: "b"})
	specs[1].Option = "@b_badge"
	got := badgeOptionKeys(specs)
	if len(got) != 2 || got[0] != "@a_badge" || got[1] != "@b_badge" {
		t.Errorf("badgeOptionKeys = %v, want [@a_badge @b_badge]", got)
	}
}

func TestBadgeSorts(t *testing.T) {
	var a, b badgeSpec
	a.Option, a.SortOption, a.SortOrder = "@ghpr_badge", "@ghpr_state", []string{"open", "draft", "merged", "closed"}
	b.Option = "@other_badge" // no SortOption → contributes no sort
	sorts := badgeSorts([]badgeSpec{a, b})
	if len(sorts) != 1 {
		t.Fatalf("badgeSorts len = %d, want 1 (only the provider with SortOrder)", len(sorts))
	}
	s := sorts[0]
	if s.option != "@ghpr_state" {
		t.Errorf("sort option = %q, want @ghpr_state", s.option)
	}
	cases := map[string]int{
		"open":    0,
		"draft":   1,
		"merged":  2,
		"closed":  3,
		" open ":  0, // trimmed
		"":        4, // unset → last
		"unknown": 4, // not in order → last
	}
	for v, want := range cases {
		if got := s.rankOf(v); got != want {
			t.Errorf("rankOf(%q) = %d, want %d", v, got, want)
		}
	}
	if keys := sortOptionKeys(sorts); len(keys) != 1 || keys[0] != "@ghpr_state" {
		t.Errorf("sortOptionKeys = %v, want [@ghpr_state]", keys)
	}
}

// discoverBadges must not fork tool binaries under a test socket.
func TestDiscoverBadges_TestSocketReturnsNil(t *testing.T) {
	t.Setenv("ATELIER_TMUX_SOCKET", "atelier-test-abc123")
	if got := discoverBadges(); got != nil {
		t.Errorf("discoverBadges under test socket = %v, want nil", got)
	}
}
