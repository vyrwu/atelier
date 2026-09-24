package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/vyrwu/atelier/internal/core"
)

func pr(num int, head, base string, minsOld int) core.PR {
	return core.PR{
		Repo: "wawafertility/infrastructure", Number: num, Title: "pr " + head,
		Head: head, Base: base, State: core.PROpen,
		CreatedAt: time.Now().Add(-time.Duration(minsOld) * time.Minute),
	}
}

// layout renders just the gutter column and the number, which is what the
// ordering tests are about. An unstacked row shows a dot-less blank.
func layout(entries []prEntry) string {
	var b strings.Builder
	for _, e := range entries {
		g := e.gutter
		if g == "" {
			g = "·"
		}
		fmt.Fprintf(&b, "%s #%d\n", g, e.pr.Number)
	}
	return b.String()
}

// The real four-deep chain from the healthlink space: an/healthlink-subnet sits
// on windows-vm-analysis, uat-host on subnet, hl7-bucket on uat-host. Creation
// order (3483, 3481, 3482) is not stack order, so newest-first would scramble it.
func TestStackOrderRealChain(t *testing.T) {
	got := stackOrder([]core.PR{
		pr(3483, "an/healthlink-subnet", "an/healthlink-windows-vm-analysis", 30),
		pr(3481, "an/healthlink-uat-host", "an/healthlink-subnet", 20),
		pr(3482, "an/healthlink-hl7-bucket", "an/healthlink-uat-host", 10),
		pr(3479, "an/healthlink-windows-vm-analysis", "main", 40),
	})
	want := "╭ #3479\n● #3483\n● #3481\n╰ #3482\n"
	if g := layout(got); g != want {
		t.Errorf("got:\n%swant:\n%s", g, want)
	}
}

// A two-PR stack is just its ends — no dot.
func TestStackOrderPair(t *testing.T) {
	got := stackOrder([]core.PR{
		pr(3477, "feat/customer-endpoint-health-alarms", "feat/vpn-tunnel-down-alarms", 5),
		pr(3476, "feat/vpn-tunnel-down-alarms", "main", 10),
	})
	if g, want := layout(got), "╭ #3476\n╰ #3477\n"; g != want {
		t.Errorf("got:\n%swant:\n%s", g, want)
	}
}

// Unstacked PRs keep the old rule — newest first, no gutter — and a stack stays
// contiguous among them, placed by its most recent member.
func TestStackOrderKeepsStacksTogether(t *testing.T) {
	got := stackOrder([]core.PR{
		pr(10, "solo-old", "main", 100),
		pr(20, "base", "main", 90),
		pr(30, "solo-new", "main", 1),
		pr(40, "tip", "base", 50),
	})
	want := "· #30\n╭ #20\n╰ #40\n· #10\n"
	if g := layout(got); g != want {
		t.Errorf("got:\n%swant:\n%s", g, want)
	}
}

// A base that isn't one of this space's PRs (the parent lives elsewhere, or was
// never tracked) makes the PR a root — it is not dropped, and the base still
// shows on the row so it isn't mistaken for a main-targeting PR.
func TestStackOrderDanglingBase(t *testing.T) {
	got := stackOrder([]core.PR{pr(7, "feat/x", "someone-elses-branch", 5)})
	if len(got) != 1 || got[0].gutter != "" {
		t.Fatalf("got %+v, want one ungutted row", got)
	}
}

// Base cycles can't come from git, but the walk must not hang if one appears.
func TestStackOrderCycle(t *testing.T) {
	done := make(chan []prEntry, 1)
	go func() {
		done <- stackOrder([]core.PR{pr(1, "a", "b", 10), pr(2, "b", "a", 5)})
	}()
	select {
	case got := <-done:
		if len(got) == 0 {
			t.Error("cycle swallowed every PR")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stackOrder hung on a base cycle")
	}
}

// The gutter, the base, and the selection all land on the row together.
func TestPRRowRendersStackAndBase(t *testing.T) {
	m := Model{w: 120, prPending: map[string]bool{}}
	e := prEntry{pr: pr(3483, "an/healthlink-subnet", "an/healthlink-windows-vm-analysis", 5), gutter: stackMid}
	line := ansiRe.ReplaceAllString(m.renderPRRow(e, false), "")

	if !strings.Contains(line, stackMid) {
		t.Errorf("no stack gutter: %q", line)
	}
	if !strings.Contains(line, "→ an/healthlink-windows-vm-analysis") {
		t.Errorf("base missing: %q", line)
	}
	if strings.Index(line, "#3483") > strings.Index(line, "→ an/") {
		t.Errorf("base should follow the number and title: %q", line)
	}

	// The base is shown for every PR, stacked or not — including main.
	plain := ansiRe.ReplaceAllString(m.renderPRRow(prEntry{pr: pr(3476, "feat/x", "main", 5)}, false), "")
	if !strings.Contains(plain, "→ main") {
		t.Errorf("base missing on an unstacked row: %q", plain)
	}
	if strings.ContainsAny(plain, stackTop+stackMid+stackEnd) {
		t.Errorf("unstacked row drew a gutter: %q", plain)
	}
}

// The base rides directly after the title, one space away, whatever the title's
// length — it reads as part of the entry rather than as a separate column.
func TestBaseFollowsTitle(t *testing.T) {
	m := Model{w: 120, prPending: map[string]bool{}}
	for _, title := range []string{"a", "a much longer pull request title here"} {
		p := pr(1, "x", "main", 5)
		p.Title = title
		line := ansiRe.ReplaceAllString(m.renderPRRow(prEntry{pr: p}, false), "")
		if !strings.Contains(line, title+" → main") {
			t.Errorf("base not one space after the title: %q", line)
		}
	}
	// A trailer never pushes in between them.
	p := pr(2, "y", "main", 5)
	p.Title, p.CI, p.Review, p.Comments = "titled", core.CIPass, core.ReviewApproved, 12
	if line := ansiRe.ReplaceAllString(m.renderPRRow(prEntry{pr: p}, false), ""); !strings.Contains(line, "titled → main") {
		t.Errorf("trailer split the title from its base: %q", line)
	}
}

// Navigation indexes the same order the view draws, stacks included.
func TestFlatPRsFollowsStackOrder(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
		pr(3483, "an/healthlink-subnet", "an/healthlink-windows-vm-analysis", 30),
		pr(3481, "an/healthlink-uat-host", "an/healthlink-subnet", 20),
		pr(3479, "an/healthlink-windows-vm-analysis", "main", 40),
	}}
	m := Model{w: 120, h: 40, screen: scrPRs, curWS: ws, filter: textinput.New(), wt: map[string][]core.Worktree{}, prPending: map[string]bool{}}

	var nums []int
	for _, p := range m.flatPRs() {
		nums = append(nums, p.Number)
	}
	if len(nums) != 3 || nums[0] != 3479 || nums[1] != 3483 || nums[2] != 3481 {
		t.Errorf("flatPRs order = %v, want base-first stack order", nums)
	}
}

// A cache written before head and base were recorded has neither, which is what
// the first frame of M-p renders while the query is out. Keying the walk on head
// refs let every one of those PRs through twice — the list appeared doubled
// until the refresh landed.
func TestStackOrderWithoutRefs(t *testing.T) {
	old := []core.PR{
		{Repo: "o/r", Number: 1, Title: "one"},
		{Repo: "o/r", Number: 2, Title: "two"},
		{Repo: "o/r", Number: 3, Title: "three"},
	}
	got := stackOrder(old)
	if len(got) != len(old) {
		t.Fatalf("stackOrder emitted %d entries for %d PRs", len(got), len(old))
	}
	seen := map[int]bool{}
	for _, e := range got {
		if seen[e.pr.Number] {
			t.Errorf("#%d rendered twice", e.pr.Number)
		}
		seen[e.pr.Number] = true
		if e.gutter != "" {
			t.Errorf("#%d drew a stack gutter with no refs to stack on", e.pr.Number)
		}
	}
}

// The same PR arriving twice (a cache merged badly, say) renders once.
func TestStackOrderDedupesByIdentity(t *testing.T) {
	dup := pr(7, "feat/x", "main", 5)
	if got := stackOrder([]core.PR{dup, dup}); len(got) != 1 {
		t.Errorf("duplicate PR rendered %d times", len(got))
	}
}

func prState(num int, st core.PRState, minsOld int) core.PR {
	p := pr(num, fmt.Sprintf("branch-%d", num), "main", minsOld)
	p.State = st
	return p
}

// The list reads in the order you act on it: open, then draft, then merged,
// then closed — newest first inside each.
func TestStackOrderSortsByState(t *testing.T) {
	got := stackOrder([]core.PR{
		prState(1, core.PRClosed, 1),
		prState(2, core.PRMerged, 2),
		prState(3, core.PRDraft, 3),
		prState(4, core.PROpen, 4),
		prState(5, core.PROpen, 50), // older open — still above every draft
	})
	var nums []int
	for _, e := range got {
		nums = append(nums, e.pr.Number)
	}
	want := []int{4, 5, 3, 2, 1}
	if !reflect.DeepEqual(nums, want) {
		t.Errorf("order = %v, want %v (open, draft, merged, closed)", nums, want)
	}
}

// A stack sorts by its most active member and stays in one piece — a merged
// base under an open tip must not be dragged down to the merged section.
func TestStackSortsByItsMostActiveMember(t *testing.T) {
	base := pr(10, "base", "main", 90)
	base.State = core.PRMerged
	tip := pr(11, "tip", "base", 80)
	tip.State = core.PROpen

	got := stackOrder([]core.PR{
		prState(20, core.PRDraft, 1), // newer, but a draft
		base, tip,
	})
	var nums []int
	for _, e := range got {
		nums = append(nums, e.pr.Number)
	}
	if !reflect.DeepEqual(nums, []int{10, 11, 20}) {
		t.Errorf("order = %v, want the stack (10,11) above the draft", nums)
	}
	if got[0].gutter != stackTop || got[1].gutter != stackEnd {
		t.Errorf("stack lost its gutter: %q %q", got[0].gutter, got[1].gutter)
	}
}

func TestPRStateRank(t *testing.T) {
	ranks := []core.PRState{core.PROpen, core.PRDraft, core.PRMerged, core.PRClosed}
	for i := 1; i < len(ranks); i++ {
		if prStateRank(ranks[i-1]) >= prStateRank(ranks[i]) {
			t.Errorf("%q should sort before %q", ranks[i-1], ranks[i])
		}
	}
}

// Repo sections follow the rows: a repo with something open comes before one
// that is entirely merged.
func TestRepoSectionsSortByState(t *testing.T) {
	quiet := prState(1, core.PRMerged, 1)
	quiet.Repo = "o/all-merged"
	active := prState(2, core.PROpen, 90)
	active.Repo = "o/has-open"

	ws := &core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{quiet, active}}
	m := Model{w: 120, curWS: ws, filter: textinput.New(), wt: map[string][]core.Worktree{}}
	groups := m.visiblePRs()
	if len(groups) != 2 || groups[0].repo != "o/has-open" {
		t.Errorf("section order = %q, %q; want the repo with an open PR first", groups[0].repo, groups[1].repo)
	}
}
