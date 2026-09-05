// Package forge derives pull-request state from the GitHub CLI (`gh`). It runs
// one batched `gh pr list` per distinct repo, matches PRs to worktree branches
// by head ref, and normalizes each into a core.PR. GitHub is ground truth; the
// result fills a cache. Registered PRs from a prior refresh are preserved even
// when gh no longer returns them (e.g. the branch was pruned), so a tracked PR
// never silently vanishes.
package forge

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/vyrwu/atelier/internal/core"
)

// checkJSON is one entry of gh's statusCheckRollup. CheckRun entries carry
// status + conclusion; StatusContext entries carry only state. We decode all
// three and let classifyCI aggregate whichever are present.
type checkJSON struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// prJSON is one element of `gh pr list --json ...` output.
type prJSON struct {
	Number            int         `json:"number"`
	Title             string      `json:"title"`
	URL               string      `json:"url"`
	State             string      `json:"state"`
	IsDraft           bool        `json:"isDraft"`
	HeadRefName       string      `json:"headRefName"`
	StatusCheckRollup []checkJSON `json:"statusCheckRollup"`
	ReviewDecision    string      `json:"reviewDecision"`
}

// Refresh queries GitHub for the pull requests behind the given worktrees and
// returns them as core.PRs. Worktrees are grouped by repo; each repo gets one
// `gh pr list` run (cwd = a worktree path for that repo so gh infers the repo).
// Only PRs whose head branch matches a worktree branch are kept. Registered PRs
// from `existing` that GitHub no longer returns are merged back in, deduped by
// repo and number. A repo whose gh call fails contributes nothing.
func Refresh(existing []core.PR, worktrees []core.Worktree) []core.PR {
	// One representative working dir + the set of branches, per repo.
	dirs := map[string]string{}
	branches := map[string]map[string]bool{}
	for _, wt := range worktrees {
		if _, ok := dirs[wt.Repo]; !ok {
			dirs[wt.Repo] = wt.Path
		}
		if branches[wt.Repo] == nil {
			branches[wt.Repo] = map[string]bool{}
		}
		branches[wt.Repo][wt.Branch] = true
	}

	var out []core.PR
	seen := map[string]bool{}
	for repo, dir := range dirs {
		for _, r := range listPRs(dir) {
			if !branches[repo][r.HeadRefName] {
				continue
			}
			pr := core.PR{
				Repo:       repo,
				Number:     r.Number,
				Title:      r.Title,
				URL:        r.URL,
				State:      classify(r.State, r.IsDraft),
				CI:         classifyCI(r.StatusCheckRollup),
				Review:     classifyReview(r.ReviewDecision),
				Registered: false,
			}
			out = append(out, pr)
			seen[key(pr.Repo, pr.Number)] = true
		}
	}

	// Preserve registered PRs that GitHub no longer surfaced.
	for _, pr := range existing {
		if pr.Registered && !seen[key(pr.Repo, pr.Number)] {
			out = append(out, pr)
			seen[key(pr.Repo, pr.Number)] = true
		}
	}
	return out
}

// Open opens url in the default browser (`open` on macOS, `xdg-open` on Linux).
func Open(url string) error {
	bin := "xdg-open"
	if runtime.GOOS == "darwin" {
		bin = "open"
	}
	return exec.Command(bin, url).Run()
}

// listPRs runs the batched gh query in dir. Best-effort: any failure (no gh, no
// network, not a repo, bad JSON) yields no PRs.
func listPRs(dir string) []prJSON {
	cmd := exec.Command("gh", "pr", "list", "--json",
		"number,title,url,state,isDraft,headRefName,statusCheckRollup,reviewDecision",
		"--limit", "50")
	cmd.Dir = dir
	data, err := cmd.Output()
	if err != nil {
		return nil
	}
	var raw []prJSON
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	return raw
}

// key is the dedupe identity for a PR: repo + "#" + number.
func key(repo string, number int) string {
	return repo + "#" + strconv.Itoa(number)
}

// classify maps gh's state + draft flag onto core.PRState. A draft is always
// PRDraft regardless of state.
func classify(state string, isDraft bool) core.PRState {
	if isDraft {
		return core.PRDraft
	}
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "MERGED":
		return core.PRMerged
	case "CLOSED":
		return core.PRClosed
	default:
		return core.PROpen
	}
}

// classifyCI aggregates statusCheckRollup into a single verdict. Any failure
// wins, then any in-flight check, then (if any check exists) pass; empty→none.
func classifyCI(checks []checkJSON) core.CIState {
	if len(checks) == 0 {
		return core.CINone
	}
	pending := false
	for _, c := range checks {
		concl := strings.ToUpper(strings.TrimSpace(c.Conclusion))
		state := strings.ToUpper(strings.TrimSpace(c.State))
		status := strings.ToUpper(strings.TrimSpace(c.Status))
		switch concl {
		case "FAILURE", "ERROR":
			return core.CIFail
		}
		if state == "FAILURE" || state == "ERROR" {
			return core.CIFail
		}
		switch status {
		case "PENDING", "IN_PROGRESS", "QUEUED":
			pending = true
		}
		if state == "PENDING" {
			pending = true
		}
	}
	if pending {
		return core.CIPending
	}
	return core.CIPass
}

// classifyReview maps gh's reviewDecision onto core.Review.
func classifyReview(decision string) core.Review {
	switch strings.ToUpper(strings.TrimSpace(decision)) {
	case "APPROVED":
		return core.ReviewApproved
	case "CHANGES_REQUESTED":
		return core.ReviewChanges
	case "REVIEW_REQUIRED":
		return core.ReviewRequired
	default:
		return core.ReviewNone
	}
}
