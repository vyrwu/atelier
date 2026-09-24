// Package mcp is a minimal stdio MCP (Model Context Protocol) server exposing a
// single tool, register_pr. It is the FR-G2 gap-filler: when the agent opens a
// PR that atelier's branch-matching sweep wouldn't discover (e.g. a fork, or a
// branch that doesn't match the worktree), the agent calls register_pr to pin
// it to the current workspace so it shows up in the Changes view.
//
// The framing is newline-delimited JSON-RPC 2.0 over stdin/stdout, hand-rolled
// so there is no SDK dependency — the surface is tiny (initialize / tools/list
// / tools/call). The current workspace is resolved from $ATELIER_SESSION.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/vyrwu/atelier/internal/core"
)

const protocolVersion = "2024-11-05"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve runs the stdio MCP server: it reads newline-delimited JSON-RPC 2.0
// requests from stdin, dispatches the MCP methods, and writes responses to
// stdout. It runs until stdin closes.
func Serve() error {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		resp, ok := dispatch(req)
		if !ok {
			continue // notification: no reply
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

// dispatch handles one request, returning the response and whether to send it
// (false for notifications, which have no id and get no reply).
func dispatch(req request) (response, bool) {
	if len(req.ID) == 0 {
		return response{}, false
	}
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "atelier", "version": "1"},
		}
	case "tools/list":
		resp.Result = map[string]interface{}{"tools": []interface{}{
			registerPRSpec(), createWorktreeSpec(), createPRSpec(),
		}}
	case "tools/call":
		resp.Result = callTool(req.Params)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp, true
}

// registerPRSpec is the register_pr tool catalog entry.
func registerPRSpec() map[string]interface{} {
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	return map[string]interface{}{
		"name":        "register_pr",
		"description": "Register a pull request you opened with the current atelier workspace so it is tracked in the Changes view, even if branch-matching wouldn't find it.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"repo":   str("owner/repo"),
				"number": map[string]interface{}{"type": "integer", "description": "the PR number"},
				"title":  str("the PR title"),
				"url":    str("the GitHub PR URL"),
				"state":  str("open, draft, merged, or closed"),
				"ci":     str("pass, fail, pending, or none"),
				"review": str("approved, changes_requested, review_required, or none"),
			},
			"required": []string{"repo", "number", "url"},
		},
	}
}

// callEnvelope is the common tools/call shape: a tool name plus opaque
// arguments each handler decodes itself.
type callEnvelope struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// callTool dispatches a tools/call to the named tool. Failures are returned as
// an error content result (so the agent reads the message) rather than a
// JSON-RPC error.
func callTool(params json.RawMessage) map[string]interface{} {
	var p callEnvelope
	if json.Unmarshal(params, &p) != nil {
		return textResult("invalid tool call params", true)
	}
	switch p.Name {
	case "register_pr":
		return handleRegisterPR(p.Arguments)
	case "create_worktree":
		return handleCreateWorktree(p.Arguments)
	case "create_pr":
		return handleCreatePR(p.Arguments)
	default:
		return textResult("unknown tool: "+p.Name, true)
	}
}

// resolveWorkspace returns the workspace for the current session ($ATELIER_SESSION).
func resolveWorkspace() (*core.Workspace, bool) {
	ws := core.Load().FindBySession(os.Getenv("ATELIER_SESSION"))
	return ws, ws != nil
}

// registerPR upserts pr into slug's workspace (matched by repo+number).
func registerPR(slug string, pr core.PR) error {
	return core.Update(func(s *core.State) {
		w := s.Find(slug)
		if w == nil {
			return
		}
		for i := range w.PRs {
			if w.PRs[i].Repo == pr.Repo && w.PRs[i].Number == pr.Number {
				w.PRs[i] = pr
				return
			}
		}
		w.PRs = append(w.PRs, pr)
	})
}

func handleRegisterPR(raw json.RawMessage) map[string]interface{} {
	var a struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		State  string `json:"state"`
		CI     string `json:"ci"`
		Review string `json:"review"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return textResult("invalid arguments", true)
	}
	ws, ok := resolveWorkspace()
	if !ok {
		return textResult("no atelier workspace for this session", true)
	}
	pr := core.PR{
		Repo:       a.Repo,
		Number:     a.Number,
		Title:      a.Title,
		URL:        a.URL,
		State:      parseState(a.State),
		CI:         parseCI(a.CI),
		Review:     parseReview(a.Review),
		Registered: true,
	}
	if err := registerPR(ws.Slug, pr); err != nil {
		return textResult("register failed: "+err.Error(), true)
	}
	return textResult(fmt.Sprintf("registered %s#%d", pr.Repo, pr.Number), false)
}

// parseState maps loose input to a PRState, defaulting to open.
func parseState(s string) core.PRState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "draft":
		return core.PRDraft
	case "merged":
		return core.PRMerged
	case "closed":
		return core.PRClosed
	default:
		return core.PROpen
	}
}

// parseCI maps loose input to a CIState, defaulting to none.
func parseCI(s string) core.CIState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pass":
		return core.CIPass
	case "fail":
		return core.CIFail
	case "pending":
		return core.CIPending
	default:
		return core.CINone
	}
}

// parseReview maps loose input to a Review, defaulting to none.
func parseReview(s string) core.Review {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "approved":
		return core.ReviewApproved
	case "changes_requested", "changes":
		return core.ReviewChanges
	case "review_required", "required":
		return core.ReviewRequired
	default:
		return core.ReviewNone
	}
}

// textResult wraps a string as an MCP tool-call content result.
func textResult(text string, isError bool) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isError,
	}
}
