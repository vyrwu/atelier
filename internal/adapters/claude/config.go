package claude

import "github.com/vyrwu/atelier/internal/config"

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
const DefaultRecapSystemPrompt = `You read a Claude Code session transcript snippet (newline-delimited JSON message events).

Output ONE line (NO line breaks), up to ~120 characters. It's shown on one line in the UI (truncated if too wide), so stay tight and skimmable, never padded.

Content priority (lead with the most important):
  1. Pending user action — what the user must do/answer NOW. If the agent asked a question with options, surface options.
  2. Latest agent action — past-tense verb + object.
  3. Current objective.

Style rules:
  - Be terse. Use abbreviations (cfg, db, PR, deps). Drop articles.
  - Concrete specifics over vague descriptions.
  - NO line breaks, NO leading/trailing whitespace, NO quotes, NO labels like "Recap:", NO code blocks, NO markdown.

Output ONLY the recap line, nothing else.`

// RecapMaxRunes is the ceiling truncateLine enforces as a safety net. The
// recap shows on one line in the picker (truncated to width), so ~one wide
// line's worth is plenty — this only guards against a runaway summary and
// avoids spending summarizer tokens on text that never shows. Exported so
// callers (and tests) share the limit instead of duplicating the literal.
const RecapMaxRunes = 120

// Config is the claude plugin's own config, loaded from `[claude]`.
type Config struct {
	// MultiRepoSystemPrompt is appended via --append-system-prompt when
	// claude opens in a multi-repo workspace. Inline string (not a path).
	// Empty falls back to DefaultMultiRepoSystemPrompt.
	MultiRepoSystemPrompt string `toml:"multi_repo_system_prompt"`
	// RecapModel is the Claude model used to summarize the latest
	// transcript into a one-line @attention_recap. Default: haiku.
	RecapModel string `toml:"recap_model"`
	// RecapSystemPrompt overrides DefaultRecapSystemPrompt.
	RecapSystemPrompt string `toml:"recap_system_prompt"`
}

func DefaultConfig() Config {
	return Config{
		RecapModel:            "haiku",
		MultiRepoSystemPrompt: DefaultMultiRepoSystemPrompt,
		RecapSystemPrompt:     DefaultRecapSystemPrompt,
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := config.LoadSection("claude", &cfg); err != nil {
		return cfg, err
	}
	if cfg.MultiRepoSystemPrompt == "" {
		cfg.MultiRepoSystemPrompt = DefaultMultiRepoSystemPrompt
	}
	if cfg.RecapSystemPrompt == "" {
		cfg.RecapSystemPrompt = DefaultRecapSystemPrompt
	}
	return cfg, nil
}
