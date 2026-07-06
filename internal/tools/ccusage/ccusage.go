// Package ccusage is atelier's singleton ccusage popup — a live view
// of Claude API token spend over the last 30 days. Backed by a single
// session `_atelier_ccusage` shared across all workspaces (account-
// wide data, not per-repo). The popup runs ccusage in a refresh loop
// so the numbers update without the user re-opening; any keypress
// closes the loop and the popup.
package ccusage

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// Spec backs the popup as a SessionGlobal. The launch command stacks
// three ccusage views from "now" to "career", refreshed every 60s:
//
//   1. `blocks --since <1d>` — last 24h of 5h session blocks. Top row
//      is the ACTIVE block with `% of quota / projected burn`; the
//      previous block(s) sit underneath for "where did the last few
//      hours go" context. 24h cap keeps the section to ~5 rows.
//   2. `weekly --since <3w>` — last 3 weeks of aggregated cost.
//      Answers "am I trending up or down this week."
//   3. `monthly --since <3m>` — last 3 months of aggregated cost.
//      Answers "career spend over time."
//
// Why three reports instead of one daily list: daily was 30 rows of
// undifferentiated noise. The active → weekly → monthly stack answers
// "right now / this week / this month" in increasing zoom-out. Three
// npx calls per refresh; all cheap once npx is warm.
//
// Why `sleep 60` + `trap` instead of `read -t 60 -n 1`: tmux's
// default-shell may be zsh, where `read -n 1` semantics differ from
// bash (zsh treats -n as line-editor max-length, not byte count), so
// the read returned instantly and the loop became a busy-refresh.
// `sleep 60` is POSIX-portable. Ctrl-C triggers the trap and exits
// cleanly; M-q (atelier's popup-table) detaches without killing the
// loop, which is fine — the next M-; → Claude Usage open kills the
// existing session via OpenCommand before re-creating it.
var Spec = &popup.SessionGlobal{
	Tool: "ccusage",
	DefaultCmd: `d1="$(date -v-1d +%Y%m%d 2>/dev/null || date -d '1 day ago' +%Y%m%d)"; ` +
		`wk3="$(date -v-3w +%Y%m%d 2>/dev/null || date -d '3 weeks ago' +%Y%m%d)"; ` +
		`mon3="$(date -v-3m +%Y%m%d 2>/dev/null || date -d '3 months ago' +%Y%m%d)"; ` +
		`trap 'exit 0' INT TERM; ` +
		`printf 'Loading Claude usage (scanning ~/.claude/projects)…\n\n'; ` +
		`while :; do ` +
		`  clear; ` +
		`  printf '\033[1;33m金 Claude usage · blocks (24h) + weekly (3w) + monthly (3m) · refreshes 60s · C-] scroll · Ctrl-C close · M-q detach\033[0m\n\n'; ` +
		`  npx --yes ccusage claude blocks --since "$d1" --order desc 2>&1; ` +
		`  echo; ` +
		`  npx --yes ccusage claude weekly --since "$wk3" --order desc 2>&1; ` +
		`  echo; ` +
		`  npx --yes ccusage claude monthly --since "$mon3" --order desc 2>&1; ` +
		`  printf '\n\033[2m[updated %s · next refresh in 60s]\033[0m\n' "$(date '+%H:%M:%S')"; ` +
		`  sleep 60; ` +
		`done`,
	Description: "Claude usage (blocks 24h + weekly 3w + monthly 3m, auto-refreshes)",
}

// OpenCommand wires `atelier tools ccusage open`. Kills any existing
// session first so each invocation produces fresh numbers — ccusage's
// daily totals shift in real time, and a cached stale popup would
// undermine the tool's whole point.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the ccusage popup (singleton; fresh on every open)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			if has, _ := h.HasSession(Spec.SessionName()); has {
				_ = h.KillSession(Spec.SessionName())
			}
			return Spec.EnsureAndAttach(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
