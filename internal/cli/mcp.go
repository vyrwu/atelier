package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// MCPCommand exposes `atelier mcp serve` — a stdio MCP (Model Context Protocol)
// server that is a THIN WRAPPER over the kernel workspace/pr CLI verbs (WS-5).
// MCP is just Claude's transport; the capability is the kernel's. The claude
// adapter registers this server into the interactive agent via --mcp-config, so
// the driver agent can grow its workspace + register PRs without leaving the
// popup. Background naming/recap/summary calls never get MCP (they run
// --tools "").
//
// Hand-rolled newline-delimited JSON-RPC 2.0 (the MCP stdio framing) — no new
// dependency; the surface is tiny (initialize / tools/list / tools/call).
func MCPCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Model Context Protocol server (stdio) exposing atelier's workspace verbs",
	}
	c.AddCommand(mcpServeCmd())
	return c
}

func mcpServeCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run the stdio MCP server (registered into the interactive agent)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPServer(os.Stdin, cmd.OutOrStdout(), tmuxhost.New(socket))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (default: ATELIER_TMUX_SOCKET)")
	return c
}

// --- JSON-RPC plumbing -------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const mcpProtocolVersion = "2024-11-05"

// runMCPServer reads newline-delimited JSON-RPC requests, dispatches MCP
// methods, and writes responses. Runs until stdin closes.
func runMCPServer(in io.Reader, out io.Writer, h *tmuxhost.Client) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			debuglog.LogErr("mcp: parse request", err)
			continue
		}
		resp, notify := dispatchMCP(h, req)
		if notify {
			continue // notifications get no response
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// dispatchMCP handles one request. Returns (response, isNotification).
func dispatchMCP(h *tmuxhost.Client, req rpcRequest) (rpcResponse, bool) {
	base := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		base.Result = map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "atelier", "version": "1"},
		}
		return base, false
	case "notifications/initialized", "notifications/cancelled":
		return rpcResponse{}, true
	case "tools/list":
		base.Result = map[string]interface{}{"tools": mcpTools()}
		return base, false
	case "tools/call":
		base.Result = mcpCallTool(h, req.Params)
		return base, false
	case "ping":
		base.Result = map[string]interface{}{}
		return base, false
	default:
		base.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
		return base, false
	}
}

// mcpTools is the tool catalog — one per kernel verb.
func mcpTools() []map[string]interface{} {
	strProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	tool := func(name, desc string, props map[string]interface{}, required []string) map[string]interface{} {
		schema := map[string]interface{}{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return map[string]interface{}{"name": name, "description": desc, "inputSchema": schema}
	}
	return []map[string]interface{}{
		tool("workspace_worktree_add",
			"Add a repository+branch worktree to the current atelier workspace (creates the git worktree and symlinks it into the workspace root).",
			map[string]interface{}{"repo": strProp("owner/repo (must exist in the code root)"), "branch": strProp("branch name for the worktree")},
			[]string{"repo", "branch"}),
		tool("workspace_worktree_list", "List the current workspace's worktrees.", map[string]interface{}{}, nil),
		tool("workspace_context", "Show the current workspace's intent, worktrees, and pull requests.", map[string]interface{}{}, nil),
		tool("pr_register", "Register a pull request you opened with the current workspace so atelier tracks it in the Changes view.",
			map[string]interface{}{"url": strProp("the GitHub PR URL")}, []string{"url"}),
		tool("pr_list", "List the current workspace's pull requests.", map[string]interface{}{}, nil),
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// mcpCallTool dispatches a tools/call to the matching kernel verb and returns
// the MCP content result (text). Errors are returned as isError content, not
// JSON-RPC errors, so the agent sees the message.
func mcpCallTool(h *tmuxhost.Client, params json.RawMessage) map[string]interface{} {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcpText("invalid tool call params: "+err.Error(), true)
	}
	args := map[string]string{}
	if len(p.Arguments) > 0 {
		_ = json.Unmarshal(p.Arguments, &args)
	}
	text, err := runMCPTool(h, p.Name, args)
	if err != nil {
		return mcpText(err.Error(), true)
	}
	return mcpText(text, false)
}

// runMCPTool executes one tool by name against the current workspace. Shared
// with the CLI verbs via the same underlying helpers.
func runMCPTool(h *tmuxhost.Client, name string, args map[string]string) (string, error) {
	ws, err := resolveCurrentWorkspace(h)
	if err != nil {
		return "", err
	}
	switch name {
	case "workspace_worktree_add":
		repo, branch := args["repo"], args["branch"]
		if repo == "" || branch == "" {
			return "", fmt.Errorf("repo and branch are required")
		}
		repoPath := filepath.Join(verbCodeRoot(), repo)
		if _, err := os.Stat(repoPath); err != nil {
			return "", fmt.Errorf("repo %q not found at %s", repo, repoPath)
		}
		wtPath := filepath.Join(verbWorktreeRoot(), repo, branch)
		wt, err := workspace.AddWorktree(h, ws.Session, ws.Root, repoPath, repo, branch, wtPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Added worktree %s@%s → %s", repo, branch, wt.Link), nil
	case "workspace_worktree_list":
		var b strings.Builder
		for _, wt := range loadWorktrees(ws.Session) {
			fmt.Fprintf(&b, "%s @ %s → %s\n", wt.Repo, wt.Branch, wt.Link)
		}
		if b.Len() == 0 {
			return "(no worktrees yet)", nil
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "workspace_context":
		return mcpWorkspaceContext(ws.Session, ws.ID, ws.Root), nil
	case "pr_register":
		url := args["url"]
		repo, number, ok := parsePRURL(url)
		if !ok {
			return "", fmt.Errorf("could not parse PR URL %q", url)
		}
		if err := statestore.UpdateWorkspace(ws.Session, func(w *statestore.Workspace) {
			for i := range w.PRs {
				if w.PRs[i].Repo == repo && w.PRs[i].Number == number {
					w.PRs[i].Registered = true
					w.PRs[i].URL = url
					return
				}
			}
			w.PRs = append(w.PRs, statestore.PR{Number: number, Repo: repo, URL: url, State: string(integration.ForgeOpen), Registered: true})
		}); err != nil {
			return "", err
		}
		workspace.SpawnForgeRefresh()
		return fmt.Sprintf("Registered %s #%d", repo, number), nil
	case "pr_list":
		var b strings.Builder
		for _, pr := range loadPRs(ws.Session) {
			fmt.Fprintf(&b, "%s #%d [%s] %s\n", pr.Repo, pr.Number, pr.State, pr.Title)
		}
		if b.Len() == 0 {
			return "(no pull requests)", nil
		}
		return strings.TrimRight(b.String(), "\n"), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

// mcpWorkspaceContext renders the workspace context as text (shared shape with
// the CLI `workspace context` verb).
func mcpWorkspaceContext(session, id, root string) string {
	st, _ := statestore.Load()
	rec := &statestore.Workspace{SessionName: session}
	if st != nil {
		if r := st.FindWorkspace(session); r != nil {
			rec = r
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Workspace: %s\n", coalesce(rec.Title, id))
	if rec.Intent != "" {
		fmt.Fprintf(&b, "Intent: %s\n", rec.Intent)
	}
	fmt.Fprintf(&b, "Root: %s\n", root)
	fmt.Fprintf(&b, "Worktrees (%d):\n", len(rec.Worktrees))
	for _, wt := range rec.Worktrees {
		fmt.Fprintf(&b, "  %s @ %s → %s\n", wt.Repo, wt.Branch, wt.Link)
	}
	fmt.Fprintf(&b, "Pull requests (%d):\n", len(rec.PRs))
	for _, pr := range rec.PRs {
		fmt.Fprintf(&b, "  %s #%d [%s] ci=%s review=%s — %s\n",
			pr.Repo, pr.Number, pr.State, pr.CI, pr.ReviewDecision, pr.Title)
	}
	return strings.TrimRight(b.String(), "\n")
}

// mcpText wraps a string as an MCP tool-call content result.
func mcpText(text string, isError bool) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isError,
	}
}
