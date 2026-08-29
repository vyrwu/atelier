// Package mock is a deterministic adapter with no external dependencies. It
// satisfies BOTH kernel ports — AIIntegration and ForgeIntegration — so the
// kernel's agent-fed capabilities (branch naming, summary, attention, agent
// popup) and its code-forge capability (per-workspace PR badge) are
// exercisable without Claude, `gh`, a network, or an API key — both as real
// config options (`[ai] provider = "mock"`, `[forge] provider = "mock"`) and
// as the injectable adapters for kernel tests. It is the proof that the ports are
// genuinely swappable.
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vyrwu/atelier/internal/config"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// Adapter satisfies integration.AIIntegration + integration.ForgeIntegration
// deterministically.
type Adapter struct{}

// New constructs the mock adapter.
func New() *Adapter { return &Adapter{} }

var (
	_ integration.AIIntegration    = (*Adapter)(nil)
	_ integration.ForgeIntegration = (*Adapter)(nil)
)

func (Adapter) Name() string        { return "mock" }
func (Adapter) DisplayName() string { return "Mock AI" }

var spec = &popup.WorkspaceScoped{Tool: "mockai", DefaultCmd: "${SHELL:-/bin/sh}", Description: "mock agent"}

// OpenAgent opens a plain shell in the workspace popup (no real agent).
func (Adapter) OpenAgent(h *tmuxhost.Client) error {
	return popup.OpenWorkspaceScoped(h, spec)
}

// SetPrompt is a no-op for the mock (nothing consumes a queued prompt).
func (Adapter) SetPrompt(_ *tmuxhost.Client, _, _, _ string) error { return nil }

var nameSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// GenerateName satisfies the kernel's intent-first naming contract
// deterministically. The kernel's workspace-naming system prompt asks for four
// "KEY: value" lines (TITLE/SLUG/TAG/REPOS); the mock emits them off the
// intent + repo index in the wrapped input, echoing an existing tag when the
// intent mentions one and the first indexed repo as the touched repo. This is
// the proof the naming port is genuinely swappable — offline, no Claude.
func (Adapter) GenerateName(_ context.Context, _, intent string) (string, error) {
	body := intentBody(intent)
	slug := nameSlug(body)
	title := titleize(body)
	tag := mockTag(intent)
	repos := mockRepos(intent)
	return "TITLE: " + title + "\nSLUG: " + slug + "\nTAG: " + tag + "\nREPOS: " + repos, nil
}

// intentBody returns the actual task text from the wrapped naming input,
// stripping the "REPO INDEX: …\nEXISTING TAGS: …\nINTENT: " preamble.
func intentBody(intent string) string {
	if _, body, ok := strings.Cut(intent, "\nINTENT: "); ok {
		return body
	}
	return intent
}

// titleize renders the first few intent words as a Title Case title.
func titleize(text string) string {
	words := strings.Fields(text)
	if len(words) > 4 {
		words = words[:4]
	}
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	if len(words) == 0 {
		return "Mock Workspace"
	}
	return strings.Join(words, " ")
}

// mockRepos echoes the first repo in the REPO INDEX preamble (so the sandbox
// deterministically materializes a worktree), or "" when none.
func mockRepos(intent string) string {
	idx, _, ok := strings.Cut(intent, "\nEXISTING TAGS:")
	if !ok {
		return ""
	}
	idx = strings.TrimPrefix(idx, "REPO INDEX:\n")
	for _, line := range strings.Split(idx, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "(none)" {
			return line
		}
	}
	return ""
}

// nameSlug is the 2-5-word kebab slug the mock derives from task text.
func nameSlug(text string) string {
	slug := strings.Trim(nameSlugRe.ReplaceAllString(strings.ToLower(text), "-"), "-")
	words := strings.Split(slug, "-")
	if len(words) > 4 {
		words = words[:4]
	}
	slug = strings.Join(words, "-")
	if slug == "" {
		slug = "mock"
	}
	return slug
}

// mockTag echoes back the first EXISTING TAG the intent body mentions (so
// the mock deterministically exercises the "reuse vocabulary" behavior),
// else empty (no tag).
func mockTag(intent string) string {
	list := ""
	for _, line := range strings.Split(intent, "\n") {
		if rest, ok := strings.CutPrefix(line, "EXISTING TAGS: "); ok {
			list = rest
			break
		}
	}
	if list == "" {
		return ""
	}
	body := strings.ToLower(intentBody(intent))
	for _, tag := range strings.Split(list, ", ") {
		tag = strings.TrimSpace(tag)
		if tag != "" && tag != "(none)" && strings.Contains(body, tag) {
			return tag
		}
	}
	return ""
}

// RefreshRecap sets a fixed recap + a blocked agent status so both slots are
// observably populated (the mock always claims the agent is waiting on you).
func (Adapter) RefreshRecap(h *tmuxhost.Client, windowID, _ string) error {
	if windowID == "" {
		return nil
	}
	if err := workspace.SetRecap(h, windowID, "mock recap"); err != nil {
		return err
	}
	return workspace.SetAgentStatus(h, windowID, workspace.AgentBlocked)
}

func (Adapter) AgentPopupSession(parentSessionID, parentWindowID string) string {
	return spec.SessionName(parentSessionID, parentWindowID)
}

// HasResumableState is always false — the mock keeps no state.
func (Adapter) HasResumableState(_ *tmuxhost.Client, _, _ string) bool { return false }

// SummarizeWorkspace returns a deterministic workspace-level line so the M-s
// picker's summary row is observably populated offline. It folds in the PR
// count so the output changes with the workspace's shape (proof the summarize
// call sees the PR states), and returns "" when there is nothing to summarize.
func (Adapter) SummarizeWorkspace(_ context.Context, intent, agentRecap string, prs []integration.PullRequest) (string, error) {
	if intent == "" && agentRecap == "" && len(prs) == 0 {
		return "", nil
	}
	return fmt.Sprintf("mock workspace summary (%d PRs)", len(prs)), nil
}

// --- ForgeIntegration --------------------------------------------------------

// ForgePR is the type-safe fixture entry for the mock forge — a subset of
// integration.PullRequest's fields, expressed as strings so the fixture is
// hand-writable and internal/seed can construct it without importing the
// kernel's enum vocabulary indirectly. State/CI/ReviewDecision are the raw
// kernel-vocabulary strings ("open"/"draft"/..., "pass"/"fail"/..., etc.).
type ForgePR struct {
	Number         int    `json:"number"`
	Repo           string `json:"repo"`
	Title          string `json:"title"`
	State          string `json:"state"`
	CI             string `json:"ci,omitempty"`
	ReviewDecision string `json:"review_decision,omitempty"`
	Comments       int    `json:"comments,omitempty"`
	URL            string `json:"url,omitempty"`
	Branch         string `json:"branch,omitempty"`
}

// toPullRequest maps a fixture entry onto the kernel's rich PR record.
func (p ForgePR) toPullRequest() integration.PullRequest {
	return integration.PullRequest{
		Number:         p.Number,
		Repo:           p.Repo,
		Title:          p.Title,
		State:          integration.ForgeState(p.State),
		CI:             integration.CIStatus(p.CI),
		ReviewDecision: integration.ReviewDecision(p.ReviewDecision),
		Comments:       p.Comments,
		URL:            p.URL,
		Branch:         p.Branch,
	}
}

// MockForgeFixture is the file the mock forge reads to list PRs: a JSON object
// mapping a repo checkout path (the repoPath passed to List) to an array of
// ForgePR entries. It lives under the active config home so it's naturally
// isolated per XDG_CONFIG_HOME. The demo sandbox (and tests) write it; a
// missing file — or a repoPath absent from the map — means "no PRs".
const MockForgeFixture = "mock-forge.json"

// MockForgeFixturePath returns the fixture path under the active config home.
func MockForgeFixturePath() string {
	return filepath.Join(config.XDGConfigHome(), "atelier", MockForgeFixture)
}

// List returns the pull requests for the repo checked out at repoPath by
// looking repoPath up in the fixture map — deterministic, offline, no `gh`. An
// absent fixture or unmapped repoPath yields nil (the Changes view degrades to
// "no PRs"). This is the proof the forge port is swappable: the kernel's
// refresh + rendering run for real against fixture data.
func (Adapter) List(repoPath string) ([]integration.PullRequest, error) {
	if repoPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(MockForgeFixturePath())
	if err != nil {
		return nil, nil
	}
	var m map[string][]ForgePR
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil
	}
	entries := m[repoPath]
	if len(entries) == 0 {
		return nil, nil
	}
	prs := make([]integration.PullRequest, len(entries))
	for i, e := range entries {
		prs[i] = e.toPullRequest()
	}
	return prs, nil
}

// Open is a no-op — the mock has no real PR to open in a browser.
func (Adapter) Open(integration.PullRequest) error { return nil }

// Close is a no-op — the mock is stateless and has no real forge to mutate.
func (Adapter) Close(integration.PullRequest) error { return nil }
