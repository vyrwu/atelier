package workspaces

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// TestFreshnessFresh guards the TTL throttle — the invariant that keeps the
// background loop from re-fetching (and, before it, spawning a detached child)
// on every tick. Within TTL → skip; beyond, empty, or unparseable → refetch.
func TestFreshnessFresh(t *testing.T) {
	now := time.Unix(2_000_000, 0)

	fresh := now.Add(-time.Second).Unix()
	if !freshnessFresh(now, strconv.FormatInt(fresh, 10)) {
		t.Error("timestamp within TTL should be fresh (skip refetch)")
	}

	stale := now.Add(-2 * freshnessRefreshTTL).Unix()
	if freshnessFresh(now, strconv.FormatInt(stale, 10)) {
		t.Error("timestamp beyond TTL should be stale (needs refetch)")
	}

	// Exactly TTL-old is stale — the throttle uses a strict `<`.
	edge := now.Add(-freshnessRefreshTTL).Unix()
	if freshnessFresh(now, strconv.FormatInt(edge, 10)) {
		t.Error("exactly TTL-old should be stale (strict <)")
	}

	for _, bad := range []string{"", "notanumber", "0", "-5", "   "} {
		if freshnessFresh(now, bad) {
			t.Errorf("freshnessFresh(%q) should be stale", bad)
		}
	}
}

// TestProcessAlive covers the singleton-lock liveness probe: the current
// process is alive; non-positive pids are never alive (so a stale/blank lock
// value can't wedge a fresh loop out).
func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	for _, dead := range []int{0, -1} {
		if processAlive(dead) {
			t.Errorf("processAlive(%d) should be false", dead)
		}
	}
}
