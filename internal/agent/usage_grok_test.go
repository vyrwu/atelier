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
	if got := renderUsage(Cursor, u, now); strings.Contains(got, "%") {
		t.Fatalf("Cursor inherited another account's quota: %s", got)
	}
}

func TestCLIQuotaCachesAreSeparate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, tc := range []struct {
		kind Kind
		key  string
	}{{Grok, "GROK_HOME"}, {Cursor, "CURSOR_CONFIG_DIR"}} {
		t.Setenv(tc.key, "/first")
		first := usagePath(tc.kind)
		t.Setenv(tc.key, "/second")
		if first == usagePath(tc.kind) || usagePath(tc.kind) == usagePath(Codex) {
			t.Fatalf("shared %s account cache", tc.kind)
		}
	}
}
