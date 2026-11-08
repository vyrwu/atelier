package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Kind is a coding agent a space can run.
type Kind string

const (
	Claude Kind = "claude"
	Codex  Kind = "codex"
	Grok   Kind = "grok"
)

// Kinds is the stable order used by the new-space and swap pickers.
func Kinds() []Kind { return []Kind{Claude, Codex, Grok} }

// Valid distinguishes an explicit CLI choice from legacy empty state.
func Valid(name string) bool {
	for _, k := range Kinds() {
		if string(k) == name {
			return true
		}
	}
	return false
}

// Of preserves the default for legacy state and unrecognised config values.
func Of(name string) Kind {
	if Valid(name) {
		return Kind(name)
	}
	return Claude
}

// Next cycles through every agent, including a fresh conversation with oneself.
func (k Kind) Next() Kind {
	kinds := Kinds()
	for i, candidate := range kinds {
		if candidate == k {
			return kinds[(i+1)%len(kinds)]
		}
	}
	return Claude
}

func (k Kind) Label() string {
	switch k {
	case Codex:
		return "Codex"
	case Grok:
		return "Grok Build"
	default:
		return "Claude Code"
	}
}

// Bin is the native CLI that runs the agent.
func (k Kind) Bin() string { return string(k) }

// codexFlags go on every interactive Codex launch. Codex runs unsandboxed and
// without approval prompts: a worktree's git data lives in its source repo,
// outside the space, so the sandbox would stop every commit. The -c is
// load-bearing beyond what it sets: with no override at all, Codex attaches to a
// running app-server daemon if there is one, and runs its hooks in the daemon's
// environment, which has no ATELIER_SESSION.
const codexFlags = "--dangerously-bypass-approvals-and-sandbox -c check_for_update_on_startup=false"

// LaunchCmd is the shell command that starts the agent fresh in a space with
// prompt as its opening message.
func (k Kind) LaunchCmd(prompt string) string {
	if k == Grok {
		return "grok --trust" + codexArg(prompt)
	}
	if k == Codex {
		return "codex " + codexFlags + codexArg(prompt)
	}
	return "claude " + shellQuote(prompt)
}

// ResumeCmd is the shell command that continues the conversation id, with
// prompt (if any) as its next message — or, with no id or one that no longer
// resumes, starts the agent fresh with it.
func (k Kind) ResumeCmd(id, prompt string) string {
	if k == Grok {
		fresh := k.LaunchCmd(prompt)
		if id == "" {
			return fresh
		}
		return k.LaunchCmd("") + " --resume " + shellQuote(id) + codexArg(prompt) + " || " + fresh
	}
	fresh := "claude"
	if prompt != "" {
		fresh += " " + shellQuote(prompt)
	}
	if k == Codex {
		fresh = "codex " + codexFlags + codexArg(prompt)
	}
	if id == "" {
		return fresh
	}
	if k == Codex {
		return "codex resume " + codexFlags + " " + shellQuote(id) + codexArg(prompt) + " || " + fresh
	}
	resume := "claude --resume " + shellQuote(id)
	if prompt != "" {
		resume += " " + shellQuote(prompt)
	}
	return resume + " || " + fresh
}

// codexArg is prompt as Codex's trailing argument, or nothing. Codex runs an
// opening message that starts with "!" as a shell command, so one never does;
// and it follows "--", or a prompt starting with "-" would be read as a flag.
func codexArg(prompt string) string {
	if prompt == "" {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(prompt), "!") {
		prompt = "Task: " + prompt
	}
	return " -- " + shellQuote(prompt)
}

// askTimeout bounds one non-interactive run, such as a title.
const askTimeout = 5 * time.Minute

// Ask runs the agent once, non-interactively, on prompt with input piped after
// it, and returns its answer. It runs from a scratch directory without a
// space's identity, so it cannot load its guide, become its resumed conversation,
// or rewrite its status. Claude and Codex disable tools/config and persistence;
// Grok disables built-in tools.
// model applies to Claude only; other agents use their configured default.
func (k Kind) Ask(prompt, input, model string) (string, error) {
	dir, err := os.MkdirTemp("", "atelier-ask-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	answer := filepath.Join(dir, "answer.md")
	var cmd *exec.Cmd
	switch k {
	case Grok:
		// #nosec G204 G702 -- Fixed CLI and separate prompt argument in a private scratch directory.
		cmd = exec.CommandContext(ctx, "grok", "-p", prompt+"\n"+input, "--tools", "", "--no-subagents", "--disable-web-search", "--max-turns", "1", "--output-format", "plain")
	case Codex:
		// #nosec G204 G702 -- Runs a fixed agent binary with separate arguments in a private scratch directory.
		cmd = exec.CommandContext(ctx, "codex", "exec", "-C", dir, "--skip-git-repo-check", "--ephemeral",
			"--ignore-user-config", "--ignore-rules", "-s", "read-only", "--disable", "hooks", "-o", answer, prompt)
	default:
		args := []string{"-p", "--no-session-persistence", "--strict-mcp-config", "--tools", "",
			"--settings", `{"disableAllHooks":true}`}
		if model != "" {
			args = append(args, "--model", model)
		}
		// #nosec G204 G702 -- Runs a fixed agent binary with separate arguments in a private scratch directory.
		cmd = exec.CommandContext(ctx, "claude", append(args, prompt)...)
	}
	cmd.Dir = dir
	cmd.Env = withoutSpace(os.Environ())
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if k == Codex {
		// #nosec G304 -- answer is inside the private directory created by os.MkdirTemp above.
		if out, err = os.ReadFile(answer); err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(string(out)), nil
}

// withoutSpace drops a space's identity from env, so the hooks of a run atelier
// starts on the side can't mistake it for the space's agent.
func withoutSpace(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "ATELIER_SESSION=") && !strings.HasPrefix(e, "ATELIER_SLUG=") &&
			!strings.HasPrefix(e, "ATELIER_AGENT=") {
			out = append(out, e)
		}
	}
	return out
}

// Prepare readies dir for agent k: folder trust and atelier's MCP server, so it
// starts without a trust prompt and with create_worktree and create_pr to hand.
func (k Kind) Prepare(dir, slug string) error {
	if k == Grok {
		return prepareGrok(dir, slug)
	}
	if k == Codex {
		return prepareCodex(dir, slug)
	}
	return PrepareProject(dir, slug)
}

// HandoffCmd is the shell command a swap puts in a space's agent window: the
// handoff to agent to, which ends by becoming that agent.
func HandoffCmd(slug string, to Kind, token string) string {
	return shellQuote(selfPath()) + " handoff " + shellQuote(slug) + " " + string(to) + " " + shellQuote(token)
}
