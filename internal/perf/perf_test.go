package perf

import (
	"testing"
	"time"
)

// TestFormatFields_Breakdown locks the perf-line contract: category
// timings are rendered as <cat>=<calls>/<ms>ms in sorted order, and
// self is total minus all attributed category time. This is the line
// a human (or Claude) reads to answer "why was session-list slow?".
func TestFormatFields_Breakdown(t *testing.T) {
	got := formatFields(
		"session-list",
		1240*time.Millisecond,
		map[string]int{"tmux": 8, "git": 45},
		map[string]time.Duration{"tmux": 902 * time.Millisecond, "git": 210 * time.Millisecond},
	)
	// git before tmux (sorted); self = 1240 - 902 - 210 = 128.
	want := "session-list dur=1240ms git=45/210ms tmux=8/902ms self=128ms"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// TestFormatFields_NoCategories: an operation that made no external
// calls attributes its whole duration to self.
func TestFormatFields_NoCategories(t *testing.T) {
	got := formatFields("noop", 5*time.Millisecond, nil, nil)
	want := "noop dur=5ms self=5ms"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// TestFormatFields_SelfClamped: if attributed category time exceeds
// the measured total (concurrent calls inflating a shared counter),
// self must clamp at 0 rather than print a negative.
func TestFormatFields_SelfClamped(t *testing.T) {
	got := formatFields(
		"racy",
		10*time.Millisecond,
		map[string]int{"tmux": 2},
		map[string]time.Duration{"tmux": 30 * time.Millisecond},
	)
	want := "racy dur=10ms tmux=2/30ms self=0ms"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// TestSpan_AttributesAddDuringSpan: a Span reports only the calls
// recorded between Start and End, not counters accrued before it.
func TestSpan_AttributesAddDuringSpan(t *testing.T) {
	resetCounters(t)
	Add("git", 100*time.Millisecond) // before the span — must be excluded

	sp := Start("op")
	Add("tmux", 50*time.Millisecond)
	Add("tmux", 25*time.Millisecond)

	c, e := snapshot()
	dCalls := c["tmux"] - sp.calls0["tmux"]
	dElapsed := e["tmux"] - sp.elapse0["tmux"]
	if dCalls != 2 {
		t.Fatalf("tmux calls delta = %d, want 2", dCalls)
	}
	if dElapsed != 75*time.Millisecond {
		t.Fatalf("tmux elapsed delta = %v, want 75ms", dElapsed)
	}
	if got := e["git"] - sp.elapse0["git"]; got != 0 {
		t.Fatalf("git delta during span = %v, want 0 (pre-span call leaked in)", got)
	}
}

func resetCounters(t *testing.T) {
	t.Helper()
	mu.Lock()
	calls = map[string]int{}
	elapsed = map[string]time.Duration{}
	mu.Unlock()
}
