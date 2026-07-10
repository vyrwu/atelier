// Package mock is a deterministic AIIntegration adapter with no external
// dependencies. It exists so the kernel's agent-fed capabilities (branch
// naming, summary, attention, agent popup) are exercisable without Claude,
// a network, or an API key — both as a real config option
// (`[integrations] ai = "mock"`) and as the injectable adapter for kernel
// tests. It is the proof that the AI port is genuinely swappable.
package mock

import (
	"context"
	"regexp"
	"strings"

	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// Adapter satisfies integration.AIIntegration deterministically.
type Adapter struct{}

// New constructs the mock AI adapter.
func New() *Adapter { return &Adapter{} }

var _ integration.AIIntegration = (*Adapter)(nil)

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
// the `auto/` format) get an `auto/` prefix, everything else `feat/`.
func (Adapter) GenerateName(_ context.Context, systemPrompt, intent string) (string, error) {
	prefix := "feat"
	if strings.Contains(systemPrompt, "auto/") {
		prefix = "auto"
	}
	slug := strings.Trim(nameSlugRe.ReplaceAllString(strings.ToLower(intent), "-"), "-")
	words := strings.Split(slug, "-")
	if len(words) > 4 {
		words = words[:4]
	}
	slug = strings.Join(words, "-")
	if slug == "" {
		slug = "mock"
	}
	return prefix + "/" + slug, nil
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
