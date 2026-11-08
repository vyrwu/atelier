package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Kind is a coding agent a space can run. Provider-specific behavior switches on it.
type Kind string

const (
	Claude      Kind = "claude"
	Codex       Kind = "codex"
	Antigravity Kind = "antigravity"
)

// Of reads a stored or configured agent name. Unknown names and state written
// before there was a choice mean Claude.
func Of(name string) Kind {
	if Kind(name) == Codex || Kind(name) == Antigravity {
		return Kind(name)
	}
	return Claude
}

// Kinds is the order of agents in the picker.
func Kinds() []Kind { return []Kind{Claude, Codex, Antigravity} }

// Next cycles every available provider, including the current one on a swap.
func (k Kind) Next() Kind {
	if k == Claude {
		return Codex
	}
	if k == Codex {
		return Antigravity
	}
	return Claude
}

// Other is the default swap target, preserving Claude/Codex muscle memory.
// The picker cycles all providers with Next.
func (k Kind) Other() Kind {
	if k == Codex || k == Antigravity {
		return Claude
	}
	return Codex
}

// Label is the agent's name as the UI shows it.
func (k Kind) Label() string {
	if k == Antigravity {
		return "Antigravity"
	}
	if k == Codex {
		return "Codex"
	}
	return "Claude Code"
}

// Bin is the CLI that runs the agent.
func (k Kind) Bin() string {
	if k == Antigravity {
		return "agy"
	}
	return string(k)
}

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
	if k == Antigravity {
		return antigravityCmd("", prompt)
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
	if k == Antigravity {
		fresh := antigravityCmd("", prompt)
		if id == "" {
			return fresh
		}
		return antigravityCmd(id, prompt) + " || " + fresh
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
// it, and returns its answer. It runs from a scratch directory with no hooks, no
// MCP servers, no tools, no user config, nothing saved, and without a space's
// identity in its environment — otherwise the run would load a space's guide, become the
// conversation a resume picks up, and rewrite the space's status through the
// hooks. model applies to Claude only; Codex uses its configured default.
func (k Kind) Ask(prompt, input, model string) (string, error) {
	// agy has no documented ephemeral, tool-free mode. Keep deterministic
	// titles rather than starting a second agent with the user's permissions.
	if k == Antigravity {
		return "", fmt.Errorf("antigravity: isolated title generation unavailable")
	}
	dir, err := os.MkdirTemp("", "atelier-ask-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	answer := filepath.Join(dir, "answer.md")
	var cmd *exec.Cmd
	if k == Codex {
		// #nosec G204 G702 -- Runs a fixed agent binary with separate arguments in a private scratch directory.
		cmd = exec.CommandContext(ctx, "codex", "exec", "-C", dir, "--skip-git-repo-check", "--ephemeral",
			"--ignore-user-config", "--ignore-rules", "-s", "read-only", "--disable", "hooks", "-o", answer, prompt)
	} else {
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
	if k == Antigravity {
		return prepareAntigravity(dir, slug)
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
