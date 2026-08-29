package workspaces

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// The forge slot is KERNEL-owned: the kernel batches the PR queries (one per
// repo, never one-per-window), associates the returned PRs with workspaces,
// stores them on the workspace's statestore record, and owns all rendering
// (glyphs, columns, sort). The active integration.ForgeIntegration adapter only
// lists / opens / closes. When no forge adapter is configured, the slot is
// simply absent.

const (
	// optForgeSweptTs gates the whole batched forge sweep (one timestamp for
	// all repos) so repeated picker opens within the TTL reuse stored PRs.
	optForgeSweptTs = "@atelier_forge_swept_ts"
	// forgeRefreshTTL is the EVENT-driven throttle (picker open / land).
	forgeRefreshTTL = 1 * time.Minute
	// forgeLoopRefreshTTL is the background-loop throttle.
	forgeLoopRefreshTTL = 1 * time.Minute
)

// forgeStateOrder is the kernel's PR sort order: open, draft, merged, closed;
// no forge item sorts last.
var forgeStateOrder = []integration.ForgeState{
	integration.ForgeOpen, integration.ForgeDraft,
	integration.ForgeMerged, integration.ForgeClosed,
}

func forgeStateRank(state integration.ForgeState) int {
	for i, s := range forgeStateOrder {
		if s == state {
			return i
		}
	}
	return len(forgeStateOrder)
}

// renderForgeBadge returns the ANSI-colored PR-state glyph (leading space) for
// the M-s rollup / M-c row, using the kernel-owned glyph+color. Empty for
// ForgeNone/unknown. Pure.
func renderForgeBadge(state integration.ForgeState) string {
	glyph, color, ok := integration.ForgeGlyph(state)
	if !ok {
		return ""
	}
	return " \033[38;5;" + color + "m" + glyph + "\033[0m"
}

// renderCIGlyph / renderReviewGlyph render the M-c CI + approval columns. Empty
// for none/unknown. Pure.
func renderCIGlyph(ci integration.CIStatus) string {
	glyph, color, ok := integration.CIGlyph(ci)
	if !ok {
		return " "
	}
	return "\033[38;5;" + color + "m" + glyph + "\033[0m"
}

func renderReviewGlyph(r integration.ReviewDecision) string {
	glyph, color, ok := integration.ReviewGlyph(r)
	if !ok {
		return " "
	}
	return "\033[38;5;" + color + "m" + glyph + "\033[0m"
}

// ForgeRefreshCommand is the hidden `_forge-refresh`: run the batched sweep once
// (event-driven — picker open / workspace land).
func ForgeRefreshCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_forge-refresh",
		Short:  "internal: batched per-repo PR sweep → workspace PR records",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			forge := integration.Active().Forge
			if forge == nil {
				return nil
			}
			return refreshForgePRs(tmuxhost.New(socket), forge, time.Now(), forgeRefreshTTL)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// refreshForgePRs runs one batched sweep: enumerate every workspace's repos,
// query each DISTINCT repo once, associate the returned PRs with workspaces by
// worktree branch (preserving registered PRs), and store the result on each
// workspace's statestore record. Gated on a single global TTL so it costs one
// `gh` call per repo per TTL regardless of workspace/window count.
func refreshForgePRs(h *tmuxhost.Client, forge integration.ForgeIntegration, now time.Time, ttl time.Duration) error {
	if !forgeSweepDue(h, now, ttl) {
		return nil
	}
	stampSweep(h, now)

	st, err := statestore.Load()
	if err != nil || st == nil {
		return err
	}
	// Collect distinct repo query paths across all workspaces.
	queryPath := map[string]string{} // repo slug → a dir to run gh in
	for i := range st.Workspaces {
		for _, wt := range st.Workspaces[i].Worktrees {
			if wt.Repo == "" {
				continue
			}
			if _, ok := queryPath[wt.Repo]; !ok {
				queryPath[wt.Repo] = repoQueryDir(wt)
			}
		}
		for _, pr := range st.Workspaces[i].PRs {
			if pr.Repo != "" {
				if _, ok := queryPath[pr.Repo]; !ok {
					queryPath[pr.Repo] = filepath.Join(workspaceCodeRoot(), pr.Repo)
				}
			}
		}
	}
	// Query each distinct repo once; index PRs by repo → branch → PR and
	// repo → number → PR.
	byBranch := map[string]map[string]integration.PullRequest{}
	byNumber := map[string]map[int]integration.PullRequest{}
	for repo, dir := range queryPath {
		prs, err := forge.List(dir)
		if err != nil {
			debuglog.LogErr("workspaces._forge-refresh: List "+repo, err)
			continue
		}
		byBranch[repo] = map[string]integration.PullRequest{}
		byNumber[repo] = map[int]integration.PullRequest{}
		for _, pr := range prs {
			if pr.Repo == "" {
				pr.Repo = repo
			}
			if pr.Branch != "" {
				byBranch[repo][pr.Branch] = pr
			}
			byNumber[repo][pr.Number] = pr
		}
	}
	// Re-associate per workspace and persist.
	for i := range st.Workspaces {
		ws := &st.Workspaces[i]
		merged := associatePRs(ws, byBranch, byNumber)
		session := ws.SessionName
		prs := merged
		_ = statestore.UpdateWorkspace(session, func(w *statestore.Workspace) { w.PRs = prs })
	}
	return nil
}

// associatePRs computes a workspace's PR set: every worktree-branch match, plus
// any registered PR (refreshed from the query by number when present). Deduped
// by repo+number, sorted by state then number. Pure over the query indexes.
func associatePRs(ws *statestore.Workspace, byBranch map[string]map[string]integration.PullRequest, byNumber map[string]map[int]integration.PullRequest) []statestore.PR {
	seen := map[string]bool{}
	var out []statestore.PR
	add := func(pr integration.PullRequest, registered bool) {
		key := pr.Repo + "#" + strconv.Itoa(pr.Number)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, toStorePR(pr, registered))
	}
	for _, wt := range ws.Worktrees {
		if m := byBranch[wt.Repo]; m != nil {
			if pr, ok := m[wt.Branch]; ok {
				add(pr, false)
			}
		}
	}
	for _, existing := range ws.PRs {
		if !existing.Registered {
			continue
		}
		if m := byNumber[existing.Repo]; m != nil {
			if pr, ok := m[existing.Number]; ok {
				add(pr, true)
				continue
			}
		}
		add(fromStorePR(existing), true) // keep as-is when the query didn't cover it
	}
	sort.SliceStable(out, func(a, b int) bool {
		ra, rb := forgeStateRank(integration.ForgeState(out[a].State)), forgeStateRank(integration.ForgeState(out[b].State))
		if ra != rb {
			return ra < rb
		}
		return out[a].Number < out[b].Number
	})
	return out
}

// repoQueryDir picks a dir to run `gh` in for a worktree's repo: the worktree
// path (a real checkout) if present, else the main checkout under the code root.
func repoQueryDir(wt statestore.Worktree) string {
	if wt.Path != "" {
		return wt.Path
	}
	return filepath.Join(workspaceCodeRoot(), wt.Repo)
}

func toStorePR(pr integration.PullRequest, registered bool) statestore.PR {
	return statestore.PR{
		Number:         pr.Number,
		Repo:           pr.Repo,
		Title:          pr.Title,
		State:          string(pr.State),
		CI:             string(pr.CI),
		ReviewDecision: string(pr.ReviewDecision),
		Comments:       pr.Comments,
		URL:            pr.URL,
		Branch:         pr.Branch,
		UpdatedAt:      pr.UpdatedAt.Unix(),
		Registered:     registered,
	}
}

func fromStorePR(pr statestore.PR) integration.PullRequest {
	return integration.PullRequest{
		Number:         pr.Number,
		Repo:           pr.Repo,
		Title:          pr.Title,
		State:          integration.ForgeState(pr.State),
		CI:             integration.CIStatus(pr.CI),
		ReviewDecision: integration.ReviewDecision(pr.ReviewDecision),
		Comments:       pr.Comments,
		URL:            pr.URL,
		Branch:         pr.Branch,
		UpdatedAt:      time.Unix(pr.UpdatedAt, 0),
	}
}

func forgeSweepDue(h *tmuxhost.Client, now time.Time, ttl time.Duration) bool {
	ts, _ := h.ShowGlobalOption(optForgeSweptTs)
	secs, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil || secs <= 0 {
		return true
	}
	return now.Sub(time.Unix(secs, 0)) >= ttl
}

func stampSweep(h *tmuxhost.Client, now time.Time) {
	_ = h.SetGlobalOption(optForgeSweptTs, strconv.FormatInt(now.Unix(), 10))
}

// workspaceForgeRollup summarizes a workspace's PRs for the M-s row: the count
// and the leading (lowest-rank) state for the badge. Pure.
func workspaceForgeRollup(prs []statestore.PR) (count int, lead integration.ForgeState) {
	count = len(prs)
	bestRank := len(forgeStateOrder)
	for _, pr := range prs {
		if r := forgeStateRank(integration.ForgeState(pr.State)); r < bestRank {
			bestRank = r
			lead = integration.ForgeState(pr.State)
		}
	}
	return count, lead
}

// workspacePRAttention counts a workspace's PRs that want the user's eyes
// (failing CI or changes-requested on an open/draft PR). Pure.
func workspacePRAttention(prs []statestore.PR) int {
	n := 0
	for _, pr := range prs {
		if fromStorePR(pr).NeedsAttention() {
			n++
		}
	}
	return n
}
