package workspaces

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// BgPullCommand is the hidden background-pull entrypoint that FR-7's
// async pull spawns detached after a workspace switch. Replaces the
// blocking spinner-wrapped pullDefault calls — the user is already on
// the workspace when this starts; it fetches + rebases + measures
// behind/ahead + stamps tmux window options, all in the background.
//
// All output goes to atelier's debug log (no terminal noise). On any
// failure, @workspace_behind / @workspace_ahead are cleared and
// @workspace_pull_error is set to a short message; the status-line
// freshness segment renders the error as a red `⚠` so the user knows
// something went wrong without atelier having to surface it intrusively.
//
// Signature: `atelier tools workspaces _bg-pull <repoPath> <defaultBranch> <windowID>`
func BgPullCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_bg-pull <repoPath> <defaultBranch> <windowID>",
		Short:  "internal: background fetch/rebase + freshness stamping (FR-7)",
		Hidden: true,
		Args:   cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			repoPath := args[0]
			defaultBranch := args[1]
			windowID := args[2]
			h := tmuxhost.New(socket)
			return runBgPull(h, repoPath, defaultBranch, windowID)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// runBgPull is the testable core. Takes the resolved client + paths
// + target window. Stamps freshness options on success or pull-error
// on failure; never returns to a user-visible surface.
func runBgPull(h *tmuxhost.Client, repoPath, defaultBranch, windowID string) error {
	if repoPath == "" || defaultBranch == "" || windowID == "" {
		return fmt.Errorf("_bg-pull: repoPath, defaultBranch, windowID all required")
	}
	debuglog.Logf("_bg-pull: starting repo=%s branch=%s window=%s", repoPath, defaultBranch, windowID)

	// Step 1: fetch origin <defaultBranch>. Network op; the slow bit.
	if err := runGit(repoPath, "fetch", "origin", defaultBranch); err != nil {
		debuglog.LogErr("_bg-pull fetch", err)
		stampPullError(h, windowID, "fetch failed")
		return err
	}

	// Step 2: figure out which path/branch the WINDOW is actually on
	// (windows in a worktree session point at a worktree path, NOT the
	// bare repo). pull --rebase only applies to the default-branch
	// window in the bare repo — worktree windows just get measured.
	winPath, _ := h.DisplayMessageAt(windowID, "#{pane_current_path}")
	winPath = strings.TrimSpace(winPath)
	winBranch := ""
	if winPath != "" {
		winBranch = runGitQuiet(winPath, "rev-parse", "--abbrev-ref", "HEAD")
	}
	if winBranch == defaultBranch && samePath(winPath, repoPath) {
		debuglog.Logf("_bg-pull: window is default-branch in bare repo, running pull --rebase")
		if err := runGit(repoPath, "pull", "--rebase"); err != nil {
			debuglog.LogErr("_bg-pull pull --rebase", err)
			stampPullError(h, windowID, "rebase failed")
			return err
		}
	}

	// Step 3: measure ahead/behind for the WINDOW's branch.
	measureBranch := winBranch
	measureDir := winPath
	if measureBranch == "" || measureDir == "" {
		measureBranch = defaultBranch
		measureDir = repoPath
	}
	behind := runGitQuiet(measureDir, "rev-list", "--count",
		measureBranch+".."+"origin/"+defaultBranch)
	ahead := runGitQuiet(measureDir, "rev-list", "--count",
		"origin/"+defaultBranch+".."+measureBranch)
	if behind == "" {
		behind = "0"
	}
	if ahead == "" {
		ahead = "0"
	}

	// Step 4: stamp the freshness options on the window. Clear any
	// prior pull-error since this run succeeded.
	stampFreshness(h, windowID, behind, ahead)
	debuglog.Logf("_bg-pull: done window=%s behind=%s ahead=%s", windowID, behind, ahead)
	return nil
}

// stampFreshness writes the success-path freshness options. Best-effort.
func stampFreshness(h *tmuxhost.Client, windowID, behind, ahead string) {
	_ = h.SetWindowOption(windowID, workspace.OptWorkspaceBehind, behind)
	_ = h.SetWindowOption(windowID, workspace.OptWorkspaceAhead, ahead)
	_ = h.SetWindowOption(windowID, workspace.OptWorkspaceFreshnessTs,
		strconv.FormatInt(time.Now().Unix(), 10))
	_ = h.UnsetWindowOption(windowID, workspace.OptWorkspacePullError)
}

// stampPullError records a failure. Clears behind/ahead so a prior
// success's numbers don't show alongside the error icon.
func stampPullError(h *tmuxhost.Client, windowID, msg string) {
	_ = h.UnsetWindowOption(windowID, workspace.OptWorkspaceBehind)
	_ = h.UnsetWindowOption(windowID, workspace.OptWorkspaceAhead)
	_ = h.SetWindowOption(windowID, workspace.OptWorkspacePullError, msg)
}

// samePath checks two filesystem paths point to the same dir,
// independent of trailing slashes / symlinks at the leaf level.
// Used to detect "window is in the bare repo, not a worktree."
func samePath(a, b string) bool {
	a = strings.TrimRight(a, "/")
	b = strings.TrimRight(b, "/")
	return a != "" && a == b
}

// spawnBgPull (legacy local helper) — removed. All callers now use
// workspace.SpawnBgPull which lives in the workspace primitive and
// (a) sets Setpgid so the detached child survives the parent popup
// closing, (b) logs the pre-Release Pid (Go 1.25+ resets it to -1
// after Release).
