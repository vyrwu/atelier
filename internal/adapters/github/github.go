// Package github is the ForgeIntegration adapter for GitHub. It lists a repo's
// pull requests in ONE batched `gh pr list --json` call, classifies each PR's
// state, CI verdict, and review decision into the kernel's normalized
// vocabulary, opens a PR in a browser, and (opt-in) closes one. It renders
// nothing and owns no window options — the kernel owns the badge slot, glyph,
// color, sort order, caching, and refresh cadence. Swap this adapter for a
// GitLab one by implementing the same integration.ForgeIntegration port and
// selecting it in config.
package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/integration"
)

// Adapter satisfies integration.ForgeIntegration for GitHub.
type Adapter struct{}

// New constructs the GitHub forge adapter.
func New() *Adapter { return &Adapter{} }

// Name identifies the adapter.
func (Adapter) Name() string { return "github" }

// checkJSON is one entry of gh's statusCheckRollup array. GitHub returns a
// heterogeneous mix: CheckRun entries carry status (QUEUED/IN_PROGRESS/
// COMPLETED) + conclusion (SUCCESS/FAILURE/...), while StatusContext entries
// carry only state (SUCCESS/FAILURE/PENDING/...). We decode all three fields
// and let classifyCI aggregate whichever are present.
type checkJSON struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// prJSON is the shape of one element of `gh pr list --json ...` output.
type prJSON struct {
	Number            int         `json:"number"`
	Title             string      `json:"title"`
	State             string      `json:"state"` // OPEN | MERGED | CLOSED
	IsDraft           bool        `json:"isDraft"`
	StatusCheckRollup []checkJSON `json:"statusCheckRollup"`
	ReviewDecision    string      `json:"reviewDecision"`
	Comments          []struct{}  `json:"comments"`
	URL               string      `json:"url"`
	HeadRefName       string      `json:"headRefName"`
	UpdatedAt         string      `json:"updatedAt"`
}

// List returns the pull requests for the repo checked out at repoPath in ONE
// batched `gh pr list` call. Best-effort: any absence (no gh, no network, not
// a repo, unparseable output) returns (nil, nil) — a badge/list is cosmetic
// and must never break the caller.
func (Adapter) List(repoPath string) ([]integration.PullRequest, error) {
	if repoPath == "" {
		return nil, nil
	}
	// --state all: `gh pr list` defaults to open-only, but the M-c Changes view
	// and the workspace PR set render merged/closed too — and a registered PR
	// must be able to transition to merged/closed on refresh. Without this the
	// adapter can never report anything but open, so a merged PR silently
	// vanishes and a registered PR freezes at "open".
	out, err := ghOutput(repoPath, "pr", "list", "--state", "all", "--json",
		"number,title,state,isDraft,statusCheckRollup,reviewDecision,comments,url,headRefName,updatedAt,headRepositoryOwner,headRepository",
		"--limit", "50")
	if err != nil {
		return nil, nil
	}
	var raw []prJSON
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, nil
	}
	repo := repoNameWithOwner(repoPath) // best-effort; "" if unavailable
	prs := make([]integration.PullRequest, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, integration.PullRequest{
			Number:         r.Number,
			Repo:           repo,
			Title:          r.Title,
			State:          classify(r.State, r.IsDraft),
			CI:             classifyCI(r.StatusCheckRollup),
			ReviewDecision: classifyReview(r.ReviewDecision),
			Comments:       len(r.Comments),
			URL:            r.URL,
			Branch:         r.HeadRefName,
			UpdatedAt:      parseTime(r.UpdatedAt),
		})
	}
	return prs, nil
}

// Open opens the given pull request in a browser via `gh pr view <url> --web`.
// Best-effort: a failure to launch is swallowed.
func (Adapter) Open(pr integration.PullRequest) error {
	if pr.URL == "" {
		return nil
	}
	_ = exec.Command("gh", "pr", "view", pr.URL, "--web").Run()
	return nil
}

// Close closes a pull request WITHOUT merging via `gh pr close`. Unlike the
// read paths this SURFACES the error: it's a mutating action the user has
// confirmed, so a failure must be reported, not swallowed.
func (Adapter) Close(pr integration.PullRequest) error {
	cmd := exec.Command("gh", "pr", "close", fmt.Sprint(pr.Number), "--repo", pr.Repo)
	return cmd.Run()
}

// classify maps gh's state + draft flag onto the kernel's ForgeState. Pure.
func classify(state string, isDraft bool) integration.ForgeState {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "MERGED":
		return integration.ForgeMerged
	case "CLOSED":
		return integration.ForgeClosed
	default: // OPEN (or anything unexpected → treat as open)
		if isDraft {
			return integration.ForgeDraft
		}
		return integration.ForgeOpen
	}
}

// classifyCI aggregates gh's statusCheckRollup into a single CI verdict. Pure.
//
// Precedence: any failure wins (→ CIFail), then any in-flight check (→
// CIPending), then — if at least one check exists and none failed or pends —
// CIPass. An empty rollup is CINone (no CI configured / no runs).
func classifyCI(checks []checkJSON) integration.CIStatus {
	if len(checks) == 0 {
		return integration.CINone
	}
	pending := false
	for _, c := range checks {
		concl := strings.ToUpper(strings.TrimSpace(c.Conclusion))
		state := strings.ToUpper(strings.TrimSpace(c.State))
		status := strings.ToUpper(strings.TrimSpace(c.Status))

		switch concl {
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT":
			return integration.CIFail
		}
		switch state {
		case "FAILURE", "ERROR":
			return integration.CIFail
		}
		switch status {
		case "QUEUED", "IN_PROGRESS", "PENDING":
			pending = true
		}
		if state == "PENDING" {
			pending = true
		}
	}
	if pending {
		return integration.CIPending
	}
	return integration.CIPass
}

// classifyReview maps gh's reviewDecision string onto the kernel's
// ReviewDecision. Pure.
func classifyReview(decision string) integration.ReviewDecision {
	switch strings.ToUpper(strings.TrimSpace(decision)) {
	case "APPROVED":
		return integration.ReviewApproved
	case "CHANGES_REQUESTED":
		return integration.ReviewChangesRequested
	case "REVIEW_REQUIRED":
		return integration.ReviewRequired
	default:
		return integration.ReviewNone
	}
}

// parseTime parses an RFC3339 timestamp, returning the zero Time on any error.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// repoNameWithOwner returns the "owner/repo" slug for the repo at dir via
// `gh repo view`. Best-effort: "" on any error.
func repoNameWithOwner(dir string) string {
	out, err := ghOutput(dir, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func ghOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
