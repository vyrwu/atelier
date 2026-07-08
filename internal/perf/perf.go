// Package perf is atelier's always-on operation timing. It answers
// "why was that slow?" after the fact: a user hits a sluggish
// session-list once, comes back, and the debug.log already holds a
// `perf` record breaking the operation down by where the wall-clock
// went (tmux round-trips vs git vs atelier's own CPU).
//
// Model: atelier is short-lived command dispatch, not a daemon, so
// timing is per-process. External-call cost is attributed by category
// via package-level counters that the tmux and git choke points feed
// (Add). A Span snapshots those counters at Start and diffs them at
// End, so every operation reports its own total plus the tmux/git/self
// split without threading state through call sites.
//
// Emitted line (via debuglog, category `perf`):
//
//	perf session-list dur=1240ms tmux=8/902ms git=45/210ms self=128ms
//
// self = total − Σ(category time): the part that is atelier's own
// work (parsing, sorting, formatting), not waiting on a subprocess.
package perf

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vyrwu/atelier/internal/debuglog"
)

var (
	mu      sync.Mutex
	calls   = map[string]int{}
	elapsed = map[string]time.Duration{}
)

// Add records one completed external call of the given category
// (e.g. "tmux", "git") and how long it took. Called from the choke
// points; safe for concurrent use. Cheap enough to leave always-on.
func Add(category string, d time.Duration) {
	mu.Lock()
	calls[category]++
	elapsed[category] += d
	mu.Unlock()
}

func snapshot() (map[string]int, map[string]time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	c := make(map[string]int, len(calls))
	e := make(map[string]time.Duration, len(elapsed))
	for k, v := range calls {
		c[k] = v
	}
	for k, v := range elapsed {
		e[k] = v
	}
	return c, e
}

// Span measures one logical operation. Create with Start, close with
// End (typically deferred). A nil *Span is a no-op, so callers never
// need to nil-check.
type Span struct {
	name    string
	start   time.Time
	calls0  map[string]int
	elapse0 map[string]time.Duration
}

// Start begins timing an operation, snapshotting the current
// external-call counters so End can report the delta incurred during
// the span.
func Start(name string) *Span {
	c, e := snapshot()
	return &Span{name: name, start: time.Now(), calls0: c, elapse0: e}
}

// End stops the span and emits its perf record to the debug log.
func (s *Span) End() {
	if s == nil {
		return
	}
	total := time.Since(s.start)
	c, e := snapshot()
	deltaCalls := make(map[string]int, len(c))
	deltaElapsed := make(map[string]time.Duration, len(e))
	for k, v := range c {
		if d := v - s.calls0[k]; d != 0 {
			deltaCalls[k] = d
		}
	}
	for k, v := range e {
		if d := v - s.elapse0[k]; d != 0 {
			deltaElapsed[k] = d
		}
	}
	debuglog.LogPerf(formatFields(s.name, total, deltaCalls, deltaElapsed))
}

// formatFields renders the perf line body. Pure: takes the measured
// total plus per-category call counts and elapsed time, returns the
// `<name> dur=… <cat>=<calls>/<ms>ms … self=…ms` string. Categories
// are sorted for deterministic, greppable output; self is total minus
// all attributed category time, clamped at zero.
func formatFields(name string, total time.Duration, calls map[string]int, elapsed map[string]time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s dur=%dms", name, total.Milliseconds())

	cats := make([]string, 0, len(elapsed))
	var attributed time.Duration
	for cat := range elapsed {
		cats = append(cats, cat)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		fmt.Fprintf(&b, " %s=%d/%dms", cat, calls[cat], elapsed[cat].Milliseconds())
		attributed += elapsed[cat]
	}

	self := total - attributed
	if self < 0 {
		self = 0
	}
	fmt.Fprintf(&b, " self=%dms", self.Milliseconds())
	return b.String()
}
