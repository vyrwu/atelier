package claude

import (
	"time"

	"github.com/vyrwu/atelier/internal/config"
)

// DefaultWorkspaceSystemPrompt is appended via --append-system-prompt when the
// driver agent opens in an atelier workspace. It describes the intent-workspace
// layout (the drawing's model): the agent's cwd is the workspace root, each
// repo/branch it works on appears as a <repo>/<branch> symlink into a real git
// worktree, and atelier CLI verbs (also exposed over MCP) let it grow the
// workspace and register the PRs it opens.
const DefaultWorkspaceSystemPrompt = `You are the driver agent for an atelier WORKSPACE — a single task ("intent") that may span multiple git repositories.
Your working directory is the workspace root. Each repository/branch you work on appears as a symlink "<repo>/<branch>" pointing at its real git worktree, so ` + "`ls`" + ` shows the worktrees in play. Edit files through those paths.
Useful atelier commands (also available as MCP tools):
  - atelier workspace worktree add <owner/repo> <branch>  — add a repo+branch to this workspace (creates the worktree + symlink)
  - atelier workspace worktree list                       — list this workspace's worktrees
  - atelier workspace context                             — show the workspace's intent, worktrees, and open PRs
  - atelier pr register <url>                             — register a PR you opened so atelier tracks it in the Changes view
Keep your footprint tight and prefer these verbs over re-implementing workspace bookkeeping by hand.`

// DefaultRecapSystemPrompt is the recap summarizer's system prompt.
//
// The recap wraps in the picker (a long summary flows onto continuation
// rows hanging under the workspace name), so there's no tight character
// budget any more. RecapMaxRunes is now just a generous ceiling that
// truncateLine enforces as a safety net against a runaway summary; normal
// output is expected to be a single tight clause. The one hard rule the
// prompt must keep is ONE line (no embedded newlines) — truncateLine keeps
// only the first line, and wrapping is the picker's job, not the model's.
const DefaultRecapSystemPrompt = `You read the tail of a Claude Code session transcript (newline-delimited JSON message events), optionally followed by a "Workspace code changes (git delta)" section summarizing what actually changed in the repo (uncommitted diff, changed files, and a patch).

Output EXACTLY two lines:

Line 1 — the attention verdict, one of:
  ATTENTION: blocked   — the agent is waiting on the USER: it asked a question, presented options/a plan to approve, or hit an error needing a human decision. The last thing in the transcript is an assistant turn addressed to the user, with no further work queued.
  ATTENTION: idle      — the agent finished cleanly, or is waiting on sub-agents / tools / background tasks to complete. No user action is needed.
  ATTENTION: running   — the agent is actively executing (mid tool-use, unresolved tool calls).
Decide from the transcript tail: the last assistant message's intent, any unresolved tool_use calls, and the stop reason. Only "blocked" means the user's eyes are needed — be conservative, prefer "idle" when unsure.

Line 2 — the recap, ONE line (NO line breaks), up to ~120 characters, shown truncated-to-width in the UI, so stay tight and skimmable, never padded.
Content priority (lead with the most important):
  1. Pending user action — what the user must do/answer NOW. If the agent asked a question with options, surface options.
  2. What actually changed — prefer the git delta (files/areas touched, kind of change) over what the conversation only discussed; when code changed, name it concretely.
  3. Latest agent action — past-tense verb + object.
  4. Current objective.

Style rules (apply to line 2):
  - Be terse. Use abbreviations (cfg, db, PR, deps). Drop articles.
  - Concrete specifics over vague descriptions.
  - NO line breaks, NO leading/trailing whitespace, NO quotes, NO labels like "Recap:", NO code blocks, NO markdown.

Output ONLY the two lines (the ATTENTION verdict, then the recap), nothing else.`

// maxTranscriptRunes bounds the transcript tail fed to the summarizer — the
// last several turns, which is all the classifier + one-line recap need. Kept
// modest on purpose: the observer re-summarizes a churning workspace every
// tick, so with many parallel agents this per-call input size is the dominant
// token cost. ~8k runes ≈ ~2k tokens keeps that in check without starving the
// summary of recent context.
const maxTranscriptRunes = 8000

// runningWindow is how recently the transcript must have been written for a
// workspace to count as "running" (blue dot) when there's no brand-new content
// this tick. Set above the loop's tick interval so a single quiet tick doesn't
// flip an actively-working agent to idle; an agent that stops writing for
// longer than this settles to idle.
const runningWindow = 90 * time.Second

// RecapMaxRunes is the ceiling truncateLine enforces as a safety net. The
// recap shows on one line in the picker (truncated to width), so ~one wide
// line's worth is plenty — this only guards against a runaway summary and
// avoids spending summarizer tokens on text that never shows. Exported so
// callers (and tests) share the limit instead of duplicating the literal.
const RecapMaxRunes = 120

// Config is the claude adapter's slice of the centralised `[ai]` section:
// the model + prompt tuning the active adapter interprets. Provider selection
// (`[ai] provider`, `[forge] provider`) is owned by the composition root
// (cmd/atelier/integrations.go) and deliberately absent here — this adapter is
// only built once `provider = "claude"` has already been chosen.
//
// The keys are uniform across adapters (any adapter reads `model`,
// `[ai.models]`, `[ai.prompts]`); the VALUES are provider-specific — "haiku"
// and "sonnet" are Claude model aliases, and the default prompts are
// Claude-authored, so their defaults live here, not in a neutral config layer.
type Config struct {
	// Model is the default Claude model for AI tasks that don't set their
	// own under [ai.models]. Default: haiku.
	Model string `toml:"model"`
	// Models holds per-task model overrides; empty means "use Model".
	Models ModelConfig `toml:"models"`
	// Prompts holds capability-level system-prompt overrides; empty means
	// "use the built-in default".
	Prompts PromptConfig `toml:"prompts"`
	// MCP registers atelier's stdio MCP server (atelier mcp serve) into the
	// interactive driver agent via --mcp-config, so the agent can add
	// worktrees / register PRs / read workspace context. Default true; set
	// `mcp = false` under [ai] to launch Claude without it. Background naming/
	// recap/summary calls never get MCP regardless (they run --tools "").
	MCP bool `toml:"mcp"`
}

// ModelConfig is `[ai.models]` — per-task model overrides. An empty value
// falls back to Config.Model.
type ModelConfig struct {
	// Naming is the model that names workspaces (M-n). Defaults to "sonnet" —
	// naming benefits from a sharper model than the haiku default.
	Naming string `toml:"naming"`
	// Recap is the model for one-line per-agent recaps (M-s rows / attention).
	Recap string `toml:"recap"`
	// Summary is the model for the workspace-level rollup summary (WS-7).
	// Defaults to the global Model (haiku) — it's a cheap heartbeat call.
	Summary string `toml:"summary"`
}

// PromptConfig is `[ai.prompts]` — capability-level system-prompt overrides.
type PromptConfig struct {
	// Recap overrides DefaultRecapSystemPrompt.
	Recap string `toml:"recap"`
	// Workspace is appended via --append-system-prompt when the driver agent
	// opens in a workspace. Empty falls back to DefaultWorkspaceSystemPrompt.
	Workspace string `toml:"workspace"`
	// Summary overrides DefaultSummarySystemPrompt (the workspace rollup).
	Summary string `toml:"summary"`
}

// NamingModel resolves the model for branch/session naming: the per-task
// override if set, else the global default.
func (c Config) NamingModel() string {
	if c.Models.Naming != "" {
		return c.Models.Naming
	}
	return c.Model
}

// RecapModel resolves the model for recaps: the per-task override if set, else
// the global default.
func (c Config) RecapModel() string {
	if c.Models.Recap != "" {
		return c.Models.Recap
	}
	return c.Model
}

// SummaryModel resolves the model for the workspace rollup summary.
func (c Config) SummaryModel() string {
	if c.Models.Summary != "" {
		return c.Models.Summary
	}
	return c.Model
}

// DefaultSummarySystemPrompt drives the workspace-level rollup (WS-7): one line
// folding the driver agent's recap + the workspace's PR states into a status a
// user scans in M-s ("PRs completed, work pending your action").
const DefaultSummarySystemPrompt = `You write a ONE-LINE status for a development WORKSPACE (a single task that may span repos), for a picker row the user scans at a glance.

You are given: the workspace INTENT, the driver agent's latest recap, and a list of the workspace's pull requests with their CI + review state.

Output ONE line (NO line breaks), up to ~100 characters, terse and skimmable. Lead with what the USER should know now:
  1. Anything blocking / needing the user (changes-requested review, failing CI, agent waiting).
  2. Overall progress ("3 PRs open, all green", "PRs merged, work pending your action").
  3. Else the agent's current objective.
NO quotes, NO labels, NO markdown, NO trailing whitespace. Output ONLY the line.`

func DefaultConfig() Config {
	return Config{
		Model:  "haiku",
		Models: ModelConfig{Naming: "sonnet"},
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := config.LoadSection("ai", &cfg); err != nil {
		return cfg, err
	}
	if cfg.Model == "" {
		cfg.Model = "haiku"
	}
	if cfg.Prompts.Workspace == "" {
		cfg.Prompts.Workspace = DefaultWorkspaceSystemPrompt
	}
	if cfg.Prompts.Recap == "" {
		cfg.Prompts.Recap = DefaultRecapSystemPrompt
	}
	if cfg.Prompts.Summary == "" {
		cfg.Prompts.Summary = DefaultSummarySystemPrompt
	}
	return cfg, nil
}
