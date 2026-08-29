package workspaces

import (
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

func TestSummaryInputHash(t *testing.T) {
	prs := []statestore.PR{
		{Repo: "o/a", Number: 1, State: "open", CI: "pass", ReviewDecision: "approved"},
		{Repo: "o/b", Number: 2, State: "draft", CI: "pending"},
	}

	base := summaryInputHash("recap", prs)

	// Identical inputs → identical hash.
	if again := summaryInputHash("recap", prs); again != base {
		t.Errorf("hash not stable: %q vs %q", base, again)
	}

	// Reordered PR slice → SAME hash (order-independence guards against the
	// register-appends vs sweep-sorts divergence triggering a spurious call).
	reordered := []statestore.PR{prs[1], prs[0]}
	if got := summaryInputHash("recap", reordered); got != base {
		t.Errorf("hash is order-sensitive: %q (reordered) != %q", got, base)
	}

	// Changed recap → different hash.
	if got := summaryInputHash("different recap", prs); got == base {
		t.Error("hash unchanged after recap change")
	}

	// Changed PR CI → different hash.
	changed := []statestore.PR{
		{Repo: "o/a", Number: 1, State: "open", CI: "fail", ReviewDecision: "approved"},
		{Repo: "o/b", Number: 2, State: "draft", CI: "pending"},
	}
	if got := summaryInputHash("recap", changed); got == base {
		t.Error("hash unchanged after PR CI change")
	}

	// Changed PR state → different hash.
	merged := []statestore.PR{
		{Repo: "o/a", Number: 1, State: "merged", CI: "pass", ReviewDecision: "approved"},
		{Repo: "o/b", Number: 2, State: "draft", CI: "pending"},
	}
	if got := summaryInputHash("recap", merged); got == base {
		t.Error("hash unchanged after PR state change")
	}

	// Empty inputs are stable + distinct from populated.
	if summaryInputHash("", nil) == base {
		t.Error("empty hash collides with populated hash")
	}
}
