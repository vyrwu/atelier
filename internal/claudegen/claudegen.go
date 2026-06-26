// Package claudegen calls Claude to generate short structured strings
// (branch names, session names). Replaces the bash flow where each name
// was generated inline via `claude --model haiku --print "..."`.
package claudegen

import (
	"bytes"
	"context"
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

// Generator wraps the claude CLI for short structured output.
type Generator struct {
	Model   string
	Timeout time.Duration
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
	// `--tools ""` disables every tool in the built-in set. claudegen's
	// purpose is "ask Claude for a short structured string" — names and
	// recaps. None of those need WebFetch / Bash / Read / Edit / etc.
	// Without this, a prompt containing a URL would invite Claude to
	// WebFetch it, which (a) leaks data, (b) slows generation by tens of
	// seconds, (c) sometimes causes Claude to bounce with a clarifying
	// question instead of the requested name. Hard-disable across the
	// board.
	args := []string{
		"-p", "--output-format", "text",
		"--model", model,
		"--setting-sources", "project,local",
		"--tools", "",
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, "claude", args...)
	// Bash uses `< /dev/null`. Without this, claude can inherit the parent
	// popup's pty as stdin and block trying to read interactive input.
	cmd.Stdin = nil
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("claude timed out after %s", timeout)
		}
		return "", fmt.Errorf("claude: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	if systemPrompt != "" {
		// Return raw output for system-prompt callers; they typically need
		// the full text to apply their own validation.
		return out.String(), nil
	}
	return firstLine(out.String()), nil
}

// BranchName generates a conventional-commits-style branch name from an
// intent description.
func (g *Generator) BranchName(intent string) (string, error) {
	return g.Run(fmt.Sprintf(
		`Generate a single conventional-commits git branch name (max 50 chars, lowercase letters + digits + hyphens only, no slashes other than feat/, fix/, chore/, refactor/, docs/, test/) for the following intent. Respond with ONLY the branch name on a single line.

Intent: %q`, intent))
}

// SessionName generates a kebab-case session name for a multi-repo task.
func (g *Generator) SessionName(intent string) (string, error) {
	return g.Run(fmt.Sprintf(
		`Generate a single kebab-case session name (max 30 chars, lowercase letters + digits + hyphens only, no slashes) for the following task. Respond with ONLY the name on a single line.

Task: %q`, intent))
}

// RecapFromTranscript asks Claude (default: haiku) to summarize the tail
// of a Claude transcript JSONL file as a single line ≤75 chars describing
// the latest action and any pending work. Returns ("", nil) for empty
// transcripts. Bash equivalent: tmux_generate_recap.
func (g *Generator) RecapFromTranscript(transcriptPath string) (string, error) {
	tail, err := tailTranscript(transcriptPath, 20)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(tail) == "" {
		return "", nil
	}
	prompt := fmt.Sprintf(`Summarize this Claude Code session as a single line, max 75 characters, lowercase, no trailing period, no quotes. Describe what just happened and what's pending if any. Examples:
  - running auth tests, two failing
  - finished workspace refactor, awaiting review
  - writing migration for users table

Transcript (last messages):
%s`, tail)
	out, err := g.Run(prompt)
	if err != nil {
		return "", err
	}
	if len(out) > 75 {
		runes := []rune(out)
		if len(runes) > 72 {
			out = string(runes[:72]) + "..."
		}
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
