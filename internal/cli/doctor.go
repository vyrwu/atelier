package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/plugin"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// CheckStatus is doctor's per-check result classification.
//
// PASS: nothing to do.
// WARN: surface-level drift, the user can keep going but should know.
// FAIL: actively broken — exit non-zero so scripts can gate on it.
// SKIP: not applicable in current environment (e.g. tmux not running).
type CheckStatus int

const (
	StatusPass CheckStatus = iota
	StatusWarn
	StatusFail
	StatusSkip
)

func (s CheckStatus) String() string {
	switch s {
	case StatusPass:
		return "PASS"
	case StatusWarn:
		return "WARN"
	case StatusFail:
		return "FAIL"
	case StatusSkip:
		return "SKIP"
	}
	return "?"
}

// CheckResult is what each diagnostic check returns. Name is the
// short label printed at the start of the line; Detail is the
// outcome explanation; Remediation, when non-empty, is printed
// indented on the next line so the user has the fix in front of them.
type CheckResult struct {
	Name        string
	Status      CheckStatus
	Detail      string
	Remediation string
}

func DoctorCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Check tmux, atelier itself, and every discovered tool's requirements",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			h := tmuxhost.New(socket)

			// Group 1: tmux + atelier-self core checks.
			results := []CheckResult{
				checkTmuxVersion(h),
				checkAtelierBinaries(),
				checkEscapeTime(h),
				checkStatuslineFormat(h),
			}

			// Group 2: persistence-layer + cache state.
			results = append(results,
				checkStatestoreParseable(),
				checkClaudeSettings(),
				checkWorktreeDirsExist(),
			)

			// Side-effect sweep: pop-up orphans get GC'd silently.
			// No doctor entry — the existing window-unlinked /
			// session-closed hooks (plus this sweep on every atelier
			// launch) are the actionable mechanism. Surfacing a
			// "you have N popup orphans" line gave the user a chore
			// instead of a fix; the fix is to just GC them.
			_ = hostpopup.CleanupOrphanedPopups(h)

			fail := false
			for _, r := range results {
				printCheck(out, r)
				if r.Status == StatusFail {
					fail = true
				}
			}

			// Plugin discovery (preserved from prior doctor).
			fmt.Fprintln(out)
			res, err := plugin.Discover()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Discovered tools (%d):\n", len(res.Plugins))
			for _, p := range res.Plugins {
				fmt.Fprintf(out, "  %-20s %s\n", p.Name, p.Manifest.Description)
				for _, req := range p.Manifest.Requires {
					status := "ok"
					if _, err := exec.LookPath(req); err != nil {
						status = "MISSING"
					}
					fmt.Fprintf(out, "    requires: %-20s %s\n", req, status)
				}
			}
			if len(res.Skipped) > 0 {
				fmt.Fprintln(out)
				fmt.Fprintf(out, "Skipped tools (%d):\n", len(res.Skipped))
				for path, perr := range res.Skipped {
					fmt.Fprintf(out, "  %s\n    error: %v\n", path, perr)
				}
			}

			if fail {
				return errors.New("doctor: one or more checks failed")
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "atelier",
		"tmux socket to probe (-L); use empty for the user's default")
	return c
}

func printCheck(w io.Writer, r CheckResult) {
	fmt.Fprintf(w, "[%s] %-26s %s\n", r.Status, r.Name, r.Detail)
	if r.Remediation != "" {
		fmt.Fprintf(w, "       fix: %s\n", r.Remediation)
	}
}

// checkTmuxVersion preserves the original "is tmux present, what
// version" probe. Failure here is terminal — every other check
// depends on a working tmux binary.
func checkTmuxVersion(h *tmuxhost.Client) CheckResult {
	v, err := h.Version()
	if err != nil {
		return CheckResult{Name: "tmux version", Status: StatusFail,
			Detail:      fmt.Sprintf("tmux invocation failed: %v", err),
			Remediation: "install tmux 3.4+ (Homebrew: `brew install tmux`)"}
	}
	return CheckResult{Name: "tmux version", Status: StatusPass,
		Detail: v}
}

// checkAtelierBinaries verifies the atelier-* tool binaries are on
// PATH. Missing tool binaries silently strip features from the M-;
// picker — better to surface the gap at doctor-time than to leave
// the user puzzled why their tool isn't listed.
func checkAtelierBinaries() CheckResult {
	required := []string{
		"atelier-workspaces",
		"atelier-toolselector",
		"atelier-popupshell",
		"atelier-claude",
		"atelier-k8s",
		"atelier-aws",
		"atelier-pg",
		"atelier-lazygit",
	}
	var missing []string
	for _, name := range required {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return CheckResult{Name: "atelier-* on PATH", Status: StatusPass,
			Detail: fmt.Sprintf("all %d tool binaries reachable", len(required))}
	}
	return CheckResult{Name: "atelier-* on PATH", Status: StatusWarn,
		Detail:      fmt.Sprintf("missing: %s", strings.Join(missing, ", ")),
		Remediation: "run `make install` or ensure ~/.local/bin (or your install prefix) is on PATH"}
}

// checkEscapeTime catches the "Esc has a noticeable delay" trap. tmux
// defaults `escape-time` to 500ms; atelier's UX assumes <=50ms so that
// M-q / M-s / M-; feel instant. Anything higher is a major UX regression
// even though it doesn't break correctness.
func checkEscapeTime(h *tmuxhost.Client) CheckResult {
	v, err := h.Run("show-option", "-gv", "escape-time")
	if err != nil {
		return CheckResult{Name: "tmux escape-time", Status: StatusSkip,
			Detail: "no tmux server running on this socket"}
	}
	raw := strings.TrimSpace(string(v))
	ms, err := strconv.Atoi(raw)
	if err != nil {
		return CheckResult{Name: "tmux escape-time", Status: StatusWarn,
			Detail: fmt.Sprintf("unparseable value %q", raw)}
	}
	if ms > 50 {
		return CheckResult{Name: "tmux escape-time", Status: StatusWarn,
			Detail:      fmt.Sprintf("%dms (recommend ≤50ms)", ms),
			Remediation: "add `set -g escape-time 10` to your tmux.conf"}
	}
	return CheckResult{Name: "tmux escape-time", Status: StatusPass,
		Detail: fmt.Sprintf("%dms", ms)}
}

// checkStatuslineFormat verifies atelier's freshness + attention
// segments survive in `window-status-format`. The bundled launcher
// stamps these on every startup, but a host-config statusline (in
// plugin mode) can overwrite them. This catches the silent breakage
// where the icons just stop rendering.
func checkStatuslineFormat(h *tmuxhost.Client) CheckResult {
	v, err := h.Run("show-options", "-gv", "window-status-format")
	if err != nil {
		return CheckResult{Name: "statusline segments", Status: StatusSkip,
			Detail: "no tmux server running on this socket"}
	}
	out := string(v)
	hasFresh := strings.Contains(out, "atelier status freshness")
	hasAttn := strings.Contains(out, "atelier status attention")
	hasW := strings.Contains(out, "#W")
	// FR-2.4: window-status-format containing atelier's freshness but no
	// #W produces a bare floating icon per inactive window (the "phantom
	// second checkmark" bug). Catch this regardless of other segments.
	if hasFresh && !hasW {
		return CheckResult{Name: "statusline segments", Status: StatusFail,
			Detail:      "window-status-format has freshness segment but no #W (bare icon, no window name)",
			Remediation: "set window-status-format to include `#W` (e.g. \" #W \") and re-source so stamp-statusline can inject at the anchor"}
	}
	switch {
	case hasFresh && hasAttn:
		return CheckResult{Name: "statusline segments", Status: StatusPass,
			Detail: "freshness + attention segments present"}
	case !hasFresh && !hasAttn:
		return CheckResult{Name: "statusline segments", Status: StatusFail,
			Detail:      "neither freshness nor attention segment present",
			Remediation: "in bundled mode this auto-injects on startup; in plugin mode add `run-shell 'atelier internal stamp-statusline'` to your tmux.conf"}
	default:
		missing := "freshness"
		if hasFresh {
			missing = "attention"
		}
		return CheckResult{Name: "statusline segments", Status: StatusWarn,
			Detail:      fmt.Sprintf("missing: %s segment", missing),
			Remediation: "re-source atelier init so stamp-statusline re-injects (run-shell 'atelier internal stamp-statusline')"}
	}
}

// checkStatestoreParseable surfaces a corrupt cache before the user
// hits "atelier launches but shows zero workspaces" silent failure.
// statestore.Load returns (nil, nil) for missing — that's PASS, not
// a problem.
func checkStatestoreParseable() CheckResult {
	state, err := statestore.Load()
	if err != nil {
		return CheckResult{Name: "statestore cache", Status: StatusFail,
			Detail:      fmt.Sprintf("unparseable: %v", err),
			Remediation: "back up the file, then `atelier state reset` (or delete $XDG_CACHE_HOME/atelier/state-*.json)"}
	}
	if state == nil {
		return CheckResult{Name: "statestore cache", Status: StatusPass,
			Detail: "no cache yet (first run)"}
	}
	return CheckResult{Name: "statestore cache", Status: StatusPass,
		Detail: fmt.Sprintf("%d workspaces, last_active=%q",
			len(state.Workspaces), state.LastActiveSession)}
}

// checkClaudeSettings ensures ~/.cache/atelier/claude/settings.json
// exists. The Claude tool reads it at startup; absence breaks the
// flow with a cryptic error. Auto-create on absence per FR-1.2.
func checkClaudeSettings() CheckResult {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	path := filepath.Join(cacheDir, "atelier", "claude", "settings.json")
	if _, err := os.Stat(path); err == nil {
		return CheckResult{Name: "claude settings", Status: StatusPass,
			Detail: path}
	}
	// Auto-create with an empty JSON object — Claude tool tolerates
	// missing keys but errors on truly invalid JSON.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return CheckResult{Name: "claude settings", Status: StatusWarn,
			Detail:      fmt.Sprintf("cannot create dir: %v", err),
			Remediation: "ensure $XDG_CACHE_HOME (or ~/.cache) is writable"}
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		return CheckResult{Name: "claude settings", Status: StatusWarn,
			Detail:      fmt.Sprintf("cannot write: %v", err),
			Remediation: "ensure $XDG_CACHE_HOME (or ~/.cache) is writable"}
	}
	return CheckResult{Name: "claude settings", Status: StatusWarn,
		Detail: fmt.Sprintf("created empty stub at %s", path)}
}

// checkWorktreeDirsExist scans the statestore cache for workspaces
// whose recorded cwd is gone from disk (user `git worktree remove`d
// it without atelier mediation, or the disk was wiped). These will
// be skipped on restore but pollute the cache forever otherwise.
func checkWorktreeDirsExist() CheckResult {
	state, err := statestore.Load()
	if err != nil || state == nil {
		return CheckResult{Name: "cached worktree dirs", Status: StatusSkip,
			Detail: "no cache to scan"}
	}
	var ghosts []string
	for _, ws := range state.Workspaces {
		for _, w := range ws.Windows {
			if w.Cwd == "" {
				continue
			}
			if _, err := os.Stat(w.Cwd); os.IsNotExist(err) {
				ghosts = append(ghosts, ws.SessionName+":"+w.Name+" → "+w.Cwd)
			}
		}
	}
	if len(ghosts) == 0 {
		return CheckResult{Name: "cached worktree dirs", Status: StatusPass,
			Detail: fmt.Sprintf("%d workspaces, all cwds exist", len(state.Workspaces))}
	}
	return CheckResult{Name: "cached worktree dirs", Status: StatusWarn,
		Detail:      fmt.Sprintf("%d orphaned: %s", len(ghosts), strings.Join(ghosts, "; ")),
		Remediation: "run `atelier state sync` to drop entries for vanished worktrees"}
}

