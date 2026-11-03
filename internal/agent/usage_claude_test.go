package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeClaudeNativeUsage(t *testing.T) {
	now := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	data, _ := json.Marshal(map[string]any{
		"local_command": "usage", "num_turns": 0, "duration_api_ms": 0,
		"result": "You are using your subscription\n\n" +
			"Current session: 25% used · resets Nov 3 at 6:10pm (UTC)\n" +
			"Current week (all models): 100% used · resets Nov 4 at 3am (UTC)\n" +
			"Current week (Fable): 0% used · resets Nov 4 at 3am (UTC)\n",
	})
	u, err := decodeClaudeUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	b := u.Buckets[0]
	if u.Source != "native" || *b.Primary.Used != 25 || *b.Secondary.Used != 100 {
		t.Fatalf("native Claude usage = %+v", u)
	}
	if b.Primary.Reset != time.Date(2026, 11, 3, 18, 10, 0, 0, time.UTC).Unix() {
		t.Errorf("5h reset = %v", time.Unix(b.Primary.Reset, 0))
	}
	got := renderUsage(Claude, u, now)
	for _, want := range []string{"5h #[fg=colour71]75%", "7d #[fg=colour167]0%"} {
		if !strings.Contains(got, want) {
			t.Errorf("native usage display lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "~") {
		t.Errorf("fresh account reading is marked cached: %s", got)
	}
}

func TestDecodeClaudeNativeUsageFailsClosed(t *testing.T) {
	for _, input := range []string{
		`{"result":"some model response","num_turns":1}`,
		`{"local_command":"usage","is_error":true}`,
		`{"local_command":"usage","num_turns":1}`,
		`{"local_command":"usage","total_cost_usd":0.1}`,
		`{"local_command":"usage","result":"the output format changed"}`,
	} {
		if _, err := decodeClaudeUsage([]byte(input), time.Now()); err == nil {
			t.Errorf("accepted unsupported usage output: %s", input)
		}
	}
}

func TestClaudeNativeResetYearBoundary(t *testing.T) {
	now := time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)
	data, _ := json.Marshal(map[string]any{"local_command": "usage", "result": "Current week (all models): 75% used · resets Jan 2 at 3:00am (UTC)\n"})
	u, err := decodeClaudeUsage(data, now)
	if err != nil || time.Unix(u.Buckets[0].Secondary.Reset, 0).Year() != 2027 {
		t.Fatalf("year rollover: %+v, %v", u, err)
	}
}
