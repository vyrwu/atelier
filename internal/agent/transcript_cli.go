package agent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func latestGrokSession(sessions, root string, since time.Time) (string, string) {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	for _, path := range newestFirst(sessions, since, func(name string) bool { return name == "updates.jsonl" }) {
		var summary struct {
			Info   struct{ ID, Cwd string } `json:"info"`
			Kind   string                   `json:"session_kind"`
			Hidden bool                     `json:"hidden"`
		}
		// #nosec G304 -- Session metadata is selected from the owner's configured Grok session directory.
		data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "summary.json"))
		if err != nil || json.Unmarshal(data, &summary) != nil || summary.Info.Cwd != root || summary.Hidden {
			continue
		}
		switch summary.Kind {
		case "", "interactive", "tui", "cli":
			if summary.Info.ID != "" {
				return summary.Info.ID, path
			}
		}
	}
	return "", ""
}

var cursorDash = regexp.MustCompile(`-+`)

func latestCursorSession(projects, root string, since time.Time) (string, string) {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	encoded := strings.Trim(cursorDash.ReplaceAllString(notAlnum.ReplaceAllString(root, "-"), "-"), "-")
	dir := filepath.Join(projects, encoded, "agent-transcripts")
	for _, path := range newestFirst(dir, since, func(name string) bool { return strings.HasSuffix(name, ".jsonl") }) {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		// Current and legacy primary layouts; subagent transcripts are nested deeper.
		if filepath.Dir(path) == dir || filepath.Dir(path) == filepath.Join(dir, id) {
			return id, path
		}
	}
	return "", ""
}

// Grok's persisted ACP updates contain timestamps and the displayed messages,
// without the synthetic system context from chat_history.jsonl.
func exportGrok(r io.Reader, since time.Time, w *exportWriter) error {
	dec := json.NewDecoder(r)
	var text strings.Builder
	who := ""
	flush := func() { w.say(who, text.String()); text.Reset() }
	for {
		var rec struct {
			Timestamp int64 `json:"timestamp"`
			Params    struct {
				Meta struct {
					Timestamp int64 `json:"agentTimestampMs"`
				} `json:"_meta"`
				Update struct {
					Kind    string          `json:"sessionUpdate"`
					Content json.RawMessage `json:"content"`
					Title   string          `json:"title"`
					Input   json.RawMessage `json:"rawInput"`
				} `json:"update"`
			} `json:"params"`
		}
		if err := dec.Decode(&rec); err != nil {
			flush()
			if err == io.EOF {
				return nil
			}
			return err
		}
		at := time.Unix(rec.Timestamp, 0)
		if rec.Params.Meta.Timestamp != 0 {
			at = time.UnixMilli(rec.Params.Meta.Timestamp)
		}
		if at.Before(since) {
			continue
		}
		u := rec.Params.Update
		switch u.Kind {
		case "user_message_chunk", "agent_message_chunk":
			next := w.agent
			if u.Kind == "user_message_chunk" {
				next = "Operator"
			}
			// User updates are submitted messages, including steering sent
			// before a reply. Only assistant deltas form a streamed message.
			if next != who || u.Kind == "user_message_chunk" {
				flush()
				who = next
			}
			var content struct{ Type, Text string }
			if json.Unmarshal(u.Content, &content) == nil && content.Type == "text" {
				_, _ = text.WriteString(content.Text) // strings.Builder writes cannot fail.
			}
		case "tool_call":
			flush()
			w.tool(u.Title, toolInput(u.Input))
		}
	}
}
