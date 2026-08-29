package workspaces

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/statestore"
)

func TestForgeStateRank(t *testing.T) {
	if forgeStateRank(integration.ForgeOpen) >= forgeStateRank(integration.ForgeDraft) {
		t.Error("open should rank before draft")
	}
	if forgeStateRank(integration.ForgeClosed) != len(forgeStateOrder)-1 {
		t.Error("closed should rank last of the known states")
	}
	if forgeStateRank(integration.ForgeNone) != len(forgeStateOrder) {
		t.Error("none/unknown should rank after all known states")
	}
}

func TestAssociatePRs_BranchMatch(t *testing.T) {
	ws := &statestore.Workspace{
		Worktrees: []statestore.Worktree{
			{Repo: "o/a", Branch: "feat/x"},
			{Repo: "o/b", Branch: "fix/y"},
		},
	}
	byBranch := map[string]map[string]integration.PullRequest{
		"o/a": {"feat/x": {Number: 1, Repo: "o/a", Branch: "feat/x", State: integration.ForgeOpen}},
		"o/b": {"fix/y": {Number: 2, Repo: "o/b", Branch: "fix/y", State: integration.ForgeDraft}},
	}
	got := associatePRs(ws, byBranch, map[string]map[int]integration.PullRequest{})
	if len(got) != 2 {
		t.Fatalf("got %d PRs, want 2", len(got))
	}
	// open sorts before draft.
	if got[0].Number != 1 || got[1].Number != 2 {
		t.Errorf("sort wrong: %+v", got)
	}
}

func TestAssociatePRs_PreservesRegistered(t *testing.T) {
	ws := &statestore.Workspace{
		PRs: []statestore.PR{{Number: 9, Repo: "o/c", State: "open", Registered: true}},
	}
	// The query covers o/c #9 with fresher fields.
	byNumber := map[string]map[int]integration.PullRequest{
		"o/c": {9: {Number: 9, Repo: "o/c", State: integration.ForgeOpen, CI: integration.CIPass, Title: "refreshed"}},
	}
	got := associatePRs(ws, map[string]map[string]integration.PullRequest{}, byNumber)
	if len(got) != 1 || got[0].Number != 9 || got[0].Title != "refreshed" || got[0].CI != "pass" {
		t.Fatalf("registered PR not refreshed: %+v", got)
	}
	if !got[0].Registered {
		t.Error("registered flag lost")
	}
	// When the query DOESN'T cover it, it's kept as-is (not dropped).
	got = associatePRs(ws, map[string]map[string]integration.PullRequest{}, map[string]map[int]integration.PullRequest{})
	if len(got) != 1 || got[0].Number != 9 {
		t.Fatalf("registered PR dropped: %+v", got)
	}
}

func TestAssociatePRs_Dedup(t *testing.T) {
	// A PR matched by branch AND registered → one entry.
	ws := &statestore.Workspace{
		Worktrees: []statestore.Worktree{{Repo: "o/a", Branch: "feat/x"}},
		PRs:       []statestore.PR{{Number: 1, Repo: "o/a", Registered: true}},
	}
	byBranch := map[string]map[string]integration.PullRequest{
		"o/a": {"feat/x": {Number: 1, Repo: "o/a", Branch: "feat/x", State: integration.ForgeOpen}},
	}
	byNumber := map[string]map[int]integration.PullRequest{
		"o/a": {1: {Number: 1, Repo: "o/a", State: integration.ForgeOpen}},
	}
	got := associatePRs(ws, byBranch, byNumber)
	if len(got) != 1 {
		t.Fatalf("expected dedup to 1, got %d: %+v", len(got), got)
	}
}

func TestWorkspaceForgeRollup(t *testing.T) {
	prs := []statestore.PR{
		{State: "closed"}, {State: "open"}, {State: "draft"},
	}
	count, lead := workspaceForgeRollup(prs)
	if count != 3 {
		t.Errorf("count = %d", count)
	}
	if lead != integration.ForgeOpen {
		t.Errorf("lead = %q, want open (lowest rank)", lead)
	}
	if c, _ := workspaceForgeRollup(nil); c != 0 {
		t.Errorf("nil rollup count = %d", c)
	}
}

func TestWorkspacePRAttention(t *testing.T) {
	prs := []statestore.PR{
		{State: "open", CI: "fail"},                          // attention
		{State: "open", ReviewDecision: "changes_requested"}, // attention
		{State: "open", CI: "pass"},                          // no
		{State: "merged", CI: "fail"},                        // no (not open/draft)
	}
	if got := workspacePRAttention(prs); got != 2 {
		t.Errorf("workspacePRAttention = %d, want 2", got)
	}
}

func TestStorePRRoundTrip(t *testing.T) {
	pr := integration.PullRequest{
		Number: 7, Repo: "o/r", Title: "t", State: integration.ForgeOpen,
		CI: integration.CIPending, ReviewDecision: integration.ReviewApproved,
		Comments: 3, URL: "u", Branch: "b",
	}
	back := fromStorePR(toStorePR(pr, true))
	if back.Number != pr.Number || back.Repo != pr.Repo || back.State != pr.State ||
		back.CI != pr.CI || back.ReviewDecision != pr.ReviewDecision || back.Comments != pr.Comments {
		t.Errorf("round-trip mismatch: %+v vs %+v", back, pr)
	}
}

func TestRenderGlyphs(t *testing.T) {
	if renderForgeBadge(integration.ForgeNone) != "" {
		t.Error("ForgeNone badge should be empty")
	}
	if !strings.Contains(renderForgeBadge(integration.ForgeOpen), "\033[38;5;") {
		t.Error("open badge should carry an SGR color")
	}
	// CI/review absent → a single space placeholder (keeps columns aligned).
	if renderCIGlyph(integration.CINone) != " " {
		t.Errorf("CINone glyph = %q, want single space", renderCIGlyph(integration.CINone))
	}
	if renderReviewGlyph(integration.ReviewNone) != " " {
		t.Errorf("ReviewNone glyph = %q, want single space", renderReviewGlyph(integration.ReviewNone))
	}
}
