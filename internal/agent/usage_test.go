package agent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureClaudeUsage(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	now := time.Now()
	data := []byte(`{"rate_limits":{"five_hour":{"used_percentage":0,"resets_at":2000000000},"seven_day":{"used_percentage":92.5,"resets_at":2000000001}}}`)
	if err := CaptureClaudeUsage(data, now); err != nil {
		t.Fatal(err)
	}
	u := LoadUsage(Claude)
	if len(u.Buckets) != 1 || *u.Buckets[0].Primary.Used != 0 || *u.Buckets[0].Secondary.Used != 92.5 {
		t.Fatalf("captured usage = %+v", u)
	}
	// A new session before its first response must not erase account usage
	// already reported by another session.
	if err := CaptureClaudeUsage([]byte(`{}`), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := LoadUsage(Claude); !got.Observed.Equal(now) {
		t.Errorf("empty session overwrote the observation: %+v", got)
	}
	fi, _ := os.Stat(usagePath(Claude))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("usage cache mode = %o", fi.Mode().Perm())
	}
}

func TestClaudePartialCaptureKeepsBothWindows(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	now := time.Now()
	first := []byte(`{"rate_limits":{"five_hour":{"used_percentage":25,"resets_at":2000000000},"seven_day":{"used_percentage":85,"resets_at":2000000001}}}`)
	partial := []byte(`{"rate_limits":{"seven_day":{"used_percentage":90,"resets_at":2000000001}}}`)
	if err := CaptureClaudeUsage(first, now); err != nil {
		t.Fatal(err)
	}
	if err := CaptureClaudeUsage(partial, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	u := LoadUsage(Claude)
	if u.Buckets[0].Primary == nil || *u.Buckets[0].Primary.Used != 25 || *u.Buckets[0].Secondary.Used != 90 {
		t.Fatalf("partial response erased another window: %+v", u)
	}
	// A stale session must not overwrite the account command's newer reading,
	// even if native refresh is temporarily failing.
	u.Source = "native"
	full := 100.0
	u.Buckets[0].Secondary.Used = &full
	if err := saveUsage(Claude, u); err != nil {
		t.Fatal(err)
	}
	if err := CaptureClaudeUsage(partial, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := LoadUsage(Claude); *got.Buckets[0].Secondary.Used != 100 || got.Source != "native" {
		t.Error("stale session headers replaced the native account reading")
	}
}

func TestClaudeMissingWindowIsVisible(t *testing.T) {
	used := 85.0
	u := Usage{Observed: time.Now(), Buckets: []UsageBucket{{Secondary: &UsageWindow{Used: &used, Minutes: 10080, Reset: 2000000000}}}}
	got := renderUsage(Claude, u, time.Now())
	if !strings.Contains(got, "5h —") || !strings.Contains(got, "7d ") {
		t.Errorf("missing window disappeared: %s", got)
	}
}

func TestDecodeCodexUsage(t *testing.T) {
	now := time.Now()
	for _, data := range []string{
		`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":15,"windowDurationMins":300,"resetsAt":2000000000}}}`,
		`{"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":15,"windowDurationMins":300,"resetsAt":2000000000}}}}`,
	} {
		u, err := decodeCodexUsage([]byte(data), now)
		if err != nil || len(u.Buckets) != 1 || u.Buckets[0].ID != "codex" || *u.Buckets[0].Primary.Used != 15 {
			t.Errorf("decoded %s: %+v, %v", data, u, err)
		}
	}
	u, err := decodeCodexUsage([]byte(`{"rateLimitsByLimitId":{"z":{"limitName":"extra"},"codex":{"primary":{"usedPercent":0}}}}`), now)
	if err != nil || len(u.Buckets) != 2 || u.Buckets[0].ID != "codex" || u.Buckets[1].ID != "z" {
		t.Errorf("multiple buckets = %+v, %v", u, err)
	}
}

func TestUsageStatusColorsAndExpiry(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		spent float64
		want  string
	}{
		{0, "#[fg=colour71]100%"},
		{74, "#[fg=colour71]26%"},
		{75, "#[fg=colour179]25%"},
		{89, "#[fg=colour179]11%"},
		{90, "#[fg=colour167]10%"},
		{99.7, "#[fg=colour167]0%"},
		{100, "#[fg=colour167]0%"},
	} {
		w := &UsageWindow{Used: &tc.spent, Minutes: 300, Reset: now.Add(time.Hour).Unix()}
		u := Usage{Observed: now, Buckets: []UsageBucket{{Primary: w}}}
		got := UsageStatus(u, u, now)
		if !strings.Contains(got, tc.want) || !strings.Contains(got, "#[fg=colour209]\uec82") || !strings.Contains(got, "#[fg=colour39]\uec81") {
			t.Errorf("spent %v: %q", tc.spent, got)
		}
		// Expiry is unknown, not an invented replenished allowance.
		w.Reset = now.Unix()
		if expired := renderUsage(Codex, u, now); strings.Contains(expired, "%") || !strings.Contains(expired, "5h —") {
			t.Errorf("expired quota = %q", expired)
		}
	}
	unknown := UsageStatus(Usage{}, Usage{}, now)
	if strings.Contains(unknown, "%") || !strings.Contains(unknown, " —") {
		t.Errorf("unknown usage = %q", unknown)
	}
}

func TestUsageStatusFreshnessAndLabels(t *testing.T) {
	now := time.Now()
	u := Usage{Observed: now, Buckets: []UsageBucket{{ID: "codex"}}}
	if strings.Contains(renderUsage(Codex, u, now), "~") {
		t.Error("fresh Codex data marked stale")
	}
	if !strings.Contains(renderUsage(Codex, u, now.Add(3*time.Minute)), "~") || !strings.Contains(renderUsage(Claude, u, now), "~") {
		t.Error("last observed usage must be marked cached")
	}
	u.Buckets = append(u.Buckets, UsageBucket{Name: "#[fg=red]\n#{shell}"})
	got := renderUsage(Codex, u, now)
	if strings.Contains(got, "#{shell}") || strings.Contains(got, "#[fg=red]") || strings.Contains(got, "\n") {
		t.Errorf("provider label escaped into tmux format: %q", got)
	}
}

func TestUsageCacheSeparatesAgentHomes(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	one := usagePath(Codex)
	t.Setenv("CODEX_HOME", t.TempDir())
	if usagePath(Codex) == one {
		t.Error("two agent homes share account usage")
	}
}

func TestInstallClaudeUsagePreservesSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	data := []byte(`{"statusLine":{"type":"command","command":"my-status --format 'hello'","padding":2},"model":"opus"}`)
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaudeUsage(root); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(root, ".claude", "settings.local.json")
	local, _ := os.ReadFile(localPath)
	var cfg struct {
		Status struct {
			Command string `json:"command"`
			Padding int    `json:"padding"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(local, &cfg); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(cfg.Status.Command)
	original, err := base64.StdEncoding.DecodeString(strings.Trim(fields[len(fields)-1], "'"))
	if err != nil || string(original) != "my-status --format 'hello'" || cfg.Status.Padding != 2 {
		t.Errorf("lost custom status command/settings: %s (%v)", local, err)
	}
	if err := InstallClaudeUsage(root); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(localPath)
	if string(again) != string(local) {
		t.Error("a second install nested the wrapper or changed settings")
	}
	// The capture lives in the project, not in the user's global config.
	got, _ := os.ReadFile(path)
	if string(got) != string(data) || !json.Valid(got) {
		t.Error("InstallClaudeUsage changed global settings")
	}
}

func TestInstallClaudeUsageWithoutCustomStatus(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for range 2 {
		if err := InstallClaudeUsage(root); err != nil {
			t.Fatal(err)
		}
	}
}
