package github

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/integration"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		state   string
		isDraft bool
		want    integration.ForgeState
	}{
		{"OPEN", false, integration.ForgeOpen},
		{"open", false, integration.ForgeOpen},
		{"OPEN", true, integration.ForgeDraft},
		{"MERGED", false, integration.ForgeMerged},
		{"CLOSED", false, integration.ForgeClosed},
		{"  merged  ", false, integration.ForgeMerged},
		{"weird", false, integration.ForgeOpen}, // unexpected → open
		{"weird", true, integration.ForgeDraft}, // unexpected + draft → draft
	}
	for _, c := range cases {
		if got := classify(c.state, c.isDraft); got != c.want {
			t.Errorf("classify(%q, %v) = %q, want %q", c.state, c.isDraft, got, c.want)
		}
	}
}

func TestClassifyCI(t *testing.T) {
	cases := []struct {
		name   string
		checks []checkJSON
		want   integration.CIStatus
	}{
		{"empty", nil, integration.CINone},
		{"empty slice", []checkJSON{}, integration.CINone},
		{
			"all success checkrun",
			[]checkJSON{{Status: "COMPLETED", Conclusion: "SUCCESS"}, {Status: "COMPLETED", Conclusion: "SUCCESS"}},
			integration.CIPass,
		},
		{
			"all success statuscontext",
			[]checkJSON{{State: "SUCCESS"}},
			integration.CIPass,
		},
		{
			"failure checkrun wins over success",
			[]checkJSON{{Status: "COMPLETED", Conclusion: "SUCCESS"}, {Status: "COMPLETED", Conclusion: "FAILURE"}},
			integration.CIFail,
		},
		{
			"error conclusion is fail",
			[]checkJSON{{Status: "COMPLETED", Conclusion: "ERROR"}},
			integration.CIFail,
		},
		{
			"cancelled conclusion is fail",
			[]checkJSON{{Status: "COMPLETED", Conclusion: "CANCELLED"}},
			integration.CIFail,
		},
		{
			"timed_out conclusion is fail",
			[]checkJSON{{Status: "COMPLETED", Conclusion: "TIMED_OUT"}},
			integration.CIFail,
		},
		{
			"failure statuscontext state is fail",
			[]checkJSON{{State: "FAILURE"}},
			integration.CIFail,
		},
		{
			"error statuscontext state is fail",
			[]checkJSON{{State: "ERROR"}},
			integration.CIFail,
		},
		{
			"in_progress checkrun is pending",
			[]checkJSON{{Status: "IN_PROGRESS"}, {Status: "COMPLETED", Conclusion: "SUCCESS"}},
			integration.CIPending,
		},
		{
			"queued checkrun is pending",
			[]checkJSON{{Status: "QUEUED"}},
			integration.CIPending,
		},
		{
			"pending statuscontext state is pending",
			[]checkJSON{{State: "PENDING"}},
			integration.CIPending,
		},
		{
			"fail beats pending",
			[]checkJSON{{Status: "IN_PROGRESS"}, {Status: "COMPLETED", Conclusion: "FAILURE"}},
			integration.CIFail,
		},
		{
			"lowercase is normalized",
			[]checkJSON{{Status: "completed", Conclusion: "success"}},
			integration.CIPass,
		},
	}
	for _, c := range cases {
		if got := classifyCI(c.checks); got != c.want {
			t.Errorf("%s: classifyCI() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClassifyReview(t *testing.T) {
	cases := []struct {
		in   string
		want integration.ReviewDecision
	}{
		{"APPROVED", integration.ReviewApproved},
		{"approved", integration.ReviewApproved},
		{"CHANGES_REQUESTED", integration.ReviewChangesRequested},
		{"REVIEW_REQUIRED", integration.ReviewRequired},
		{"  review_required  ", integration.ReviewRequired},
		{"", integration.ReviewNone},
		{"UNKNOWN", integration.ReviewNone},
	}
	for _, c := range cases {
		if got := classifyReview(c.in); got != c.want {
			t.Errorf("classifyReview(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParsePRList unmarshals a realistic `gh pr list --json` fixture and maps
// it through the same decode+classify path List uses (repo is stamped by the
// caller, not present in per-PR JSON). No network, no gh binary.
func TestParsePRList(t *testing.T) {
	const fixture = `[
	  {
	    "number": 42,
	    "title": "Add batched forge listing",
	    "state": "OPEN",
	    "isDraft": false,
	    "statusCheckRollup": [
	      {"status": "COMPLETED", "conclusion": "SUCCESS"},
	      {"status": "COMPLETED", "conclusion": "FAILURE"}
	    ],
	    "reviewDecision": "CHANGES_REQUESTED",
	    "comments": [{}, {}, {}],
	    "url": "https://github.com/vyrwu/atelier/pull/42",
	    "headRefName": "feat/batched-forge",
	    "updatedAt": "2026-08-29T12:34:56Z"
	  },
	  {
	    "number": 7,
	    "title": "Draft: wip",
	    "state": "OPEN",
	    "isDraft": true,
	    "statusCheckRollup": [],
	    "reviewDecision": "",
	    "comments": [],
	    "url": "https://github.com/vyrwu/atelier/pull/7",
	    "headRefName": "wip",
	    "updatedAt": "2026-08-28T00:00:00Z"
	  }
	]`

	var raw []prJSON
	if err := json.Unmarshal([]byte(fixture), &raw); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("got %d PRs, want 2", len(raw))
	}

	const repo = "vyrwu/atelier"
	got := integration.PullRequest{
		Number:         raw[0].Number,
		Repo:           repo,
		Title:          raw[0].Title,
		State:          classify(raw[0].State, raw[0].IsDraft),
		CI:             classifyCI(raw[0].StatusCheckRollup),
		ReviewDecision: classifyReview(raw[0].ReviewDecision),
		Comments:       len(raw[0].Comments),
		URL:            raw[0].URL,
		Branch:         raw[0].HeadRefName,
		UpdatedAt:      parseTime(raw[0].UpdatedAt),
	}
	want := integration.PullRequest{
		Number:         42,
		Repo:           "vyrwu/atelier",
		Title:          "Add batched forge listing",
		State:          integration.ForgeOpen,
		CI:             integration.CIFail,
		ReviewDecision: integration.ReviewChangesRequested,
		Comments:       3,
		URL:            "https://github.com/vyrwu/atelier/pull/42",
		Branch:         "feat/batched-forge",
		UpdatedAt:      time.Date(2026, 8, 29, 12, 34, 56, 0, time.UTC),
	}
	if got != want {
		t.Errorf("PR[0]:\n got  %+v\n want %+v", got, want)
	}

	// Second PR: draft, no checks, no review, no comments.
	if s := classify(raw[1].State, raw[1].IsDraft); s != integration.ForgeDraft {
		t.Errorf("PR[1] state = %q, want draft", s)
	}
	if ci := classifyCI(raw[1].StatusCheckRollup); ci != integration.CINone {
		t.Errorf("PR[1] CI = %q, want none", ci)
	}
	if rd := classifyReview(raw[1].ReviewDecision); rd != integration.ReviewNone {
		t.Errorf("PR[1] review = %q, want none", rd)
	}
	if n := len(raw[1].Comments); n != 0 {
		t.Errorf("PR[1] comments = %d, want 0", n)
	}
}

func TestParseTime(t *testing.T) {
	if got := parseTime("2026-08-29T12:34:56Z"); !got.Equal(time.Date(2026, 8, 29, 12, 34, 56, 0, time.UTC)) {
		t.Errorf("parseTime valid = %v", got)
	}
	if got := parseTime("not-a-time"); !got.IsZero() {
		t.Errorf("parseTime invalid should be zero, got %v", got)
	}
	if got := parseTime(""); !got.IsZero() {
		t.Errorf("parseTime empty should be zero, got %v", got)
	}
}

// TestList_EmptyRepoPath guards the no-path branch: never shell out, always
// (nil, nil).
func TestList_EmptyRepoPath(t *testing.T) {
	prs, err := Adapter{}.List("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prs != nil {
		t.Errorf("empty repoPath should yield nil PRs, got %v", prs)
	}
}

// compile-time check: Adapter satisfies the port.
var _ integration.ForgeIntegration = Adapter{}
