package claude

import (
	"time"

	"github.com/vyrwu/atelier/internal/config"
)

// DefaultMultiRepoSystemPrompt is the verbatim bash claude_start
// IMPL_SYS_MULTIREPO string — appended via --append-system-prompt when claude
// opens in a multi-repo workspace.
const DefaultMultiRepoSystemPrompt = `You are working on a multi-repo task. CWD is ~/code.
On startup, read ~/code/CLAUDE.md (create if missing) for a concise per-repo summary maintained across sessions. Use it to skip re-scanning.
Inspect ~/code/github/* to determine which repos are relevant to the user prompt below.
Continuously update ~/code/CLAUDE.md with newly discovered repo summaries — keep them VERY concise (purpose, primary language, key entry points). Prioritize token efficiency.`

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
}

// ModelConfig is `[ai.models]` — per-task model overrides. An empty value
// falls back to Config.Model.
type ModelConfig struct {
	// Naming is the model that names branches/sessions (M-n). Defaults to
	// "sonnet" — naming benefits from a sharper model than the haiku default.
	Naming string `toml:"naming"`
	// Recap is the model for one-line session recaps (M-s rows / attention).
	Recap string `toml:"recap"`
}

// PromptConfig is `[ai.prompts]` — capability-level system-prompt overrides.
type PromptConfig struct {
	// Recap overrides DefaultRecapSystemPrompt.
	Recap string `toml:"recap"`
	// MultiRepo is appended via --append-system-prompt when claude opens in
	// a multi-repo workspace. Empty falls back to DefaultMultiRepoSystemPrompt.
	MultiRepo string `toml:"multi_repo"`
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
	if cfg.Prompts.MultiRepo == "" {
		cfg.Prompts.MultiRepo = DefaultMultiRepoSystemPrompt
	}
	if cfg.Prompts.Recap == "" {
		cfg.Prompts.Recap = DefaultRecapSystemPrompt
	}
	return cfg, nil
}
