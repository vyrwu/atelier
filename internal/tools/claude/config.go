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
// Hard length constraint: the post-processor (truncateLine) silently
// truncates anything over RecapMaxRunes — the recap renders in a fixed-
// width picker row, so overflow gets cut mid-word. The prompt repeats
// the limit multiple times and tells the model the consequence of
// overshooting; in practice this gives noticeably tighter outputs than
// a single mention.
const DefaultRecapSystemPrompt = `You read a Claude Code session transcript snippet (newline-delimited JSON message events).

HARD LIMIT: Output ONE line, ≤75 characters total (count characters, not words). Anything past character 75 is dropped mid-word in the UI — write tight.

Content priority (drop later items if needed to fit):
  1. Pending user action — what the user must do/answer NOW. If the agent asked a question with options, surface options.
  2. Latest agent action — past-tense verb + object, ≤4 words.
  3. Current objective — one or two words.

Style rules:
  - Be terse. Use abbreviations (cfg, db, PR, deps). Drop articles.
  - Concrete specifics over vague descriptions.
  - NO leading/trailing whitespace, NO quotes, NO labels like "Recap:", NO code blocks, NO markdown.

Output ONLY the recap line, nothing else. ≤75 chars.`

// RecapMaxRunes is the hard cap enforced by the post-processor after
// the system-prompt advisory. Exported so callers (and tests) can share
// the limit instead of duplicating the literal 75.
const RecapMaxRunes = 75

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
