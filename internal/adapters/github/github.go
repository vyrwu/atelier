// Package github is the ForgeIntegration adapter for GitHub. It classifies
// a workspace's pull-request state (via `gh pr view --json`) into the
// kernel's ForgeState vocabulary and opens the PR in a browser. It renders
// nothing and owns no window options — the kernel owns the badge slot,
// glyph, color, sort order, caching, and refresh cadence. Swap this adapter
// for a GitLab one by implementing the same integration.ForgeIntegration
// port and selecting it in config.
package github

import (
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/vyrwu/atelier/internal/integration"
)

// Adapter satisfies integration.ForgeIntegration for GitHub.
type Adapter struct{}

// New constructs the GitHub forge adapter.
func New() *Adapter { return &Adapter{} }

// Name identifies the adapter.
func (Adapter) Name() string { return "github" }

// Status classifies the PR for the branch checked out in ws.Cwd. Any
// absence (no PR, gh missing, network failure, unparseable output) maps to
// ForgeNone with no error — the kernel clears the badge. Best-effort by
// design: a badge is cosmetic and must never break the picker.
func (Adapter) Status(ws integration.WorkspaceRef) (integration.ForgeStatus, error) {
	if ws.Cwd == "" {
		return integration.ForgeStatus{State: integration.ForgeNone}, nil
	}
	out, err := ghOutput(ws.Cwd, "pr", "view", "--json", "state,isDraft")
	if err != nil {
		return integration.ForgeStatus{State: integration.ForgeNone}, nil
	}
	var v struct {
		State   string `json:"state"` // OPEN | MERGED | CLOSED
		IsDraft bool   `json:"isDraft"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return integration.ForgeStatus{State: integration.ForgeNone}, nil
	}
	return integration.ForgeStatus{State: classify(v.State, v.IsDraft)}, nil
}

// Open opens the workspace's PR in the browser via `gh pr view --web`.
// Best-effort: no PR → gh exits non-zero, swallowed.
func (Adapter) Open(ws integration.WorkspaceRef) error {
	if ws.Cwd == "" {
		return nil
	}
	return gh(ws.Cwd, "pr", "view", "--web")
}

// classify maps gh's state + draft flag onto the kernel's ForgeState. Pure.
func classify(state string, isDraft bool) integration.ForgeState {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "MERGED":
		return integration.ForgeMerged
	case "CLOSED":
		return integration.ForgeClosed
	default: // OPEN (or anything unexpected → treat as open)
		if isDraft {
			return integration.ForgeDraft
		}
		return integration.ForgeOpen
	}
}

func gh(dir string, args ...string) error {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func ghOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
