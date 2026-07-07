package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

const attentionOption = "@needs_attention"

func StatusCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Status-line data emitters + hook entry points",
	}
	c.AddCommand(attentionStatusCmd())
	c.AddCommand(freshnessStatusCmd())
	return c
}

// freshnessStatusCmd is the per-window sync-state emitter (FR-7).
// Invoked from window-status-format with the window's pull-state options
// (and the session's @repo_path) inlined via tmux's #{...} expansion:
//
//	set -ag window-status-current-format \
//	  "#(atelier status freshness '#{@workspace_behind}' '#{@workspace_ahead}' '#{@workspace_pull_error}' '#{@workspace_freshness_ts}' '#{@repo_path}')"
//
// Args (positional):
//  1. behind        — count of commits in origin/<default> not in branch
//  2. ahead         — count of commits in branch not in origin/<default>
//  3. pull_error    — short error message from the last failed _bg-pull
//  4. freshness_ts  — unix epoch of last successful fetch
//  5. repo_path     — session's @repo_path (empty for non-git sessions)
//
// Outputs the icon + counts wrapped in tmux color codes, or empty
// string when the workspace isn't a git repo or no pull has run yet.
func freshnessStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    freshnessEmitter + " <behind> <ahead> <pull_error> <freshness_ts> <repo_path>",
		Short:  "Per-window freshness icon for window-status-format (FR-7)",
		Hidden: true,
		Args:   cobra.ExactArgs(5),
		Run: func(cmd *cobra.Command, args []string) {
			out := formatFreshnessIcon(args[0], args[1], args[2], args[3], args[4])
			// Tmux invokes this on every status redraw — 20+ times/min
			// per window. Two logging modes:
			//
			//   1. ATELIER_STATUSLINE_TRACE=1: log every call (noisy,
			//      for active debugging only).
			//   2. Default: log only the "expected to render but
			//      couldn't" case — repo is set but freshness_ts empty
			//      and no pull-error. That's the silent-failure case
			//      (bg-pull never ran for this window) which is
			//      otherwise invisible to the user.
			repoSet := strings.TrimSpace(args[4]) != ""
			tsEmpty := strings.TrimSpace(args[3]) == ""
			errEmpty := strings.TrimSpace(args[2]) == ""
			silentMiss := repoSet && tsEmpty && errEmpty
			if os.Getenv("ATELIER_STATUSLINE_TRACE") != "" || silentMiss {
				debuglog.Logf("status.freshness: behind=%q ahead=%q err=%q ts=%q repo=%q → %q",
					args[0], args[1], args[2], args[3], args[4], out)
			}
			if out != "" {
				fmt.Fprint(cmd.OutOrStdout(), out)
			}
		},
	}
	return c
}

// formatFreshnessIcon renders the status-line freshness segment.
// Pure helper for unit testing.
//
//	Empty       — non-git session OR pull never ran (freshnessTs="")
//	✔           — in-sync (green, U+2714 heavy check) — uses stock tmux 'green' which
//	               picks up whatever shade the user's theme defines
//	↓N          — behind by N (red) — needs `git pull`
//	↑N          — ahead by N (yellow) — needs `git push`
//	↓N↑M        — diverged (red, worst case)
//	⚠ <msg>     — pull error (red) with short message ("fetch failed",
//	               "rebase failed", etc.) so the user can tell error
//	               classes apart without opening the debug log
//
// Per the user's spec — between window name and the global attention
// rollup. Padded with a leading space so it doesn't kiss the window
// name. Uses stock tmux color names (green, red, yellow) so the user's
// theme palette resolves them naturally.
func formatFreshnessIcon(behind, ahead, pullError, freshnessTs, repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	if msg := strings.TrimSpace(pullError); msg != "" {
		return fmt.Sprintf(" #[fg=red]⚠ %s#[default]", truncateErr(msg))
	}
	if strings.TrimSpace(freshnessTs) == "" {
		// Pull pending — show nothing rather than a noisy "…". The
		// pull only takes a second or two; the icon will appear on
		// the next status redraw (status-interval = 3s).
		return ""
	}
	b := atoi(behind)
	a := atoi(ahead)
	switch {
	case b == 0 && a == 0:
		return " #[fg=green]✔#[default]"
	case b > 0 && a == 0:
		return fmt.Sprintf(" #[fg=red]↓%d#[default]", b)
	case b == 0 && a > 0:
		return fmt.Sprintf(" #[fg=yellow]↑%d#[default]", a)
	default:
		return fmt.Sprintf(" #[fg=red]↓%d↑%d#[default]", b, a)
	}
}

// truncateErr clamps a `_bg-pull` error message to a length that's
// reasonable for an inline status-bar segment. The bg-pull subcommand
// stamps short canonical messages today ("fetch failed", "rebase
// failed"), but if a future change lets raw git stderr through we
// don't want it blowing out the status line across every window.
const freshnessErrMax = 30

func truncateErr(s string) string {
	r := []rune(s)
	if len(r) <= freshnessErrMax {
		return s
	}
	return string(r[:freshnessErrMax-1]) + "…"
}

func atoi(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func attentionStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   attentionEmitter,
		Short: "Attention rollup + clear-on-popup hook",
	}
	c.AddCommand(attentionCountCmd())
	c.AddCommand(attentionClearPopupCmd())
	return c
}

// attentionCountCmd is the rollup emitter — sums @needs_attention=1
// across every tmux window and renders " ⏺ <n>" in yellow when
// non-zero. Designed to be embedded in any user's window-status
// format via `#(atelier status attention count)`. Public API.
//
// The previous subcommand name `--count` (with leading dashes)
// looked like a flag and was unreachable through cobra's parser —
// `atelier status attention --count` errored with "unknown flag",
// which tmux's `#(...)` quietly swallowed (stderr discarded). The
// attention rollup silently returned empty for every user. Renamed
// to `count`; init generator updated to match.
func attentionCountCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "count",
		Short: "Print attention rollup (e.g. \" ⏺ 3\") for the status line",
		Run: func(cmd *cobra.Command, _ []string) {
			h := tmuxhost.New(socket)
			if n := countAttentionWindows(h); n > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), " #[fg=yellow]⏺ %d#[default]", n)
			}
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// attentionClearPopupCmd wires into `client-session-changed`. When the
// client switches into an atelier popup session (name `_atelier_<tool>_<sid>_<wid>`),
// we clear @needs_attention on the parent window so it stops blinking now
// that the user has obviously seen it.
func attentionClearPopupCmd() *cobra.Command {
	var (
		socket      string
		sessionName string
	)
	c := &cobra.Command{
		Use:   "clear-popup",
		Short: "Clear @needs_attention on parent window when client switches into a popup",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			// Use the explicit --session flag when given (tests); otherwise
			// fall back to the current session (production hook).
			if sessionName == "" {
				out, err := h.DisplayMessage("#{session_name}")
				if err != nil {
					return nil // best-effort hook; never fail
				}
				sessionName = out
			}
			sid, wid, ok := parsePopupParent(sessionName)
			if !ok {
				return nil
			}
			windowID, ok, err := findWindowIDByDigits(h, sid, wid)
			if err != nil || !ok {
				return nil
			}
			return workspace.SetAttention(h, windowID, false)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().StringVar(&sessionName, "session", "", "session name to evaluate (default: current session)")
	return c
}

// parsePopupParent extracts parent (sid, wid) from a popup session name.
// Recognizes both atelier (`_atelier_<tool>_<sid>_<wid>`) and bash naming
// (`_popup_`, `_claudepop_`, `_k8spop_`, `_awspop_`, `_lazygitpop_`).
// Returns ok=false for session-global popups or non-popup sessions.
func parsePopupParent(sessionName string) (sid, wid string, ok bool) {
	if strings.HasPrefix(sessionName, "_atelier_") {
		rest := strings.TrimPrefix(sessionName, "_atelier_")
		parts := strings.Split(rest, "_")
		if len(parts) < 3 {
			return "", "", false
		}
		return parts[1], parts[2], true
	}
	for _, p := range []string{"_popup_", "_claudepop_", "_k8spop_", "_awspop_", "_lazygitpop_"} {
		if !strings.HasPrefix(sessionName, p) {
			continue
		}
		rest := strings.TrimPrefix(sessionName, p)
		parts := strings.SplitN(rest, "_", 3)
		if len(parts) < 2 {
			return "", "", false
		}
		return parts[0], parts[1], true
	}
	return "", "", false
}

// findWindowIDByDigits returns the tmux window ID whose session_id /
// window_id digits match the given strings.
func findWindowIDByDigits(h *tmuxhost.Client, sidDigits, widDigits string) (string, bool, error) {
	windows, err := h.ListWindows()
	if err != nil {
		return "", false, err
	}
	for _, line := range windows {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if digitsOf(fields[0]) == sidDigits && digitsOf(fields[1]) == widDigits {
			return fields[1], true, nil
		}
	}
	return "", false, nil
}

// countAttentionWindows counts the windows currently flagged with
// `@needs_attention` for the status-line rollup.
//
// Popup-backing sessions (`_atelier_*`, `_claudepop_*`, `_popup_*`,
// `_k8spop_*`, `_awspop_*`, `_lazygitpop_*`) are explicitly excluded:
// any attention flag stamped on those windows is noise, not a real
// workspace event. This matters because legacy bash hooks (e.g. the
// pre-atelier `tmux_notify_attention` script) misroute the flag to the
// popup window instead of the parent workspace — so without this filter
// a single Claude Stop hook can inflate the rollup to 2.
func countAttentionWindows(h *tmuxhost.Client) int {
	out, err := h.Run("list-windows", "-a",
		"-F", "#{session_name}|#{?#{==:#{E:"+attentionOption+"},1},1,0}")
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		i := strings.IndexByte(line, '|')
		if i < 0 {
			continue
		}
		if line[i+1:] != "1" {
			continue
		}
		if isPopupSession(line[:i]) {
			continue
		}
		count++
	}
	return count
}

func isPopupSession(name string) bool {
	for _, p := range []string{"_atelier_", "_claudepop_", "_popup_", "_k8spop_", "_awspop_", "_lazygitpop_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func digitsOf(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}
