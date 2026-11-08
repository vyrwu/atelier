package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func readGrokUsage() (Usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "grok", "usage", "--json")
	cmd.Dir = filepath.Dir(usagePath(Grok))
	cmd.Env = withoutSpace(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return Usage{}, err
	}
	return decodeGrokUsage(out, time.Now())
}

// The native billing response is metadata, not a model call. Older shell-route
// builds and team-managed billing cannot report a quota; keep it unknown.
func decodeGrokUsage(data []byte, now time.Time) (Usage, error) {
	var response struct {
		Config *struct {
			Used   *float64 `json:"creditUsagePercent"`
			Period struct {
				Type string    `json:"type"`
				End  time.Time `json:"end"`
			} `json:"currentPeriod"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return Usage{}, err
	}
	c := response.Config
	if c == nil || c.Used == nil || *c.Used < 0 || *c.Used > 100 || !c.Period.End.After(now) {
		return Usage{}, fmt.Errorf("grok: native account quota unavailable")
	}
	minutes := 0
	if c.Period.Type == "WEEKLY" {
		minutes = 7 * 24 * 60
	}
	return Usage{Observed: now, Source: "native", Buckets: []UsageBucket{{ID: "grok", Primary: &UsageWindow{
		Used: c.Used, Minutes: minutes, Reset: c.Period.End.Unix(), Native: true,
	}}}}, nil
}
