package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// The TUI's /usage calls this ACP extension. No session or prompt is created:
// the CLI owns authentication and only fetches the account billing metadata.
func readGrokUsage() (Usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "grok", "agent", "--no-leader", "stdio")
	cmd.Dir = filepath.Dir(usagePath(Grok))
	cmd.Env = withoutSpace(os.Environ())
	input, err := cmd.StdinPipe()
	if err != nil {
		return Usage{}, err
	}
	defer func() { _ = input.Close() }()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return Usage{}, err
	}
	if err := cmd.Start(); err != nil {
		return Usage{}, err
	}
	defer func() {
		_ = input.Close()
		// EOF normally shuts down the private agent. Bound cleanup if it stalls.
		kill := time.AfterFunc(time.Second, func() { _ = cmd.Process.Kill() })
		_ = cmd.Wait()
		kill.Stop()
	}()
	send := json.NewEncoder(input)
	if err := send.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": 1, "clientCapabilities": map[string]any{},
			"clientInfo": map[string]string{"name": "atelier-usage", "version": "1"},
		},
	}); err != nil {
		return Usage{}, err
	}
	scan := bufio.NewScanner(output)
	scan.Buffer(make([]byte, 4096), 2<<20)
	expected := 1
	for scan.Scan() {
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(scan.Bytes(), &msg) != nil || msg.JSONRPC != "2.0" {
			continue
		}
		var id int
		if json.Unmarshal(msg.ID, &id) != nil || id != expected {
			continue
		}
		if msg.Error != nil {
			return Usage{}, fmt.Errorf("grok usage: %s (%d)", msg.Error.Message, msg.Error.Code)
		}
		if expected == 2 {
			return decodeGrokUsage(msg.Result, time.Now())
		}
		var init struct {
			Version int `json:"protocolVersion"`
		}
		if json.Unmarshal(msg.Result, &init) != nil || init.Version != 1 {
			return Usage{}, fmt.Errorf("grok usage: unsupported ACP version")
		}
		// ACP extension method names carry a leading underscore on the wire.
		if err := send.Encode(map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "_x.ai/billing", "params": map[string]any{},
		}); err != nil {
			return Usage{}, err
		}
		expected = 2
	}
	if err := scan.Err(); err != nil {
		return Usage{}, err
	}
	return Usage{}, fmt.Errorf("grok usage: no billing response")
}

// Only an explicit account percentage with a future reset is a usable quota.
// Missing account data and session token/cost counters remain unknown.
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
	if c.Period.Type == "USAGE_PERIOD_TYPE_WEEKLY" || c.Period.Type == "WEEKLY" {
		minutes = 7 * 24 * 60
	}
	return Usage{Observed: now, Source: "native", Buckets: []UsageBucket{{ID: "grok", Primary: &UsageWindow{
		Used: c.Used, Minutes: minutes, Reset: c.Period.End.Unix(), Native: true,
	}}}}, nil
}
