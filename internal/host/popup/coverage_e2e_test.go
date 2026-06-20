//go:build e2e

// Coverage tests for the cascade cleanup of popup sessions when their
// parent session/window goes away. Matches bash tmux_cleanup_popups
// behavior, including recognizing BOTH atelier (`_atelier_*`) and
// bash (`_popup_`, `_claudepop_`, `_k8spop_`, `_awspop_`, `_lazygitpop_`)
// session-name prefixes.
package popup_test

import (
	"testing"

	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestCleanup_BashPopupPrefixes_AlsoCleaned asserts that orphaned
// bash-prefix popup sessions (`_popup_`, `_claudepop_`, etc.) get
// killed alongside atelier-prefix ones. atelier and bash users
// coexist on the same socket; cleanup must handle both.
func TestCleanup_BashPopupPrefixes_AlsoCleaned(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("workspace")

	// Create orphan popups for both naming schemes (declared parent
	// sid=$99/wid=@99 doesn't exist).
	for _, name := range []string{
		"_atelier_claude_99_99",
		"_claudepop_99_99",
		"_popup_99_99",
		"_lazygitpop_99_99",
	} {
		if err := srv.Client.NewSession(name, true); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	if err := hostpopup.CleanupOrphanedPopups(srv.Client); err != nil {
		t.Fatalf("CleanupOrphanedPopups: %v", err)
	}

	got := srv.Sessions()
	for _, want_gone := range []string{
		"_atelier_claude_99_99",
		"_claudepop_99_99",
		"_popup_99_99",
		"_lazygitpop_99_99",
	} {
		for _, s := range got {
			if s == want_gone {
				t.Errorf("orphan %s not cleaned up; sessions=%v", want_gone, got)
			}
		}
	}
}

// TestCleanup_BashPrefixes_LivePopupsPreserved asserts that a
// bash-prefix popup whose parent window IS alive doesn't get killed.
func TestCleanup_BashPrefixes_LivePopupsPreserved(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("workspace")

	// Find the real parent sid+wid digits so the live-popup name is
	// computed correctly.
	out, err := srv.Client.Run("list-windows", "-a", "-F",
		"#{session_id} #{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var sidDigits, widDigits string
	for _, c := range string(out) {
		if c >= '0' && c <= '9' {
			if sidDigits == "" {
				sidDigits = string(c)
			} else if widDigits == "" {
				widDigits = string(c)
				break
			}
		}
		if c == ' ' || c == '\t' || c == '\n' {
			continue
		}
	}
	// Better: parse the line as "$N @M".
	var sid, wid string
	if n := scanIDs(string(out), &sid, &wid); n != 2 {
		t.Fatalf("could not parse window IDs from %q", out)
	}
	sidDigits = onlyDigits(sid)
	widDigits = onlyDigits(wid)
	livePopup := "_claudepop_" + sidDigits + "_" + widDigits

	if err := srv.Client.NewSession(livePopup, true); err != nil {
		t.Fatalf("create live popup: %v", err)
	}

	if err := hostpopup.CleanupOrphanedPopups(srv.Client); err != nil {
		t.Fatalf("CleanupOrphanedPopups: %v", err)
	}

	found := false
	for _, s := range srv.Sessions() {
		if s == livePopup {
			found = true
		}
	}
	if !found {
		t.Fatalf("live popup %q wrongly cleaned up: sessions=%v",
			livePopup, srv.Sessions())
	}
}

func scanIDs(s string, sid, wid *string) int {
	parts := coverageSplitFields(s)
	if len(parts) < 2 {
		return 0
	}
	*sid = parts[0]
	*wid = parts[1]
	return 2
}

func coverageSplitFields(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

func onlyDigits(s string) string {
	var out []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}
