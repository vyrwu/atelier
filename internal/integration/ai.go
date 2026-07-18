package integration

import (
	"context"

	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// AIIntegration is the single port for the AI agent that inhabits a
// workspace. ONE adapter (claude / codex / mock) satisfies ALL of it; config
// selects which is active. The kernel calls these when it needs the
// capability; the kernel owns the views, the attention sigil, the summary
// column, and the branch-naming policy (prompts + validation). The adapter
// owns only HOW its agent runs — resume semantics, project encoding, hook
// payload shape — behind this contract.
//
// The port is tmux-aware on purpose: atelier's substrate IS tmux, and the
// agent runs in a tmux popup whose per-window options carry the agent's
// queued prompt / resume state. Passing the host keeps that state where it
// already lives rather than forcing a lossy value-type round-trip through
// the kernel.
//
// Control-flow shapes differ per method and that is intentional:
//   - OpenAgent / SetPrompt / GenerateName / Summarize are pull (kernel calls).
//   - OnStop is push: the agent's stop-hook (installed by EnsureHooks) fires
//     and calls back through it; the adapter then uses kernel verbs
//     (workspace.SetAttention / SetRecap) to fill the kernel-owned slots.
type AIIntegration interface {
	// Name identifies the adapter (e.g. "claude"). Used in diagnostics.
	Name() string

	// DisplayName is the adapter's user-facing product name (e.g. "Claude
	// Code"). The kernel renders it in the tool selector's AI-agent entry so
	// the label reflects the active adapter rather than a generic "AI Agent".
	DisplayName() string

	// OpenAgent opens the agent in the current workspace's popup, reading
	// the window's queued prompt / resume state to build its launch command.
	OpenAgent(h *tmuxhost.Client) error

	// SetPrompt queues an initial task prompt (and optional workspace kind)
	// for the next OpenAgent on windowID. Empty prompt clears it.
	SetPrompt(h *tmuxhost.Client, windowID, prompt, kind string) error

	// GenerateName runs the agent's model with a kernel-supplied naming
	// instruction and returns its raw output (trailing newlines trimmed).
	// Used by workspace creator auto-mode; the KERNEL owns the instruction,
	// parses the lines (name, and optionally a grouping tag), and validates.
	GenerateName(ctx context.Context, systemPrompt, intent string) (string, error)

	// OnStop handles the agent's stop event (the hook payload). The adapter
	// resolves the target window, decides whether to flag attention, tracks
	// its own resume pointer, and refreshes the summary — all via the
	// kernel verbs workspace.SetAttention / SetRecap. windowID may be empty
	// (the adapter resolves it from the popup context).
	OnStop(h *tmuxhost.Client, windowID string, payload []byte) error

	// Summarize refreshes the workspace summary for windowID on demand,
	// from the agent's transcript under projectDir.
	Summarize(h *tmuxhost.Client, windowID, projectDir string) error

	// EnsureHooks installs whatever wiring the agent needs to call back into
	// the kernel on stop (attention + summary). Idempotent.
	EnsureHooks() error

	// AgentPopupSession returns the backing tmux popup-session name the agent
	// uses for the given parent workspace window. The workspace switcher uses
	// it to detect whether the agent popup is already running before deciding
	// to (re)open it on land.
	AgentPopupSession(parentSessionID, parentWindowID string) string

	// HasResumableState reports whether the agent has resumable state for the
	// workspace window at wid / worktree cwd (a tracked session id or an
	// on-disk transcript). The switcher uses it to decide whether to auto-open
	// the agent on land vs. leave a bare shell.
	HasResumableState(h *tmuxhost.Client, wid, cwd string) bool
}
