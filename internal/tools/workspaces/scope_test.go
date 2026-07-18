package workspaces

import (
	"strings"
	"testing"
)

// TestSessionFooter proves the footer reflects the pin state: the Pinned
// badge appears at the front (bottom-left) only while pinned, and the M-o
// hint only when a forge is active. The picker's initial render and
// _set-scope-pin's live change-footer share this, so this guards both
// against drift.
func TestSessionFooter(t *testing.T) {
	base := "M-x · delete  |  M-t · tag  |  M-p · pin  |  M-? · help"

	if got := sessionFooter(false, false); got != base {
		t.Errorf("unpinned/no-forge = %q, want %q", got, base)
	}
	if got := sessionFooter(false, true); !strings.Contains(got, "M-o · open PR") {
		t.Errorf("forge footer missing open-PR hint: %q", got)
	}

	pinned := sessionFooter(true, false)
	if !strings.HasPrefix(pinned, pinnedBadge) {
		t.Errorf("pinned footer must lead with the badge: %q", pinned)
	}
	if !strings.Contains(pinned, "Pinned") {
		t.Errorf("pinned footer must say Pinned: %q", pinned)
	}
	if strings.Contains(base, "Pinned") {
		t.Errorf("unpinned footer must not carry the badge: %q", base)
	}
}
