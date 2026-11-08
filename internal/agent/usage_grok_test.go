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
		`{"session":{"inputTokens":1000}}`, `Usage limits are managed by your team.`} {
		if _, err := decodeGrokUsage([]byte(raw), now); err == nil {
			t.Errorf("accepted unsupported quota: %s", raw)
		}
	}
	u, err := decodeGrokUsage([]byte(`{"config":{"creditUsagePercent":25,"currentPeriod":{"type":"WEEKLY","end":"2026-11-15T00:00:00Z"}}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := renderUsage(Grok, u, now); !strings.Contains(got, "7d ") || !strings.Contains(got, "25%") {
		t.Fatal(got)
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
