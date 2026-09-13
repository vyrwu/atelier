package forge

import (
	"reflect"
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

func TestClassifyCI(t *testing.T) {
	cases := []struct {
		name   string
		checks []checkJSON
		want   core.CIState
	}{
		{"none", nil, core.CINone},
		{"pass", []checkJSON{{Conclusion: "SUCCESS"}}, core.CIPass},
		{"fail-conclusion", []checkJSON{{Conclusion: "FAILURE"}}, core.CIFail},
		{"any-fail-wins", []checkJSON{{Conclusion: "SUCCESS"}, {Conclusion: "ERROR"}}, core.CIFail},
		{"pending-status", []checkJSON{{Status: "IN_PROGRESS"}}, core.CIPending},
		{"pass-then-pending", []checkJSON{{Conclusion: "SUCCESS"}, {Status: "QUEUED"}}, core.CIPending},
		{"fail-beats-pending", []checkJSON{{Status: "PENDING"}, {Conclusion: "FAILURE"}}, core.CIFail},
		{"status-context-state", []checkJSON{{State: "FAILURE"}}, core.CIFail},
		{"status-context-pass", []checkJSON{{State: "SUCCESS"}}, core.CIPass},
	}
	for _, c := range cases {
		if got := classifyCI(c.checks); got != c.want {
			t.Errorf("classifyCI(%s) = %q, want %q", c.name, got, c.want)
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
		{core.PRClosed, core.PRDraft, true},
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
		{core.PROpen, core.PROpen, nil}, // already open
		{core.PROpen, core.PRDraft, []string{"ready", "--undo"}},
		{core.PRDraft, core.PRDraft, nil},  // already draft
		{core.PRClosed, core.PROpen, nil},  // reopen (handled by caller) leaves nothing here
		{core.PRClosed, core.PRDraft, nil}, // ditto
	}
	for _, c := range cases {
		if got := prTransitionStep(c.cur, c.target); !reflect.DeepEqual(got, c.want) {
			t.Errorf("prTransitionStep(%q, %q) = %v, want %v", c.cur, c.target, got, c.want)
		}
	}
}
