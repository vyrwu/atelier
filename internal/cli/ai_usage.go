package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/statestore"
)

// aiUsageCmd is `atelier ai usage`: cumulative token accounting for the
// background AI calls (recaps, naming) that quietly burn tokens. It reports
// totals, a per-task breakdown, and the derived per-hour burn rate so the
// real cost of the refresh loop's summaries is measurable, not guessed.
func aiUsageCmd() *cobra.Command {
	var asJSON, reset bool
	c := &cobra.Command{
		Use:   "usage",
		Short: "Show cumulative token usage for background AI calls (recaps, naming)",
		Long: `Report the tokens (input/output/cache) and cost spent by atelier's
background AI calls — the refresh loop's recaps and branch/session naming —
since counting began, with a per-task breakdown and a per-hour burn rate.

Counting is cumulative and persists across restarts. --reset restarts the
measurement window (handy for "how much does the next hour cost?").`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if reset {
				if err := statestore.ResetAIUsage(time.Now().Unix()); err != nil {
					return err
				}
				fmt.Fprintln(out, "ai usage counters reset")
				return nil
			}
			s, err := statestore.Load()
			if err != nil {
				return err
			}
			var u statestore.AIUsage
			if s != nil && s.AIUsage != nil {
				u = *s.AIUsage
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(u)
			}
			fmt.Fprint(out, formatUsageReport(u, time.Now().Unix()))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	c.Flags().BoolVar(&reset, "reset", false, "reset counters and start a fresh measurement window")
	return c
}

// formatUsageReport renders the AI usage table. Pure: takes `now` rather than
// reading the clock so it's deterministic under test.
func formatUsageReport(u statestore.AIUsage, now int64) string {
	if u.Total.Calls == 0 {
		return "no background AI usage recorded yet\n"
	}
	elapsed := now - u.SinceTS
	var b strings.Builder
	if elapsed > 0 {
		fmt.Fprintf(&b, "AI background token usage — measured over %s\n\n", humanDuration(elapsed))
	} else {
		fmt.Fprintf(&b, "AI background token usage\n\n")
	}

	const rowFmt = "  %-9s %6s  %8s  %8s  %8s  %8s  %8s\n"
	fmt.Fprintf(&b, rowFmt, "task", "calls", "input", "output", "cache-rd", "cache-wr", "cost")
	for _, task := range sortedTasks(u.ByTask) {
		writeUsageRow(&b, rowFmt, task, u.ByTask[task])
	}
	fmt.Fprintf(&b, "  %s\n", strings.Repeat("─", 63))
	writeUsageRow(&b, rowFmt, "total", u.Total)

	if elapsed > 0 {
		hours := float64(elapsed) / 3600
		fmt.Fprintf(&b, "\n  rate — input %s/h · output %s/h · %s/h\n",
			humanCount(int64(float64(u.Total.InputTokens)/hours)),
			humanCount(int64(float64(u.Total.OutputTokens)/hours)),
			humanCost(u.Total.CostUSD/hours))
	}
	return b.String()
}

func writeUsageRow(b *strings.Builder, format, label string, c statestore.AIUsageCounts) {
	fmt.Fprintf(b, format, label,
		strconv.FormatInt(c.Calls, 10),
		humanCount(c.InputTokens), humanCount(c.OutputTokens),
		humanCount(c.CacheReadTokens), humanCount(c.CacheCreationTokens),
		humanCost(c.CostUSD))
}

// sortedTasks returns the task keys in a stable order for deterministic output.
func sortedTasks(m map[string]statestore.AIUsageCounts) []string {
	tasks := make([]string, 0, len(m))
	for k := range m {
		tasks = append(tasks, k)
	}
	sort.Strings(tasks)
	return tasks
}

// humanCount abbreviates a token count (12345 → "12.3K", 1_800_000 → "1.8M").
func humanCount(n int64) string {
	f := float64(n)
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", f/1e3)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", f/1e6)
	default:
		return fmt.Sprintf("%.1fB", f/1e9)
	}
}

// humanCost formats a USD amount with cent precision, sub-cent as <$0.01.
func humanCost(usd float64) string {
	if usd > 0 && usd < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", usd)
}

// humanDuration renders a seconds count as a compact "2d3h" / "3h12m" / "45m"
// / "12s" — the two most-significant units only.
func humanDuration(sec int64) string {
	if sec < 60 {
		return strconv.FormatInt(sec, 10) + "s"
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd%dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}
