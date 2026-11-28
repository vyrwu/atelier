package agent

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Native agy 1.5.3 transcript_full.jsonl is one trajectory step per line.
// Export only explicit user prompts and planner text/tool calls; never thinking,
// tool results, or system-injected context. Full logs avoid compact truncation.
func exportAntigravity(r io.Reader, since time.Time, w *exportWriter) error {
	dec := json.NewDecoder(r)
	for {
		var rec struct {
			Type    string    `json:"type"`
			Source  string    `json:"source"`
			Created time.Time `json:"created_at"`
			Content string    `json:"content"`
			Tools   []struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"tool_calls"`
		}
		if err := dec.Decode(&rec); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		// Native step timestamps have second precision. Include the boundary
		// second rather than losing a prompt written just after a swap.
		if rec.Created.Before(since.Truncate(time.Second)) {
			continue
		}
		switch {
		case rec.Type == "USER_INPUT" && rec.Source == "USER_EXPLICIT":
			content := rec.Content
			if strings.HasPrefix(content, "<USER_REQUEST>\n") {
				if text, _, ok := strings.Cut(strings.TrimPrefix(content, "<USER_REQUEST>\n"), "\n</USER_REQUEST>"); ok {
					content = text
				}
			}
			w.say("Operator", content)
		case rec.Type == "PLANNER_RESPONSE" && rec.Source == "MODEL":
			w.say(w.agent, rec.Content)
			for _, tool := range rec.Tools {
				w.tool(tool.Name, toolInput(tool.Args))
			}
		}
	}
}
