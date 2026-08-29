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
// Every method is PULL: the kernel calls the adapter when it needs the
// capability. There is no push path — the recap and the attention verdict are
// both re-derived by RefreshRecap reading the agent's transcript, driven by the
// background refresh loop, so the agent needs no hook wired into its config.
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

	// RefreshRecap re-derives the workspace's recap AND its attention verdict
	// for windowID by reading the agent's latest session transcript under cwd
	// (the worktree), then writes them via workspace.SetRecap / SetAttention.
	// A single model pass returns both the one-line summary and whether the
	// agent is blocked waiting on the USER (vs idle-delegated or running) — only
	// the blocked case raises attention. The adapter throttles on the
	// transcript's mtime so it re-summarizes only when the agent did something,
	// making this cheap to call every loop tick. No-op when the workspace has no
	// agent transcript.
	RefreshRecap(h *tmuxhost.Client, windowID, cwd string) error

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

	// SummarizeWorkspace rolls the driver agent's recap and the workspace's PR
	// states up into a single workspace-level line for the M-s picker's summary
	// row ("PRs completed, work pending your action"). This is the daemon's
	// second AI call shape (WS-7); the caller change-detection-gates it (a hash
	// of recap + PR states) so it re-runs only when an input changed, and
	// budget-guards it. Returns "" when there is nothing to summarize.
	SummarizeWorkspace(ctx context.Context, intent, agentRecap string, prs []PullRequest) (string, error)
}
