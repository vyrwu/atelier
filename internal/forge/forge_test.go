package forge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/core"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		state   string
		isDraft bool
		want    core.PRState
	}{
		{"OPEN", false, core.PROpen},
		{"open", true, core.PRDraft}, // case-insensitive + draft
		{"MERGED", false, core.PRMerged},
		{"MERGED", true, core.PRMerged}, // terminal wins over the draft flag
		{"CLOSED", false, core.PRClosed},
		{"CLOSED", true, core.PRClosed}, // a closed draft is Closed, not Draft (the wedge bug)
	}
	for _, c := range cases {
		if got := classify(c.state, c.isDraft); got != c.want {
			t.Errorf("classify(%q, %v) = %q, want %q", c.state, c.isDraft, got, c.want)
		}
	}
}

func TestClassifyRollup(t *testing.T) {
	cases := map[string]core.CIState{
		"SUCCESS":  core.CIPass,
		"FAILURE":  core.CIFail,
		"ERROR":    core.CIFail,
		"PENDING":  core.CIPending,
		"EXPECTED": core.CIPending, // a required check that hasn't reported yet
		"":         core.CINone,    // no checks on the head commit
		"WAT":      core.CINone,
		"success":  core.CIPass, // case-insensitive, like the state mapping
	}
	for in, want := range cases {
		if got := classifyRollup(in); got != want {
			t.Errorf("classifyRollup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyReview(t *testing.T) {
	cases := map[string]core.Review{
		"APPROVED":          core.ReviewApproved,
		"CHANGES_REQUESTED": core.ReviewChanges,
		"REVIEW_REQUIRED":   core.ReviewRequired,
		"":                  core.ReviewNone,
		"SOMETHING_ELSE":    core.ReviewNone,
	}
	for in, want := range cases {
		if got := classifyReview(in); got != want {
			t.Errorf("classifyReview(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNeedsReopen(t *testing.T) {
	cases := []struct {
		cur, target core.PRState
		want        bool
	}{
		{core.PRClosed, core.PROpen, true},
		{core.PRClosed, core.PRClosed, false},
		{core.PROpen, core.PRClosed, false},
		{core.PRDraft, core.PROpen, false},
		{core.PRMerged, core.PROpen, false},
	}
	for _, c := range cases {
		if got := needsReopen(c.cur, c.target); got != c.want {
			t.Errorf("needsReopen(%q, %q) = %v, want %v", c.cur, c.target, got, c.want)
		}
	}
}

func TestPRTransitionStep(t *testing.T) {
	cases := []struct {
		cur, target core.PRState
		want        []string
	}{
		{core.PROpen, core.PRClosed, []string{"close"}},
		{core.PRDraft, core.PRClosed, []string{"close"}},
		{core.PRClosed, core.PRClosed, nil}, // already closed
		{core.PRDraft, core.PROpen, []string{"ready"}},
		{core.PROpen, core.PROpen, nil},   // already open
		{core.PRClosed, core.PROpen, nil}, // reopen (handled by caller) leaves nothing here
	}
	for _, c := range cases {
		if got := prTransitionStep(c.cur, c.target); !reflect.DeepEqual(got, c.want) {
			t.Errorf("prTransitionStep(%q, %q) = %v, want %v", c.cur, c.target, got, c.want)
		}
	}
}

// The query is what makes a refresh one round trip instead of one listing per
// repo: an alias per branch and per registered PR, all in a single document.
func TestBuildQuery(t *testing.T) {
	q := buildQuery([]lookups{
		{
			branches: []branchTarget{
				{repo: "owner/repo", branch: "feat/x"},
				{repo: "other/thing", branch: "main"},
			},
			numbers: []numberTarget{{repo: "owner/repo", number: 100}},
		},
		{branches: []branchTarget{{repo: "a/b", branch: "y"}}},
	})

	for _, want := range []string{
		`s0b0: repository(owner: "owner", name: "repo")`,
		`headRefName: "feat/x"`,
		`s0b1: repository(owner: "other", name: "thing")`,
		`s0n0: repository(owner: "owner", name: "repo") { pullRequest(number: 100)`,
		`s1b0: repository(owner: "a", name: "b")`, // every space, one round trip
		"fragment pr on PullRequest",
		"statusCheckRollup { state }", // the aggregate, not every check run
		"comments { totalCount }",     // a count, not the bodies
		"isInMergeQueue",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
}

// A branch name is user data that lands inside a GraphQL string literal.
func TestBuildQueryEscapes(t *testing.T) {
	q := buildQuery([]lookups{{branches: []branchTarget{{repo: "o/r", branch: `we"ird\name`}}}})
	if !strings.Contains(q, `headRefName: "we\"ird\\name"`) {
		t.Errorf("branch not escaped into the literal:\n%s", q)
	}
	if strings.Count(q, "headRefName:") != 1 { // the fragment names the field too
		t.Errorf("escaping broke the query shape:\n%s", q)
	}
}

func TestAliasRepo(t *testing.T) {
	branches := []branchTarget{{repo: "o/a", branch: "x"}, {repo: "o/b", branch: "y"}}
	numbers := []numberTarget{{repo: "o/c", number: 7}}
	cases := map[string]string{
		"b0":  "o/a",
		"b1":  "o/b",
		"n0":  "o/c",
		"b9":  "", // out of range
		"z0":  "",
		"b":   "",
		"bxx": "",
		"":    "",
	}
	for alias, want := range cases {
		if got := aliasRepo(alias, branches, numbers); got != want {
			t.Errorf("aliasRepo(%q) = %q, want %q", alias, got, want)
		}
	}
}

// Registered PRs are looked up by NUMBER, which is the whole point: a PR whose
// worktree (or branch) is gone used to be copied forward from the cache and
// never re-queried, so its CI and review froze at whatever it was registered
// with.
func TestNumberTargetsCoverRegisteredPRs(t *testing.T) {
	got := numberTargets([]core.PR{
		{Repo: "o/r", Number: 1, Registered: true},
		{Repo: "o/r", Number: 2},                   // discovered by branch — no number lookup needed
		{Repo: "o/r", Number: 1, Registered: true}, // duplicate
		{Repo: "", Number: 3, Registered: true},    // unusable
		{Repo: "o/r", Number: 0, Registered: true}, // unusable
		{Repo: "a/b", Number: 9, Registered: true},
		{Repo: "a/b", Number: 8, Registered: true, State: core.PRMerged}, // terminal — never re-asked
		{Repo: "a/b", Number: 7, Registered: true, State: core.PRClosed}, // can be reopened
	})
	want := []numberTarget{{repo: "a/b", number: 7}, {repo: "a/b", number: 9}, {repo: "o/r", number: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("numberTargets = %+v, want %+v", got, want)
	}
}

func TestSplitRepo(t *testing.T) {
	cases := []struct{ in, owner, name string }{
		{"owner/repo", "owner", "repo"},
		{"repo", "", "repo"},
		{"", "", ""},
	}
	for _, c := range cases {
		o, n := splitRepo(c.in)
		if o != c.owner || n != c.name {
			t.Errorf("splitRepo(%q) = (%q, %q), want (%q, %q)", c.in, o, n, c.owner, c.name)
		}
	}
}

// toPR is the whole normalization: the row's fields, the rollup verdict, and the
// comment count.
func TestPRNodeToPR(t *testing.T) {
	var n prNode
	body := `{
	  "number": 100, "title": "a title", "url": "https://github.com/o/r/pull/100",
	  "state": "OPEN", "isDraft": true, "createdAt": "2026-01-02T03:04:05Z",
	  "headRefName": "feat/x", "repository": {"nameWithOwner": "o/r"},
	  "reviewDecision": "CHANGES_REQUESTED",
	  "comments": {"totalCount": 12},
	  "commits": {"nodes": [{"commit": {"statusCheckRollup": {"state": "FAILURE"}}}]}
	}`
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		t.Fatal(err)
	}
	pr := n.toPR("")
	if pr.Repo != "o/r" || pr.Number != 100 || pr.Title != "a title" {
		t.Errorf("identity wrong: %+v", pr)
	}
	if pr.State != core.PRDraft {
		t.Errorf("state = %q, want draft", pr.State)
	}
	if pr.CI != core.CIFail {
		t.Errorf("ci = %q, want fail", pr.CI)
	}
	if pr.Review != core.ReviewChanges {
		t.Errorf("review = %q, want changes_requested", pr.Review)
	}
	if pr.Comments != 12 {
		t.Errorf("comments = %d, want 12", pr.Comments)
	}
	if pr.CreatedAt.IsZero() {
		t.Error("createdAt lost")
	}
}

// A PR sitting in its base branch's merge queue is marked queued; one that
// isn't (or a GitHub that doesn't say) is not.
func TestPRNodeInMergeQueue(t *testing.T) {
	for body, want := range map[string]bool{
		`{"number": 1, "state": "OPEN", "isInMergeQueue": true}`:  true,
		`{"number": 1, "state": "OPEN", "isInMergeQueue": false}`: false,
		`{"number": 1, "state": "OPEN"}`:                          false,
	} {
		var n prNode
		if err := json.Unmarshal([]byte(body), &n); err != nil {
			t.Fatal(err)
		}
		if got := n.toPR("o/r").Queued; got != want {
			t.Errorf("%s: queued = %v, want %v", body, got, want)
		}
	}
}

// A PR whose head commit has no checks at all reports none, not pending.
func TestPRNodeWithoutChecks(t *testing.T) {
	var n prNode
	body := `{"number": 1, "url": "https://github.com/o/r/pull/1", "state": "OPEN",
	  "repository": {"nameWithOwner": "o/r"}, "comments": {"totalCount": 0},
	  "commits": {"nodes": [{"commit": {"statusCheckRollup": null}}]}}`
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		t.Fatal(err)
	}
	if got := n.toPR("").CI; got != core.CINone {
		t.Errorf("ci = %q, want none", got)
	}
	var empty prNode
	if got := empty.toPR("o/r").CI; got != core.CINone {
		t.Errorf("no commits: ci = %q, want none", got)
	}
	if got := empty.toPR("o/r").Repo; got != "o/r" {
		t.Errorf("repo hint not used: %q", got)
	}
}

// Partial failure is the normal case with several repos: one alias resolves,
// another comes back null with an error and a non-zero exit. The good data must
// survive.
func TestGQLResponsePartialFailure(t *testing.T) {
	body := `{"data": {"b0": {"pullRequests": {"nodes": [{"number": 100}]}}, "b1": null},
	  "errors": [{"message": "Could not resolve to a Repository"}]}`
	var resp gqlResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("aliases = %d, want 2", len(resp.Data))
	}
	if resp.Data["b0"] == nil || resp.Data["b0"].PullRequests == nil || len(resp.Data["b0"].PullRequests.Nodes) != 1 {
		t.Error("the alias that resolved was lost")
	}
	if resp.Data["b1"] != nil {
		t.Error("the failed alias should decode as nil, so its cached PRs are kept")
	}
	if len(resp.Errors) != 1 {
		t.Error("errors not decoded")
	}
}

// One repo's lookup failing while the rest succeed must not empty that repo from
// the board: its cached PRs stay as they were until a lookup actually answers.
// A lookup that answers with no PR is different — that branch really has none.
func TestMergeKeepsAFailedReposPRs(t *testing.T) {
	branches := []branchTarget{
		{repo: "o/reachable", branch: "a"},
		{repo: "o/unreachable", branch: "b"},
		{repo: "o/answered-empty", branch: "c"},
	}
	existing := []core.PR{
		{Repo: "o/reachable", Number: 1, Title: "old title"},
		{Repo: "O/Unreachable", Number: 2, Title: "cached"}, // case differs from the remote URL
		{Repo: "o/answered-empty", Number: 3, Title: "gone"},
	}
	data := map[string]*aliasNode{
		"b0": {PullRequests: &struct {
			Nodes []prNode `json:"nodes"`
		}{Nodes: []prNode{{Number: 1, Title: "new title", Repository: struct {
			NameWithOwner string `json:"nameWithOwner"`
		}{"o/reachable"}}}}},
		"b1": nil, // this lookup failed
		"b2": {PullRequests: &struct {
			Nodes []prNode `json:"nodes"`
		}{}},
	}

	got := map[int]core.PR{}
	for _, pr := range merge(existing, data, lookups{branches: branches}) {
		got[pr.Number] = pr
	}
	if got[1].Title != "new title" {
		t.Errorf("reachable PR not refreshed: %+v", got[1])
	}
	if pr, ok := got[2]; !ok || pr.Title != "cached" {
		t.Errorf("failed repo's PR dropped or changed: %+v (present=%v)", pr, ok)
	}
	if _, ok := got[3]; ok {
		t.Error("a branch that answered with no PR kept a stale one")
	}
}

// Repo names are case-insensitive on GitHub. A PR registered as "O/Repo" and
// refreshed as GitHub's "o/repo" is one PR: kept registered, listed once.
func TestMergeMatchesRepoCaseInsensitively(t *testing.T) {
	existing := []core.PR{{Repo: "O/Repo", Number: 5, Title: "old", Registered: true}}
	numbers := []numberTarget{{repo: "O/Repo", number: 5}}
	fresh := prNode{Number: 5, Title: "new"}
	fresh.Repository.NameWithOwner = "o/repo"
	got := merge(existing, map[string]*aliasNode{"n0": {PullRequest: &fresh}}, lookups{numbers: numbers})
	if len(got) != 1 {
		t.Fatalf("got %d PRs, want one: %+v", len(got), got)
	}
	if !got[0].Registered || got[0].Title != "new" {
		t.Errorf("got %+v, want the refreshed PR, still registered", got[0])
	}
}

// A branch lookup matches the head name across forks. A fork's PR on a branch
// that happens to share the worktree's name is someone else's, and is dropped —
// unless it was registered on purpose, by number.
func TestMergeDropsForkPRsFoundByBranch(t *testing.T) {
	own := prNode{Number: 1}
	own.Repository.NameWithOwner = "o/r"
	fork := prNode{Number: 2, IsCrossRepository: true}
	fork.Repository.NameWithOwner = "o/r"
	registeredFork := prNode{Number: 3, IsCrossRepository: true}
	registeredFork.Repository.NameWithOwner = "o/r"

	data := map[string]*aliasNode{
		"b0": {PullRequests: &struct {
			Nodes []prNode `json:"nodes"`
		}{Nodes: []prNode{own, fork}}},
		"n0": {PullRequest: &registeredFork},
	}
	got := map[int]bool{}
	for _, pr := range merge([]core.PR{{Repo: "o/r", Number: 3, Registered: true}}, data,
		lookups{branches: []branchTarget{{repo: "o/r", branch: "fix-typo"}}, numbers: []numberTarget{{repo: "o/r", number: 3}}}) {
		got[pr.Number] = true
	}
	if !got[1] || got[2] || !got[3] {
		t.Errorf("got %v, want own #1 and registered #3, not the fork's #2", got)
	}
}

// A merged PR is terminal, so a branch whose cached PRs have all merged is not
// looked up again. A branch with anything else on it still is — a reused branch
// can carry a live PR behind a merged one — and so is one with no PR yet, which
// is how a new PR is found.
func TestPlanSkipsSettledBranches(t *testing.T) {
	existing := []core.PR{
		{Repo: "O/R", Number: 1, Head: "done", State: core.PRMerged},
		{Repo: "o/r", Number: 2, Head: "live", State: core.PROpen},
		{Repo: "o/r", Number: 3, Head: "reused", State: core.PRMerged},
		{Repo: "o/r", Number: 4, Head: "reused", State: core.PRDraft},
		{Repo: "o/r", Number: 5, Head: "shut", State: core.PRClosed},
	}
	branches := []branchTarget{
		{repo: "o/r", branch: "done"},
		{repo: "o/r", branch: "fresh"},
		{repo: "o/r", branch: "live"},
		{repo: "o/r", branch: "reused"},
		{repo: "o/r", branch: "shut"},
	}
	l := plan(existing, branches)
	want := []branchTarget{
		{repo: "o/r", branch: "fresh"},
		{repo: "o/r", branch: "live"},
		{repo: "o/r", branch: "reused"},
		{repo: "o/r", branch: "shut"},
	}
	if !reflect.DeepEqual(l.branches, want) {
		t.Errorf("branches = %+v, want %+v", l.branches, want)
	}
	if !l.settled[settledKey("o/r", "done")] || len(l.settled) != 1 {
		t.Errorf("settled = %v, want only o/r done", l.settled)
	}
}

// A merged PR whose branch was skipped is kept as it was: it was not asked
// about, so its absence from the answer says nothing. One whose worktree is gone
// is not kept — that is how a finished branch leaves the view.
func TestMergeKeepsSettledMergedPRs(t *testing.T) {
	existing := []core.PR{
		{Repo: "o/r", Number: 1, Head: "done", State: core.PRMerged, Title: "kept"},
		{Repo: "o/r", Number: 2, Head: "removed", State: core.PRMerged},
	}
	l := plan(existing, []branchTarget{{repo: "o/r", branch: "done"}})
	got := merge(existing, map[string]*aliasNode{}, l)
	if len(got) != 1 || got[0].Number != 1 || got[0].Title != "kept" {
		t.Errorf("got %+v, want only the settled #1, unchanged", got)
	}
}

// One query answers every due space; its aliases carry the space's index, and
// the answer is split back so each space merges only what it asked for.
func TestSplitBySpace(t *testing.T) {
	a, b := &aliasNode{}, &aliasNode{}
	got := splitBySpace(map[string]*aliasNode{"s0b0": a, "s1n0": b, "s1b2": nil, "junk": a})
	want := map[int]map[string]*aliasNode{
		0: {"b0": a},
		1: {"n0": b, "b2": nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitBySpace = %v, want %v", got, want)
	}
}

// A PR the agent registers while a sweep is in flight is not in that sweep's
// result. Landing the sweep must not erase the registration.
func TestKeepRegisteredKeepsARegistrationMadeMeanwhile(t *testing.T) {
	got := KeepRegistered(
		[]core.PR{{Repo: "o/r", Number: 1, Title: "fresh"}},
		[]core.PR{{Repo: "o/r", Number: 1}, {Repo: "o/r", Number: 5, Registered: true}, {Repo: "o/r", Number: 6}},
	)
	nums := map[int]bool{}
	for _, pr := range got {
		nums[pr.Number] = true
	}
	if len(got) != 2 || !nums[1] || !nums[5] {
		t.Errorf("got %+v, want the sweep's #1 plus the registered #5", got)
	}
}
