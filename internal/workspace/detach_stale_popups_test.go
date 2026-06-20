package workspace

import "testing"

// TestShouldDetachPopupClient locks in the workspace-scoped popup
// dismissal logic. After LandOuter switches the outer client to a new
// workspace, popup-clients whose backing session is scoped to a
// DIFFERENT workspace must be detached (the bug: claude popup from
// workspace A staying visible after M-s'ing to workspace B). Popups
// scoped to the SAME workspace are kept.
func TestShouldDetachPopupClient(t *testing.T) {
	cases := []struct {
		name           string
		clientSession  string
		keepSidWid     string
		wantDetach     bool
	}{
		// Same-workspace popup → KEEP.
		{
			name:          "claude popup for same (sid, wid) is kept",
			clientSession: "_atelier_claude_2_2",
			keepSidWid:    "2_2",
			wantDetach:    false,
		},
		{
			name:          "popupshell on same workspace kept",
			clientSession: "_atelier_popupshell_2_2",
			keepSidWid:    "2_2",
			wantDetach:    false,
		},

		// Different-workspace popup → DETACH (the bug case).
		{
			name:          "claude popup for different workspace detached",
			clientSession: "_atelier_claude_2_2",
			keepSidWid:    "1_1",
			wantDetach:    true,
		},
		{
			name:          "popup scoped to different window on same session detached",
			clientSession: "_atelier_claude_2_3",
			keepSidWid:    "2_5",
			wantDetach:    true,
		},

		// Session-global popups (k9s, pgcli, pgcenter) have no
		// sid/wid suffix — they're singletons across all
		// workspaces. Treat as stale: M-s to ANY workspace should
		// dismiss them (the user is moving away from where they
		// invoked the popup).
		{
			name:          "session-global k9s popup detached",
			clientSession: "_atelier_k9s",
			keepSidWid:    "2_2",
			wantDetach:    true,
		},

		// Non-atelier sessions are off-limits.
		{
			name:          "foreign user session never touched",
			clientSession: "vyrwu/atelier",
			keepSidWid:    "2_2",
			wantDetach:    false,
		},
		{
			name:          "foreign session NOT touched even with empty keep",
			clientSession: "scratch",
			keepSidWid:    "",
			wantDetach:    false,
		},

		// Empty keepSidWid (resolution failed) → fall back to
		// detach-all-atelier. Better to dismiss too much than
		// leave a stale popup whose scope we don't know.
		{
			name:          "empty keep detaches atelier popup",
			clientSession: "_atelier_claude_2_2",
			keepSidWid:    "",
			wantDetach:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDetachPopupClient(tc.clientSession, tc.keepSidWid); got != tc.wantDetach {
				t.Errorf("shouldDetachPopupClient(%q, %q) = %v, want %v",
					tc.clientSession, tc.keepSidWid, got, tc.wantDetach)
			}
		})
	}
}

// TestStripEqualsPrefix locks in the `display-message`-vs-`switch-client`
// target-format quirk: switch-client accepts `=session`; display-message
// returns empty for that form. Stripping is required for resolution to
// work.
func TestStripEqualsPrefix(t *testing.T) {
	cases := map[string]string{
		"=ws-a":   "ws-a",
		"=ws:1":   "ws:1",
		"ws-a":    "ws-a",
		"@2":      "@2",
		"":        "",
	}
	for in, want := range cases {
		if got := stripEqualsPrefix(in); got != want {
			t.Errorf("stripEqualsPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
