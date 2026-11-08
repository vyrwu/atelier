package agent

import (
	"strings"
	"testing"
	"time"
)

func TestGrokQuotaFailsClosed(t *testing.T) {
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	for _, raw := range []string{`{}`, `{"config":null}`, `{"config":{"creditUsagePercent":25}}`,
		`{"config":{"creditUsagePercent":101,"currentPeriod":{"end":"2026-11-15T00:00:00Z"}}}`,
		`{"config":{"creditUsagePercent":25,"currentPeriod":{"end":"2026-11-01T00:00:00Z"}}}`,
		`{"config":{"creditUsagePercent":-1,"currentPeriod":{"end":"2026-11-15T00:00:00Z"}}}`,
		`{"config":{"creditUsagePercent":null,"currentPeriod":{"end":"2026-11-15T00:00:00Z"}}}`,
		`{"config":{"creditUsagePercent":0,"currentPeriod":{"end":"2026-11-08T00:00:00Z"}}}`,
		`{"config":{"creditUsagePercent":0,"currentPeriod":{"end":"bad date"}}}`,
		`{"config":{"prepaidBalance":100,"currentPeriod":{"end":"2026-11-15T00:00:00Z"}}}`,
		`{"session":{"inputTokens":1000}}`, `Usage limits are managed by your team.`} {
		if _, err := decodeGrokUsage([]byte(raw), now); err == nil {
			t.Errorf("accepted unsupported quota: %s", raw)
		}
	}
	u, err := decodeGrokUsage([]byte(`{"config":{"creditUsagePercent":25,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-11-15T00:00:00Z"}}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := renderUsage(Grok, u, now); !strings.Contains(got, "7d ") || !strings.Contains(got, "25%") {
		t.Fatal(got)
	}
	u.Buckets[0].Primary.Minutes = 0
	if got := renderUsage(Grok, u, now); strings.Contains(got, "0m") || !strings.Contains(got, "25%") {
		t.Fatalf("unknown duration rendered incorrectly: %s", got)
	}
}

func TestGrokQuotaCachesAreSeparate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GROK_HOME", "/first")
	first := usagePath(Grok)
	t.Setenv("GROK_HOME", "/second")
	if first == usagePath(Grok) || usagePath(Grok) == usagePath(Codex) {
		t.Fatal("shared Grok account cache")
	}
}

// The native 1.0.70 ACP response uses a prefixed period enum and fractional
// second timestamps. Preserve explicit zero usage; omitted usage is unknown.
func TestGrokBillingResponse(t *testing.T) {
	now := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	for _, used := range []string{"0", "1.0", "100"} {
		raw := `{"config":{"creditUsagePercent":` + used + `,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-11-08T14:41:21.964+00:00","end":"2026-11-15T14:41:21.964+00:00"},"prepaidBalance":42,"isUnifiedBillingUser":true},"subscription_tier":"ignored"}`
		u, err := decodeGrokUsage([]byte(raw), now)
		if err != nil {
			t.Fatal(err)
		}
		w := u.Buckets[0].Primary
		if w.Minutes != 10080 || !w.Native || u.Source != "native" || w.Reset != 1794753681 {
			t.Fatalf("window = %+v", w)
		}
		rendered := renderUsage(Grok, u, now)
		want := used
		if want == "1.0" {
			want = "1"
		}
		if !strings.Contains(rendered, want+"%") || strings.Contains(rendered, "~") || strings.Contains(rendered, "42") {
			t.Fatalf("render = %s", rendered)
		}
	}
}
