package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Current Claude Code exposes /usage as a non-interactive local command. The
// native CLI owns authentication and fetches the account counters; this does
// not send an inference request or read subscription credentials in atelier.
func readClaudeUsage() (Usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "json",
		"--no-session-persistence", "--strict-mcp-config", "--tools", "",
		"--settings", `{"disableAllHooks":true}`, "/usage")
	cmd.Dir = filepath.Dir(usagePath(Claude))
	for _, entry := range withoutSpace(os.Environ()) {
		key, _, _ := strings.Cut(entry, "=")
		if key != "CLAUDECODE" && key != "TZ" && key != "LC_ALL" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	// The command's result text has English labels and a human reset date.
	// Pin UTC and locale so parsing never depends on the desktop's timezone.
	cmd.Env = append(cmd.Env, "TZ=UTC", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return Usage{}, err
	}
	return decodeClaudeUsage(out, time.Now())
}

var claudeUsageLine = regexp.MustCompile(`(?m)^Current (session|week \(all models\)): ([0-9]+(?:\.[0-9]+)?)% used · resets (.+?) \(UTC\)\s*$`)

// decodeClaudeUsage fails closed if /usage is unsupported or its output
// changes. Validate the native local-command envelope, then select exactly the
// session and all-model weekly counters, excluding model-specific weeks.
func decodeClaudeUsage(data []byte, now time.Time) (Usage, error) {
	var result struct {
		Command string  `json:"local_command"`
		Error   bool    `json:"is_error"`
		Turns   int     `json:"num_turns"`
		APIMs   int     `json:"duration_api_ms"`
		Cost    float64 `json:"total_cost_usd"`
		Text    string  `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return Usage{}, err
	}
	if result.Command != "usage" || result.Error || result.Turns != 0 || result.APIMs != 0 || result.Cost != 0 {
		return Usage{}, fmt.Errorf("claude: native non-interactive /usage unavailable")
	}
	b := UsageBucket{ID: "claude"}
	for _, m := range claudeUsageLine.FindAllStringSubmatch(result.Text, -1) {
		used, err := strconv.ParseFloat(m[2], 64)
		if err != nil || used < 0 || used > 100 {
			return Usage{}, fmt.Errorf("claude: invalid usage percentage %q", m[2])
		}
		date := fmt.Sprintf("%d %s", now.UTC().Year(), m[3])
		reset, err := time.ParseInLocation("2006 Jan 2 at 3:04pm", date, time.UTC)
		if err != nil {
			reset, err = time.ParseInLocation("2006 Jan 2 at 3pm", date, time.UTC)
		}
		if err != nil {
			return Usage{}, fmt.Errorf("claude: unsupported reset date %q", m[3])
		}
		// A reset in early January can be reported during late December.
		if now.UTC().Month() == time.December && reset.Month() == time.January {
			reset = reset.AddDate(1, 0, 0)
		}
		w := &UsageWindow{Used: &used, Reset: reset.Unix(), Minutes: 300, Native: true}
		if m[1] == "session" {
			b.Primary = w
		} else {
			w.Minutes = 10080
			b.Secondary = w
		}
	}
	if b.Primary == nil && b.Secondary == nil {
		return Usage{}, fmt.Errorf("claude: no account windows in /usage response")
	}
	return Usage{Observed: now, Source: "native", Buckets: []UsageBucket{b}}, nil
}
