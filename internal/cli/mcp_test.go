package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// The MCP protocol methods exercised here (initialize, tools/list, ping,
// unknown) never touch tmux — dispatchMCP only reaches for the client on
// tools/call. So a plain client is fine; no workspace is resolved.

func TestDispatchMCP_Initialize(t *testing.T) {
	h := tmuxhost.New("")
	resp, notify := dispatchMCP(h, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize"})
	if notify {
		t.Fatal("initialize must produce a response, not a notification")
	}
	m, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	if m["protocolVersion"] != mcpProtocolVersion {
		t.Errorf("protocolVersion = %v, want %q", m["protocolVersion"], mcpProtocolVersion)
	}
	caps, ok := m["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities missing/wrong type: %v", m["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities must advertise a tools capability; got %v", caps)
	}
}

func TestDispatchMCP_ToolsList(t *testing.T) {
	h := tmuxhost.New("")
	resp, notify := dispatchMCP(h, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"})
	if notify {
		t.Fatal("tools/list must produce a response")
	}
	m, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	tools, ok := m["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tools type = %T, want []map", m["tools"])
	}
	got := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		got[name] = true
	}
	for _, want := range []string{
		"workspace_worktree_add", "workspace_worktree_list",
		"workspace_context", "pr_register", "pr_list",
	} {
		if !got[want] {
			t.Errorf("tools/list missing %q; got %v", want, got)
		}
	}
}

func TestDispatchMCP_Ping(t *testing.T) {
	h := tmuxhost.New("")
	resp, notify := dispatchMCP(h, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "ping"})
	if notify {
		t.Fatal("ping must produce a response")
	}
	if resp.Error != nil {
		t.Errorf("ping returned error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Error("ping must return a (empty) result object")
	}
}

func TestDispatchMCP_UnknownMethod(t *testing.T) {
	h := tmuxhost.New("")
	resp, notify := dispatchMCP(h, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`4`), Method: "no/such/method"})
	if notify {
		t.Fatal("an unknown method must still produce an error response")
	}
	if resp.Error == nil {
		t.Fatal("unknown method must return a JSON-RPC error")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("unknown method code = %d, want -32601 (method not found)", resp.Error.Code)
	}
}

func TestDispatchMCP_InitializedNotification(t *testing.T) {
	h := tmuxhost.New("")
	_, notify := dispatchMCP(h, rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
	if !notify {
		t.Error("notifications/initialized must be treated as a notification (no response)")
	}
}

// TestRunMCPServer_RoundTrip drives the server over an in-memory newline-
// delimited JSON-RPC exchange: initialize then tools/list. It asserts one
// framed response per request and that the catalog comes back on the wire.
func TestRunMCPServer_RoundTrip(t *testing.T) {
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := runMCPServer(in, &out, tmuxhost.New("")); err != nil {
		t.Fatalf("runMCPServer: %v", err)
	}

	// The notification gets no response, so exactly two framed lines come back.
	lines := splitNonEmpty(out.String())
	if len(lines) != 2 {
		t.Fatalf("want 2 responses (notification suppressed), got %d:\n%s", len(lines), out.String())
	}

	type resp struct {
		ID     json.Number `json:"id"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			Tools           []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}

	var initResp resp
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("unmarshal initialize resp: %v\n%s", err, lines[0])
	}
	if initResp.ID.String() != "1" || initResp.Result.ProtocolVersion != mcpProtocolVersion {
		t.Errorf("initialize resp wrong: id=%s protocol=%q", initResp.ID, initResp.Result.ProtocolVersion)
	}

	var listResp resp
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("unmarshal tools/list resp: %v\n%s", err, lines[1])
	}
	if listResp.ID.String() != "2" {
		t.Errorf("tools/list resp id = %s, want 2", listResp.ID)
	}
	if len(listResp.Result.Tools) == 0 {
		t.Fatalf("tools/list returned no tools:\n%s", lines[1])
	}
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
