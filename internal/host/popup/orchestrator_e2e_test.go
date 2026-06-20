//go:build e2e

package popup_test

import (
	"testing"

	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/testtmux"
)

func TestCleanup_RemovesOrphanedWorkspaceScopedPopup(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("workspace") // sid=$0 wid=@0 typically

	// Create a "popup" session whose declared parent (sid=$5, wid=@99) doesn't exist.
	if err := srv.Client.NewSession("_atelier_lazygit_5_99", true); err != nil {
		t.Fatalf("NewSession orphan: %v", err)
	}

	if err := hostpopup.CleanupOrphanedPopups(srv.Client); err != nil {
		t.Fatalf("CleanupOrphanedPopups: %v", err)
	}

	for _, s := range srv.Sessions() {
		if s == "_atelier_lazygit_5_99" {
			t.Fatalf("orphan popup not cleaned up: %v", srv.Sessions())
		}
	}
}

func TestCleanup_PreservesLivePopups(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("workspace")

	// Find the real session_id / window_id of the workspace we just created
	out, err := srv.Client.Run("list-windows", "-a", "-F", "#{session_id} #{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var sid, wid string
	if _, err := scan(string(out), &sid, &wid); err != nil {
		t.Fatalf("scan list-windows output: %v (out=%q)", err, out)
	}
	digitsOnly := func(s string) string {
		out := make([]rune, 0)
		for _, r := range s {
			if r >= '0' && r <= '9' {
				out = append(out, r)
			}
		}
		return string(out)
	}
	popupName := "_atelier_lazygit_" + digitsOnly(sid) + "_" + digitsOnly(wid)
	if err := srv.Client.NewSession(popupName, true); err != nil {
		t.Fatalf("NewSession live popup: %v", err)
	}

	if err := hostpopup.CleanupOrphanedPopups(srv.Client); err != nil {
		t.Fatalf("CleanupOrphanedPopups: %v", err)
	}

	found := false
	for _, s := range srv.Sessions() {
		if s == popupName {
			found = true
		}
	}
	if !found {
		t.Fatalf("live popup was cleaned up unexpectedly: sessions=%v", srv.Sessions())
	}
}

func TestCleanup_ClearsChainWhenNoPopupsRemain(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("workspace")

	// Set up: a stale chain and one orphan
	if err := srv.Client.SetGlobalOption(state.OptOuterPane, "%5"); err != nil {
		t.Fatalf("SetGlobalOption: %v", err)
	}
	if err := srv.Client.NewSession("_atelier_lazygit_5_99", true); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := hostpopup.CleanupOrphanedPopups(srv.Client); err != nil {
		t.Fatalf("CleanupOrphanedPopups: %v", err)
	}

	pane, _ := srv.Client.ShowGlobalOption(state.OptOuterPane)
	if pane != "" {
		t.Fatalf("expected outer-pane chain cleared after orphan cleanup, got %q", pane)
	}
}

func scan(s string, sid, wid *string) (int, error) {
	// Find first non-empty line and split into two fields.
	for _, line := range splitLines(s) {
		fields := splitFields(line)
		if len(fields) >= 2 {
			*sid = fields[0]
			*wid = fields[1]
			return 2, nil
		}
	}
	return 0, errEmpty
}

var errEmpty = scanErr("no usable lines")

type scanErr string

func (e scanErr) Error() string { return string(e) }

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func splitFields(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\t' {
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
