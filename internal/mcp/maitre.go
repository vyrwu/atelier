package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/operator"
)

func maitreTools() []interface{} {
	field := func(kind, description string) map[string]interface{} {
		return map[string]interface{}{"type": kind, "description": description}
	}
	space := field("string", "Space slug from list_spaces; never maitre")
	agent := map[string]interface{}{"type": "string", "enum": []string{"claude", "codex", "grok"}}
	tool := func(name, description string, props map[string]interface{}, required ...string) interface{} {
		return map[string]interface{}{"name": name, "description": description, "inputSchema": map[string]interface{}{"type": "object", "properties": props, "required": append([]string{}, required...)}}
	}
	single := func(name, description string) interface{} {
		return tool(name, description, map[string]interface{}{"space": space}, "space")
	}
	return []interface{}{
		tool("list_spaces", "List active spaces with status, worktrees and cached PRs; use trash for retired spaces.", map[string]interface{}{"trash": field("boolean", "List the Trash instead")}),
		tool("create_space", "Start an agent in a new space and return its slug as soon as it launches.", map[string]interface{}{"intent": field("string", "Self-contained task: repository, goal and completion criteria"), "agent": agent}, "intent"),
		tool("rename_space", "Change a space's display title; its slug stays fixed.", map[string]interface{}{"space": space, "title": field("string", "1–80 characters")}, "space", "title"),
		single("retire_space", "Stop a space and put it in the Trash, keeping its files and conversation."),
		single("restore_space", "Restore a space from the Trash and resume its agent."),
		single("delete_space", "Permanently delete a space, after the human confirms in the visible Maître popup. Hidden or unanswered requests are refused."),
		tool("purge_spaces", "Delete old spaces after one human confirmation; keep running sessions and unsaved work.", map[string]interface{}{"older_than": field("string", "Idle age, for example 30d, 2w or 36h"), "all": field("boolean", "Include active spaces")}, "older_than"),
		tool("swap_agent", "Hand an active space's conversation to another agent.", map[string]interface{}{"space": space, "to": agent}, "space", "to"),
		tool("read_screen", "Read the agent's visible screen plus scrollback, joining wrapped lines. Read before sending input.", map[string]interface{}{"space": space, "lines": field("integer", "Scrollback lines, default 60 (0–100000)")}, "space"),
		tool("send_input", "Type literal text and/or menu keys into a space's agent. Read its screen first.", map[string]interface{}{"space": space, "text": field("string", "Literal text"), "keys": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string", "enum": []string{"Enter", "Escape", "Up", "Down", "Tab", "1", "2", "3", "4", "5", "6", "7", "8", "9", "C-c"}}}, "enter": field("boolean", "Submit text after keys (default true); ignored for keys-only input")}, "space"),
		tool("wait_for_change", "Wait for a space to become blocked or idle after this call starts. Use instead of polling; returns [] on timeout.", map[string]interface{}{"timeout_seconds": field("integer", "Default 300 (Grok 50); maximum 1800")}),
		single("focus_space", "Move the human's outer client into a space and close the Maître popup."),
	}
}

func callMaitreTool(ctx context.Context, p callEnvelope) map[string]interface{} {
	var spec map[string]interface{}
	for _, t := range maitreTools() {
		s := t.(map[string]interface{})
		if s["name"] == p.Name {
			spec = s
			break
		}
	}
	if spec == nil {
		return textResult("unknown tool: "+p.Name, true)
	}
	var a struct {
		Space     string   `json:"space"`
		Title     string   `json:"title"`
		Intent    string   `json:"intent"`
		Agent     string   `json:"agent"`
		To        string   `json:"to"`
		OlderThan string   `json:"older_than"`
		Trash     bool     `json:"trash"`
		All       bool     `json:"all"`
		Lines     *int     `json:"lines"`
		Text      string   `json:"text"`
		Keys      []string `json:"keys"`
		Enter     *bool    `json:"enter"`
		Timeout   int64    `json:"timeout_seconds"`
	}
	if len(p.Arguments) == 0 {
		p.Arguments = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(p.Arguments, &a); err != nil {
		return textResult("invalid arguments: "+err.Error(), true)
	}
	values := map[string]string{"space": a.Space, "title": a.Title, "intent": a.Intent, "to": a.To, "older_than": a.OlderThan}
	for _, key := range spec["inputSchema"].(map[string]interface{})["required"].([]string) {
		if strings.TrimSpace(values[key]) == "" {
			return textResult(key+" is required", true)
		}
	}
	var result interface{} = "ok"
	var err error
	switch p.Name {
	case "list_spaces":
		result, err = operator.List(a.Trash)
	case "create_space":
		var slug string
		slug, err = operator.Create(a.Intent, a.Agent)
		result = map[string]string{"slug": slug}
	case "rename_space":
		err = operator.Rename(a.Space, a.Title)
	case "retire_space":
		err = operator.Retire(a.Space)
	case "restore_space":
		err = operator.Restore(a.Space)
	case "delete_space":
		err = operator.Delete(ctx, a.Space, func(prompt string) error { return operator.ConfirmDestroy(ctx, prompt) })
	case "purge_spaces":
		result, err = operator.Purge(ctx, a.OlderThan, a.All)
	case "swap_agent":
		result, err = operator.Swap(a.Space, a.To)
	case "read_screen":
		lines := operator.DefaultLines
		if a.Lines != nil {
			lines = *a.Lines
		}
		result, err = operator.Read(a.Space, lines)
	case "send_input":
		err = operator.Send(a.Space, a.Text, a.Keys, a.Enter == nil || *a.Enter)
	case "wait_for_change":
		if a.Timeout < 0 || a.Timeout > int64(operator.MaxWait/time.Second) {
			err = fmt.Errorf("timeout_seconds must be between 0 and 1800")
		} else {
			result, err = operator.Wait(ctx, time.Duration(a.Timeout)*time.Second)
		}
	case "focus_space":
		err = operator.Focus(a.Space)
	}
	if err != nil {
		return textResult(err.Error(), true)
	}
	if s, ok := result.(string); ok {
		return textResult(s, false)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return textResult(err.Error(), true)
	}
	return textResult(string(data), false)
}
