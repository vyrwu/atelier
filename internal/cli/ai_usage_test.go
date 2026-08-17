package cli

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

func TestFormatUsageReport_Empty(t *testing.T) {
	got := formatUsageReport(statestore.AIUsage{}, 1000)
	if !strings.Contains(got, "no background AI usage recorded yet") {
		t.Fatalf("empty report: got %q", got)
	}
}

func TestFormatUsageReport_TableAndRate(t *testing.T) {
	u := statestore.AIUsage{
		SinceTS: 0,
		Total:   statestore.AIUsageCounts{Calls: 10, InputTokens: 3_600_000, OutputTokens: 36_000, CostUSD: 0.50},
		ByTask: map[string]statestore.AIUsageCounts{
			"recap":  {Calls: 9, InputTokens: 3_590_000, OutputTokens: 30_000, CostUSD: 0.47},
			"naming": {Calls: 1, InputTokens: 10_000, OutputTokens: 6_000, CostUSD: 0.03},
		},
	}
	// 1 hour elapsed → per-hour rate equals the totals.
	got := formatUsageReport(u, 3600)

	for _, want := range []string{
		"measured over 1h0m",
		"recap", "naming", "total",
		"3.6M",   // input abbreviation
		"36.0K",  // output abbreviation
		"$0.50",  // total cost
		"rate —", // burn-rate line
		"3.6M/h", // input per hour == total (1h window)
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q in:\n%s", want, got)
		}
	}
	// recap must sort before... it's alphabetical: naming < recap.
	if strings.Index(got, "naming") > strings.Index(got, "recap") {
		t.Errorf("expected stable alpha task order (naming before recap):\n%s", got)
	}
}

func TestHumanCount(t *testing.T) {
	cases := map[int64]string{
		0:             "0",
		999:           "999",
		1000:          "1.0K",
		12_345:        "12.3K",
		1_800_000:     "1.8M",
		2_500_000_000: "2.5B",
	}
	for in, want := range cases {
		if got := humanCount(in); got != want {
			t.Errorf("humanCount(%d): got %q want %q", in, got, want)
		}
	}
}

func TestHumanCost(t *testing.T) {
	cases := map[float64]string{
		0:      "$0.00",
		0.004:  "<$0.01",
		0.42:   "$0.42",
		12.999: "$13.00",
	}
	for in, want := range cases {
		if got := humanCost(in); got != want {
			t.Errorf("humanCost(%v): got %q want %q", in, got, want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[int64]string{
		12:               "12s",
		90:               "1m",
		3600 + 12*60:     "1h12m",
		2*86400 + 3*3600: "2d3h",
	}
	for in, want := range cases {
		if got := humanDuration(in); got != want {
			t.Errorf("humanDuration(%d): got %q want %q", in, got, want)
		}
	}
}
