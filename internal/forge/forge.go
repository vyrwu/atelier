// Package forge derives pull-request state from GitHub through `gh`. One
// GraphQL query per refresh asks for exactly the PRs a workspace tracks — its
// worktree branches, plus every registered PR by number — and exactly the
// fields the view draws. GitHub is ground truth; the result fills a cache.
// Registered PRs are preserved when the query can't reach them, so a tracked PR
// never silently vanishes.
//
// It is a query, not a listing: `gh pr list --json statusCheckRollup,comments`
// pulls the repo's 50 most recent PRs with every check run and every comment
// body, then throws nearly all of it away. In a busy repo those 50 span barely a
// day, so a workspace's own PR falls out of the window and stops being refreshed
// at all.
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/git"
)

// prNode is one PullRequest as the GraphQL query asks for it: the row's fields
// and nothing else. The CI signal is the *rollup* state — one aggregate enum
// GitHub computes — rather than every individual check run, and comments come
// back as a count rather than as bodies.
type prNode struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	State       string `json:"state"`
	IsDraft     bool   `json:"isDraft"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	HeadRefOid  string `json:"headRefOid"`
	// IsCrossRepository marks a PR whose head is in a fork: same branch name,
	// someone else's branch.
	IsCrossRepository bool      `json:"isCrossRepository"`
	CreatedAt         time.Time `json:"createdAt"`
	Repository        struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	ReviewDecision string `json:"reviewDecision"`
	Comments       struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// prFields is the field selection shared by every alias in the query.
const prFields = `fragment pr on PullRequest {
  number title url state isDraft createdAt headRefName baseRefName headRefOid isCrossRepository
  repository { nameWithOwner }
  reviewDecision
  comments { totalCount }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

// aliasNode is one aliased repository(...) result: a branch lookup yields a list,
// a number lookup yields a single PR. A null alias means that lookup failed
// (repo gone, no access) while the rest of the query still succeeded.
type aliasNode struct {
	PullRequests *struct {
		Nodes []prNode `json:"nodes"`
	} `json:"pullRequests"`
	PullRequest *prNode `json:"pullRequest"`
}

// gqlResponse is the envelope. Partial failures come back as data + errors, so
// data is read whatever the errors say.
type gqlResponse struct {
	Data   map[string]*aliasNode `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// branchTarget is "the PRs whose head is this branch in this repo".
type branchTarget struct {
	repo   string // owner/repo
	branch string
}

// numberTarget is "this exact PR" — how registered PRs are refreshed, since
// they may have no worktree (or no branch) left to find them by.
type numberTarget struct {
	repo   string // owner/repo
	number int
}

// Refresh queries GitHub for the pull requests a workspace tracks and returns
// them as core.PRs: one PR per worktree branch, plus every registered PR by
// number. All of it is one GraphQL request — no per-repo listing, no
// most-recent-50 window to fall out of. PRs of a repo whose lookup failed, and
// everything at all if the query itself failed, are kept from existing so a
// tracked PR never vanishes on a blip.
func Refresh(existing []core.PR, worktrees []core.Worktree) []core.PR {
	branches := branchTargets(worktrees)
	numbers := numberTargets(existing)
	if len(branches) == 0 && len(numbers) == 0 {
		return existing
	}

	data, ok := runQuery(buildQuery(branches, numbers))
	if !ok {
		return existing // offline, no gh, or an unparsable answer — keep what we had
	}

	return merge(existing, data, branches, numbers)
}

// merge folds a query's answer into the cache. A lookup that came back null
// failed on its own — repo renamed, SSO-restricted, briefly unreachable — while
// the rest of the query succeeded, so every cached PR of that repo is kept as it
// was rather than read as "no longer exists". Registered PRs are always kept.
func merge(existing []core.PR, data map[string]*aliasNode, branches []branchTarget, numbers []numberTarget) []core.PR {
	// Registered-ness lives in the cache, not on GitHub: carry it across so a PR
	// found by branch this time is still tracked when the branch is gone.
	registered := map[string]bool{}
	for _, pr := range existing {
		if pr.Registered {
			registered[key(pr.Repo, pr.Number)] = true
		}
	}

	var out []core.PR
	seen := map[string]bool{}
	failed := map[string]bool{} // lower-cased owner/repo whose lookup failed
	for alias, node := range data {
		if node == nil {
			failed[strings.ToLower(aliasRepo(alias, branches, numbers))] = true
			continue
		}
		var nodes []prNode
		switch {
		case node.PullRequests != nil:
			nodes = node.PullRequests.Nodes
		case node.PullRequest != nil:
			nodes = []prNode{*node.PullRequest}
		}
		for _, n := range nodes {
			// A branch lookup matches the head *name* across every fork. A space's
			// worktree is never a fork's branch, so those PRs are someone else's —
			// on a common name like fix-typo, and M-c would close them. A PR looked
			// up by number was registered on purpose, and stays.
			if n.IsCrossRepository && strings.HasPrefix(alias, "b") {
				continue
			}
			pr := n.toPR(aliasRepo(alias, branches, numbers))
			k := key(pr.Repo, pr.Number)
			if seen[k] {
				continue // a registered PR the branch lookup found too
			}
			pr.Registered = registered[k]
			out = append(out, pr)
			seen[k] = true
		}
	}

	for _, pr := range existing {
		k := key(pr.Repo, pr.Number)
		keep := pr.Registered || failed[strings.ToLower(pr.Repo)]
		if seen[k] || !keep {
			continue
		}
		out = append(out, pr)
		seen[k] = true
	}
	return out
}

// branchTargets is one lookup per repo+branch behind the workspace's worktrees.
// The repo is resolved from each worktree's origin remote (local, instant) —
// the directory name alone doesn't carry the owner.
func branchTargets(worktrees []core.Worktree) []branchTarget {
	repos := map[string]string{} // worktree dir segment → owner/repo
	var out []branchTarget
	seen := map[string]bool{}
	for _, wt := range worktrees {
		repo, ok := repos[wt.Repo]
		if !ok {
			repo = git.OriginRepo(wt.Path)
			repos[wt.Repo] = repo
		}
		if repo == "" || wt.Branch == "" {
			continue
		}
		k := repo + "\x00" + wt.Branch
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, branchTarget{repo: repo, branch: wt.Branch})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].repo != out[j].repo {
			return out[i].repo < out[j].repo
		}
		return out[i].branch < out[j].branch
	})
	return out
}

// numberTargets is one lookup per registered PR. These are addressed by number
// because a registered PR need not have a worktree — or a branch — any more,
// which is exactly the case that used to freeze at whatever status it was
// registered with.
func numberTargets(existing []core.PR) []numberTarget {
	var out []numberTarget
	seen := map[string]bool{}
	for _, pr := range existing {
		if !pr.Registered || pr.Repo == "" || pr.Number == 0 {
			continue
		}
		k := key(pr.Repo, pr.Number)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, numberTarget{repo: pr.Repo, number: pr.Number})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].repo != out[j].repo {
			return out[i].repo < out[j].repo
		}
		return out[i].number < out[j].number
	})
	return out
}

// buildQuery renders one query with an alias per lookup: b<i> for branches,
// n<i> for numbers. Aliases are what let a whole workspace — several repos,
// several branches — resolve in a single round trip.
func buildQuery(branches []branchTarget, numbers []numberTarget) string {
	var b strings.Builder
	b.WriteString("query {\n")
	for i, t := range branches {
		owner, name := splitRepo(t.repo)
		fmt.Fprintf(&b, "  b%d: repository(owner: %s, name: %s) { pullRequests(headRefName: %s, first: %d, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { ...pr } } }\n",
			i, gqlString(owner), gqlString(name), gqlString(t.branch), prsPerBranch)
	}
	for i, t := range numbers {
		owner, name := splitRepo(t.repo)
		fmt.Fprintf(&b, "  n%d: repository(owner: %s, name: %s) { pullRequest(number: %d) { ...pr } }\n",
			i, gqlString(owner), gqlString(name), t.number)
	}
	b.WriteString("}\n")
	b.WriteString(prFields)
	return b.String()
}

// prsPerBranch bounds how many PRs one branch can contribute. A branch normally
// has one, but a reused branch can carry a closed PR or two behind the live one.
const prsPerBranch = 5

// aliasRepo maps an alias back to the repo it was asked about — the fallback for
// a PR whose node somehow carries no repository.
func aliasRepo(alias string, branches []branchTarget, numbers []numberTarget) string {
	if len(alias) < 2 {
		return ""
	}
	i, err := strconv.Atoi(alias[1:])
	if err != nil {
		return ""
	}
	switch alias[0] {
	case 'b':
		if i < len(branches) {
			return branches[i].repo
		}
	case 'n':
		if i < len(numbers) {
			return numbers[i].repo
		}
	}
	return ""
}

// runQuery runs the GraphQL query through gh (which owns the auth). A partial
// failure — one repo gone, the rest fine — comes back as data plus errors and a
// non-zero exit, so the body is parsed regardless of the exit status; ok is
// false only when there is no usable data at all.
func runQuery(query string) (map[string]*aliasNode, bool) {
	data, err := ghOutput("", "api", "graphql", "-f", "query="+query)
	var resp gqlResponse
	if json.Unmarshal(data, &resp) != nil || resp.Data == nil {
		return nil, false
	}
	if err != nil && len(resp.Data) == 0 {
		return nil, false
	}
	return resp.Data, true
}

// toPR normalizes one queried PR into the core type. repoHint names the repo the
// alias asked about, for the (unexpected) case of a node without one.
func (n prNode) toPR(repoHint string) core.PR {
	repo := n.Repository.NameWithOwner
	if repo == "" {
		repo = repoHint
	}
	if repo == "" {
		repo = repoFromURL(n.URL)
	}
	return core.PR{
		Repo:      repo,
		Number:    n.Number,
		Title:     n.Title,
		URL:       n.URL,
		Head:      n.HeadRefName,
		Base:      n.BaseRefName,
		HeadOID:   n.HeadRefOid,
		State:     classify(n.State, n.IsDraft),
		CI:        classifyRollup(n.rollupState()),
		Review:    classifyReview(n.ReviewDecision),
		Comments:  n.Comments.TotalCount,
		CreatedAt: n.CreatedAt,
	}
}

// rollupState is the head commit's aggregate check state, or "" when the commit
// has no checks at all.
func (n prNode) rollupState() string {
	if len(n.Commits.Nodes) == 0 {
		return ""
	}
	r := n.Commits.Nodes[0].Commit.StatusCheckRollup
	if r == nil {
		return ""
	}
	return r.State
}

// splitRepo splits "owner/repo"; a value with no slash is treated as the name.
func splitRepo(repo string) (owner, name string) {
	if o, n, ok := strings.Cut(repo, "/"); ok {
		return o, n
	}
	return "", repo
}

// gqlString renders s as a GraphQL string literal. JSON string escaping is
// GraphQL string escaping, so a branch with a quote or backslash in it can't
// break out of the query.
func gqlString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// Open opens url in the default browser (`open` on macOS, `xdg-open` on Linux).
func Open(url string) error {
	bin := "xdg-open"
	if runtime.GOOS == "darwin" {
		bin = "open"
	}
	return exec.Command(bin, url).Run()
}

// ghTimeout bounds a single gh call so a hung request can't leave the UI's
// refresh pending indefinitely. The refresh is async, so this never blocks the
// view — it only caps the background fetch. The tailored query lands in about a
// second, so this only fires on a genuinely stuck call.
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

// needsReopen reports whether reaching target from cur requires reopening first
// (GitHub can't move a closed PR straight to open). Pure — see forge_test.
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
	}
	return nil
}

// SetPRState opens or closes a PR on GitHub and returns the freshly re-queried
// PR so the caller can re-render it in place. "Open" means reopen a closed PR
// or mark a draft ready. The prior Registered flag is preserved.
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
	// GitHub won't move a closed PR straight to open: reopen first, which
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
	var r struct {
		State   string `json:"state"`
		IsDraft bool   `json:"isDraft"`
	}
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

// viewPR re-queries a single PR by number, through the same query as a refresh
// so a re-rendered row carries exactly the fields a refreshed one does.
func viewPR(repo string, number int) (core.PR, error) {
	t := []numberTarget{{repo: repo, number: number}}
	data, ok := runQuery(buildQuery(nil, t))
	if !ok {
		return core.PR{}, fmt.Errorf("query %s#%d", repo, number)
	}
	node := data["n0"]
	if node == nil || node.PullRequest == nil {
		return core.PR{}, fmt.Errorf("%s#%d not found", repo, number)
	}
	return node.PullRequest.toPR(repo), nil
}

// key is the dedupe identity for a PR: repo + "#" + number. The repo is folded
// to lower case — GitHub names are case-insensitive, and a registered PR carries
// whatever casing the agent passed while a refreshed one carries GitHub's own.
func key(repo string, number int) string {
	return strings.ToLower(repo) + "#" + strconv.Itoa(number)
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

// classifyRollup maps the head commit's aggregate check state onto core.CIState.
// GitHub does the aggregation — any failure wins, then anything in flight — so
// this is the same verdict the old per-check pass computed, for a fraction of
// the payload. An empty state means the commit has no checks at all.
func classifyRollup(state string) core.CIState {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "SUCCESS":
		return core.CIPass
	case "FAILURE", "ERROR":
		return core.CIFail
	case "PENDING", "EXPECTED":
		return core.CIPending
	default:
		return core.CINone
	}
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
