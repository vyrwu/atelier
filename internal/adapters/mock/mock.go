// Package mock is a deterministic adapter with no external dependencies. It
// satisfies BOTH kernel ports — AIIntegration and ForgeIntegration — so the
// kernel's agent-fed capabilities (branch naming, summary, attention, agent
// popup) and its code-forge capability (per-workspace PR badge) are
// exercisable without Claude, `gh`, a network, or an API key — both as real
// config options (`[integrations] ai = "mock"`, `forge = "mock"`) and as the
// injectable adapters for kernel tests. It is the proof that the ports are
// genuinely swappable.
package mock

import (
	"context"
	"encoding/json"
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

// GenerateName produces a deterministic conventional name from the intent.
// It honors the kernel's requested format: session prompts (which specify
// the `auto/` format) get an `auto/` prefix, everything else `feat/`. When
// the kernel asks for the tag-aware two-line contract (issue #56), it also
// emits a second line: an existing tag echoed back if the intent mentions
// one, else empty.
func (Adapter) GenerateName(_ context.Context, systemPrompt, intent string) (string, error) {
	prefix := "feat"
	if strings.Contains(systemPrompt, "auto/") {
		prefix = "auto"
	}
	// The tag-aware contract wraps the text as "EXISTING TAGS: ...\nINTENT:
	// <text>"; name off the real intent, not the wrapper.
	name := prefix + "/" + nameSlug(intentBody(intent))
	if !strings.Contains(systemPrompt, "grouping tag") {
		return name, nil
	}
	return name + "\n" + mockTag(intent), nil
}

// intentBody returns the actual task text from a possibly tag-wrapped
// intent, stripping the "EXISTING TAGS: ...\nINTENT: " preamble.
func intentBody(intent string) string {
	if _, body, ok := strings.Cut(intent, "\nINTENT: "); ok {
		return body
	}
	return intent
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
	list, _, ok := strings.Cut(intent, "\nINTENT: ")
	if !ok {
		return ""
	}
	list = strings.TrimPrefix(list, "EXISTING TAGS: ")
	body := strings.ToLower(intentBody(intent))
	for _, tag := range strings.Split(list, ", ") {
		tag = strings.TrimSpace(tag)
		if tag != "" && tag != "(none)" && strings.Contains(body, tag) {
			return tag
		}
	}
	return ""
}

// OnStop flags attention on the target window (no transcript to summarize).
func (Adapter) OnStop(h *tmuxhost.Client, windowID string, _ []byte) error {
	if windowID == "" {
		if v, err := h.DisplayMessage("#{window_id}"); err == nil {
			windowID = v
		}
	}
	if windowID == "" {
		return nil
	}
	return workspace.SetAttention(h, windowID, true)
}

// Summarize sets a fixed recap so the summary slot is observably populated.
func (Adapter) Summarize(h *tmuxhost.Client, windowID, _ string) error {
	if windowID == "" {
		return nil
	}
	return workspace.SetRecap(h, windowID, "mock recap")
}

func (Adapter) EnsureHooks() error { return nil }

func (Adapter) AgentPopupSession(parentSessionID, parentWindowID string) string {
	return spec.SessionName(parentSessionID, parentWindowID)
}

// HasResumableState is always false — the mock keeps no state.
func (Adapter) HasResumableState(_ *tmuxhost.Client, _, _ string) bool { return false }

// --- ForgeIntegration --------------------------------------------------------

// MockForgeFixture is the file the mock forge reads to classify workspaces:
// a JSON object mapping a workspace's canonical worktree path (WorkspaceRef.Cwd)
// to a ForgeState string ("open"/"draft"/"merged"/"closed"). It lives under
// the active config home so it's naturally isolated per XDG_CONFIG_HOME. The
// demo sandbox (and tests) write it; a missing file means "no PRs anywhere".
const MockForgeFixture = "mock-forge.json"

// MockForgeFixturePath returns the fixture path under the active config home.
func MockForgeFixturePath() string {
	return filepath.Join(config.XDGConfigHome(), "atelier", MockForgeFixture)
}

// Status classifies the workspace's forge item by looking its worktree path
// up in the fixture map — deterministic, offline, no `gh`. An absent fixture
// or unmapped workspace is ForgeNone (badge cleared). This is the proof the
// forge port is swappable: the kernel's refresh + badge rendering run for
// real against fixture data.
func (Adapter) Status(ws integration.WorkspaceRef) (integration.ForgeStatus, error) {
	return integration.ForgeStatus{State: mockForgeState(ws.Cwd)}, nil
}

// Open is a no-op — the mock has no real PR to open in a browser.
func (Adapter) Open(integration.WorkspaceRef) error { return nil }

func mockForgeState(cwd string) integration.ForgeState {
	if cwd == "" {
		return integration.ForgeNone
	}
	data, err := os.ReadFile(MockForgeFixturePath())
	if err != nil {
		return integration.ForgeNone
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return integration.ForgeNone
	}
	return integration.ForgeState(m[cwd])
}
