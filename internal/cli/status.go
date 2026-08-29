package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/state"
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
	c.AddCommand(forgeStatusCmd())
	return c
}

// forgeStatusCmd is the per-window forge (PR) badge emitter. Invoked from
// window-status-current-format with the window's cached @forge_state inlined
// via tmux's #{...} expansion:
//
//	set -ag window-status-current-format "#(atelier status forge '#{@forge_state}')"
//
// Outputs the state's colored glyph, or empty when the window has no forge
// item (ForgeNone) or no forge integration is configured. Sits AFTER the
// attention rollup in the current-window format.
//
// @forge_state is populated/refreshed by the forge-refresh path
// (workspace.SpawnForgeRefresh, fired at each workspace-land event and on
// picker open); this emitter only renders the cached value.
func forgeStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    forgeEmitter + " <forge_state>",
		Short:  "Per-window forge (PR) badge for window-status-current-format",
		Hidden: true,
		// Best-effort: an empty/absent @forge_state must render nothing,
		// never error — this runs on every status redraw.
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			state := ""
			if len(args) > 0 {
				state = args[0]
			}
			if out := formatForgeIcon(state); out != "" {
				fmt.Fprint(cmd.OutOrStdout(), out)
			}
		},
	}
	return c
}

// formatForgeIcon renders the status-line forge (PR) segment from a window's
// cached @forge_state. Pure helper for unit testing.
//
//	Empty        — no forge item (ForgeNone) or forge integration absent
//	 <glyph>     — open (green) / draft (grey) / merged (purple) / closed (red)
//
// Leading space so it doesn't kiss the segment before it. Uses the
// kernel-owned glyph + 256-color spec (integration.ForgeGlyph) so the
// status-line badge matches the picker badge exactly.
func formatForgeIcon(state string) string {
	glyph, color, ok := integration.ForgeGlyph(integration.ForgeState(strings.TrimSpace(state)))
	if !ok {
		return ""
	}
	return fmt.Sprintf(" #[fg=colour%s]%s#[default]", color, glyph)
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
			info, ok := state.ParsePopup(sessionName)
			if !ok {
				return nil
			}
			windowID, ok, err := findWindowIDByDigits(h, info.SidDigit, info.WidDigit)
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
		if state.Digits(fields[0]) == sidDigits && state.Digits(fields[1]) == widDigits {
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
	// Which sessions are workspaces (carry @workspace_id) — read from
	// list-SESSIONS, not per-window: @workspace_id is session-scoped and
	// window-context inheritance is version-fragile (would empty the rollup).
	listable := workspace.ListableSessions(h)
	// session_name LAST so a stray '|' in it can't shift the fixed field.
	out, err := h.Run("list-windows", "-a",
		"-F", "#{?#{==:#{E:"+attentionOption+"},1},1,0}|#{session_name}")
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		attn, session, ok := strings.Cut(line, "|")
		if !ok || attn != "1" {
			continue
		}
		if state.IsPopupSession(session) {
			continue
		}
		// Same inclusion predicate as the M-s picker: a window with attention
		// but whose session isn't a workspace (no @workspace_id) has no picker
		// row to land on, so it must not inflate the rollup into a phantom.
		if !listable[session] {
			continue
		}
		count++
	}
	return count
}
