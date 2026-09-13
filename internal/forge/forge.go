// Package forge derives pull-request state from the GitHub CLI (`gh`). It runs
// one batched `gh pr list` per distinct repo, matches PRs to worktree branches
// by head ref, and normalizes each into a core.PR. GitHub is ground truth; the
// result fills a cache. Registered PRs from a prior refresh are preserved even
// when gh no longer returns them (e.g. the branch was pruned), so a tracked PR
// never silently vanishes.
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

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
	Comments          []struct{}  `json:"comments"` // decoded only for its length
	CreatedAt         time.Time   `json:"createdAt"`
}

// prJSONFields is the shared `--json` field list for list and view.
const prJSONFields = "number,title,url,state,isDraft,headRefName,statusCheckRollup,reviewDecision,comments,createdAt"

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
	anyFailed := false
	for repo, dir := range dirs {
		prs, ok := listPRs(dir)
		if !ok {
			anyFailed = true // timed out / no gh / offline — don't drop this repo's PRs
			continue
		}
		for _, r := range prs {
			if !branches[repo][r.HeadRefName] {
				continue
			}
			// Identify the repo as owner/repo (from the PR URL), so it matches
			// registered PRs (which parse owner/repo from the URL too) and dedupes
			// against them — the worktree directory name ("infrastructure") would
			// not match "owner/infrastructure".
			pr := prFromJSON(repo, r, false)
			out = append(out, pr)
			seen[key(pr.Repo, pr.Number)] = true
		}
	}

	// Preserve existing PRs not re-discovered: always keep registered ones, and —
	// so a timed-out/offline refresh never makes PRs vanish — keep everything when
	// any repo's query failed (the UI keeps the last-known status until it lands).
	for _, pr := range existing {
		if seen[key(pr.Repo, pr.Number)] {
			continue
		}
		if pr.Registered || anyFailed {
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

// listPRs runs the batched gh query in dir. --state all so closed/merged/draft
// PRs surface, not just open ones. ok is false on any failure (no gh, offline,
// not a repo, bad JSON, or the query exceeding ghTimeout) so the caller keeps the
// cached PRs rather than dropping them.
func listPRs(dir string) (prs []prJSON, ok bool) {
	data, err := ghOutput(dir, "pr", "list", "--state", "all", "--json", prJSONFields, "--limit", "50")
	if err != nil {
		return nil, false
	}
	if json.Unmarshal(data, &prs) != nil {
		return nil, false
	}
	return prs, true
}

// ghTimeout bounds a single gh call so a hung request can't leave the UI's
// refresh pending indefinitely. The refresh is async, so this never blocks the
// view — it only caps the background fetch. Generous on purpose: `gh pr list`
// with statusCheckRollup routinely takes ~4s, so a tight timeout would just keep
// returning the stale cache; this only fires on a genuinely stuck call.
const ghTimeout = 10 * time.Second

// ghOutput runs `gh <args...>` in dir (cwd unchanged if empty) with a timeout,
// returning stdout.
func ghOutput(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Output()
}

// prFromJSON normalizes one gh PR record into a core.PR. The repo is taken from
// the PR URL (owner/repo) so it dedupes against registered PRs, falling back to
// the passed repo hint.
func prFromJSON(repoHint string, r prJSON, registered bool) core.PR {
	name := repoFromURL(r.URL)
	if name == "" {
		name = repoHint
	}
	return core.PR{
		Repo:       name,
		Number:     r.Number,
		Title:      r.Title,
		URL:        r.URL,
		State:      classify(r.State, r.IsDraft),
		CI:         classifyCI(r.StatusCheckRollup),
		Review:     classifyReview(r.ReviewDecision),
		Comments:   len(r.Comments),
		CreatedAt:  r.CreatedAt,
		Registered: registered,
	}
}

// needsReopen reports whether reaching target from cur requires reopening first
// (GitHub can't move a closed PR straight to open/draft). Pure — see forge_test.
func needsReopen(cur, target core.PRState) bool {
	return cur == core.PRClosed && target != core.PRClosed
}

// prTransitionStep is the single `gh pr` subcommand (as args) that moves a PR
// from cur toward target, or nil if it's already there. It assumes any needed
// reopen has already happened (so cur is the real, post-reopen state). Pure —
// this is the transition matrix the tests pin. Merged is terminal (rejected by
// the caller before this runs).
func prTransitionStep(cur, target core.PRState) []string {
	switch target {
	case core.PRClosed:
		if cur != core.PRClosed {
			return []string{"close"}
		}
	case core.PROpen:
		if cur == core.PRDraft {
			return []string{"ready"}
		}
	case core.PRDraft:
		if cur == core.PROpen {
			return []string{"ready", "--undo"}
		}
	}
	return nil
}

// SetPRState changes a PR's state on GitHub (open/closed/draft) and returns the
// freshly re-queried PR so the caller can re-render it in place. "Open" means
// reopen a closed PR or mark a draft ready; "draft" converts an open PR back to
// draft. The prior Registered flag is preserved.
func SetPRState(pr core.PR, target core.PRState) (core.PR, error) {
	// Work from the real current state, not the (possibly stale) UI state, so
	// every permutation is reachable and a mismatch can't wedge the row.
	cur, err := prState(pr.Repo, pr.Number)
	if err != nil {
		return pr, err
	}
	if cur == core.PRMerged {
		return pr, fmt.Errorf("#%d is merged — its state can't be changed", pr.Number)
	}
	// GitHub won't move a closed PR straight to open/draft: reopen first, which
	// restores its prior draft-ness, then re-read so the final step is minimal.
	if needsReopen(cur, target) {
		if err := ghPR(pr.Repo, pr.Number, "reopen"); err != nil {
			return pr, err
		}
		if cur, err = prState(pr.Repo, pr.Number); err != nil {
			return pr, err
		}
	}
	if step := prTransitionStep(cur, target); step != nil {
		if err := ghPR(pr.Repo, pr.Number, step[0], step[1:]...); err != nil {
			return pr, err
		}
	}
	fresh, err := viewPR(pr.Repo, pr.Number)
	if err != nil {
		return pr, err
	}
	fresh.Registered = pr.Registered
	return fresh, nil
}

// prState re-queries just a PR's state (cheap — state + draft flag only).
func prState(repo string, number int) (core.PRState, error) {
	data, err := ghOutput("", "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "state,isDraft")
	if err != nil {
		return "", err
	}
	var r prJSON
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	return classify(r.State, r.IsDraft), nil
}

// ghPR runs `gh pr <verb> <number> --repo <owner/repo> [extra...]`. The --repo
// flag makes it work without a checkout (registered PRs may have none).
func ghPR(repo string, number int, verb string, extra ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	args := append([]string{"pr", verb, strconv.Itoa(number), "--repo", repo}, extra...)
	if out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr %s: %s", verb, strings.TrimSpace(string(out)))
	}
	return nil
}

// viewPR re-queries a single PR by number.
func viewPR(repo string, number int) (core.PR, error) {
	data, err := ghOutput("", "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", prJSONFields)
	if err != nil {
		return core.PR{}, err
	}
	var r prJSON
	if err := json.Unmarshal(data, &r); err != nil {
		return core.PR{}, err
	}
	return prFromJSON(repo, r, false), nil
}

// key is the dedupe identity for a PR: repo + "#" + number.
func key(repo string, number int) string {
	return repo + "#" + strconv.Itoa(number)
}

// repoFromURL extracts owner/repo from a github PR URL, or "" if it can't.
func repoFromURL(url string) string {
	i := strings.Index(url, "github.com/")
	if i < 0 {
		return ""
	}
	parts := strings.Split(url[i+len("github.com/"):], "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// classify maps gh's state + draft flag onto core.PRState. Terminal states win:
// a closed or merged PR is closed/merged even if it was a draft; only an OPEN PR
// is draft-vs-open. (Checking isDraft first would mislabel a closed draft as an
// active draft and wedge the state transitions.)
func classify(state string, isDraft bool) core.PRState {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "MERGED":
		return core.PRMerged
	case "CLOSED":
		return core.PRClosed
	}
	if isDraft {
		return core.PRDraft
	}
	return core.PROpen
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
