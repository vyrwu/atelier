// Package claudegen calls Claude to generate short structured strings
// (branch names, session names). Replaces the bash flow where each name
// was generated inline via `claude --model haiku --print "..."`.
package claudegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultModel is the Claude model used for name generation. haiku is the
// historical default — fast and cheap.
const DefaultModel = "haiku"

// DefaultTimeout caps how long we'll wait for Claude. 90s accommodates
// cold-starts on sonnet for branch-name generation; bash's equivalent
// has no timeout at all and runs under a single "Building workspace..."
// spinner, so users are accustomed to a multi-second wait here.
const DefaultTimeout = 90 * time.Second

// Usage is the token accounting for a single claude CLI call, read from the
// `--output-format json` envelope. Zero-value when the CLI emitted no JSON
// (older builds / stubbed binaries) — callers treat that as "unknown".
type Usage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	CostUSD             float64
}

// Generator wraps the claude CLI for short structured output.
type Generator struct {
	Model   string
	Timeout time.Duration

	// LastUsage holds the token accounting reported by the most recent
	// Run/RunWithSystemPrompt call. Each call site constructs a fresh
	// Generator (New()), so this is effectively per-call. Zero when the CLI
	// returned no usage metadata.
	LastUsage Usage
}

// New returns a Generator with the default model + timeout.
func New() *Generator { return &Generator{Model: DefaultModel, Timeout: DefaultTimeout} }

// Run calls claude with the given prompt and returns the trimmed first
// line of stdout. Output beyond the first line is discarded (defends
// against models that occasionally add explanation).
func (g *Generator) Run(prompt string) (string, error) {
	return g.RunWithSystemPrompt("", prompt)
}

// RunWithSystemPrompt invokes claude with --system-prompt + --print. When
// systemPrompt is empty, behaves like Run.
//
// Returns full stdout (not just the first line) — callers that want
// single-line output can post-process.
func (g *Generator) RunWithSystemPrompt(systemPrompt, prompt string) (string, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return "", fmt.Errorf("claude CLI not on PATH: %w", err)
	}
	model := g.Model
	if model == "" {
		model = DefaultModel
	}
	timeout := g.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// `--setting-sources project,local` is critical: without it the Claude
	// CLI loads the user's full global settings (incl. heavy MCP servers,
	// auth checks, etc.), turning what should be a sub-2s name-generation
	// call into a 30+ second cold-start that consistently times out.
	// Bash's tmux_workspace_build uses the same flags.
	//
	// `--output-format json` gives us the `usage` envelope (input/output/cache
	// tokens + cost) alongside the text, so every background call can be
	// metered — see `atelier ai usage`. The text lands in the `result` field;
	// parseCLIResult extracts it (and falls back to raw stdout for a
	// non-JSON/stubbed claude).
	//
	// `--tools ""` disables every tool in the built-in set. claudegen's
	// purpose is "ask Claude for a short structured string" — names and
	// recaps. None of those need WebFetch / Bash / Read / Edit / etc.
	// Without this, a prompt containing a URL would invite Claude to
	// WebFetch it, which (a) leaks data, (b) slows generation by tens of
	// seconds, (c) sometimes causes Claude to bounce with a clarifying
	// question instead of the requested name. Hard-disable across the board.
	args := []string{
		"-p", "--output-format", "json",
		"--model", model,
		"--setting-sources", "project,local",
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	// `--tools` became variadic (`--tools <tools...>`) in recent claude CLIs,
	// so a trailing `--tools "" <prompt>` greedily swallows the prompt as a
	// tool name and the call fails with "Input must be provided". Keep it LAST
	// (nothing after it to eat) and feed the prompt on stdin instead of as a
	// positional arg — stdin can't be captured by any flag.
	args = append(args, "--tools", "")

	cmd := exec.CommandContext(ctx, "claude", args...)
	// Prompt via stdin (see above). A bounded reader EOFs immediately after the
	// prompt, so claude never blocks reading interactive input the way it would
	// if it inherited the parent popup's pty.
	cmd.Stdin = strings.NewReader(prompt)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("claude timed out after %s", timeout)
		}
		return "", fmt.Errorf("claude: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	text, usage := parseCLIResult(out.String())
	g.LastUsage = usage
	if systemPrompt != "" {
		// Return raw text for system-prompt callers; they typically need the
		// full output to apply their own validation.
		return text, nil
	}
	return firstLine(text), nil
}

// parseCLIResult extracts the model's text output + token usage from a
// `claude --output-format json` envelope. When the output isn't that JSON
// shape (an older/non-JSON claude, or a stubbed binary in tests) it falls
// back to treating the whole string as the text with zero usage — so the
// call still works, just unmetered.
func parseCLIResult(raw string) (string, Usage) {
	var r struct {
		Result string  `json:"result"`
		Cost   float64 `json:"total_cost_usd"`
		Usage  struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &r); err != nil {
		return raw, Usage{}
	}
	return r.Result, Usage{
		InputTokens:         r.Usage.InputTokens,
		OutputTokens:        r.Usage.OutputTokens,
		CacheCreationTokens: r.Usage.CacheCreationInputTokens,
		CacheReadTokens:     r.Usage.CacheReadInputTokens,
		CostUSD:             r.Cost,
	}
}

// recapFallbackMaxRunes is the ceiling for the fallback recap path. Mirrors
// claude.RecapMaxRunes (this package can't import that one without a cycle);
// ~one wide line, since the recap shows on one truncated-to-width line.
const recapFallbackMaxRunes = 120

// RecapFromTranscript asks Claude (default: haiku) to summarize the tail
// of a Claude transcript JSONL file as a single line describing the latest
// action and any pending work. Returns ("", nil) for empty transcripts.
// Bash equivalent: tmux_generate_recap. This is the fallback path; the
// primary recap comes from latestRecap → RunWithSystemPrompt + truncateLine.
func (g *Generator) RecapFromTranscript(transcriptPath string) (string, error) {
	tail, err := tailTranscript(transcriptPath, 20)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(tail) == "" {
		return "", nil
	}
	prompt := fmt.Sprintf(`Summarize this Claude Code session as a single line (no line breaks), up to ~120 characters, lowercase, no trailing period, no quotes. It's shown on one line in the UI, so stay tight. Describe what just happened and what's pending if any. Examples:
  - running auth tests, two failing
  - finished workspace refactor, awaiting review
  - writing migration for users table

Transcript (last messages):
%s`, tail)
	out, err := g.Run(prompt)
	if err != nil {
		return "", err
	}
	if runes := []rune(out); len(runes) > recapFallbackMaxRunes {
		out = string(runes[:recapFallbackMaxRunes-1]) + "…"
	}
	return out, nil
}

func tailTranscript(path string, lastN int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > lastN {
		lines = lines[len(lines)-lastN:]
	}
	return strings.Join(lines, "\n"), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
