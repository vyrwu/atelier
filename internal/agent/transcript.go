package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LatestSession is the id of the conversation agent k last had in the space at
// root, or "" if it has none. It is read off the agent's own transcripts rather
// than recorded, so it holds for spaces made before atelier knew about agents and
// needs no hook to have fired (NFR-R2). Transcripts older than since — the
// space's creation — belong to an earlier space that had the same name.
func LatestSession(k Kind, root string, since time.Time) string {
	id, _ := latestSession(k, root, since)
	return id
}

// Transcript is the file holding agent k's latest conversation in the space at
// root, or "" (see LatestSession).
func Transcript(k Kind, root string, since time.Time) string {
	_, path := latestSession(k, root, since)
	return path
}

// RecentKind recovers a missing workspace's agent from its newest interactive
// transcript. A directory edit must not become a conversation cutoff.
func RecentKind(root string, fallback Kind) Kind {
	kind := fallback
	var newest time.Time
	for _, k := range Kinds() {
		path := Transcript(k, root, time.Time{})
		if fi, err := os.Stat(path); err == nil && fi.ModTime().After(newest) {
			kind, newest = k, fi.ModTime()
		}
	}
	return kind
}

func latestSession(k Kind, root string, since time.Time) (id, path string) {
	if k == Grok {
		return latestGrokSession(filepath.Join(grokHome(), "sessions"), root, since)
	}
	if k == Cursor {
		return latestCursorSession(filepath.Join(cursorDir(true), "projects"), root, since)
	}
	if k == Codex {
		return latestCodexSession(filepath.Join(codexHome(), "sessions"), root, since)
	}
	return latestClaudeSession(claudeProjectsDir(), root, since)
}

// newestFirst lists the files under dir matching keep, most recently written
// first, skipping any last written before since.
func newestFirst(dir string, since time.Time, keep func(name string) bool) []string {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	walkRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	_ = filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !keep(d.Name()) {
			return nil
		}
		if fi, err := d.Info(); err == nil && !fi.ModTime().Before(since) {
			// Preserve the caller's path spelling: Claude uses its parent to
			// distinguish the main conversation from subagent transcripts.
			rel, err := filepath.Rel(walkRoot, p)
			if err == nil {
				files = append(files, file{filepath.Join(dir, rel), fi.ModTime()})
			}
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

// claudeProjectsDir is where Claude keeps transcripts, one directory per
// launch directory.
func claudeProjectsDir() string {
	dir, legacy := claudeDir()
	if legacy {
		return filepath.Join(dir, ".claude", "projects")
	}
	return filepath.Join(dir, "projects")
}

var notAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// latestClaudeSession is the newest interactive conversation Claude started in
// root. Claude files transcripts under the launch directory with every
// non-alphanumeric replaced by '-', named <session-id>.jsonl. The same directory
// also holds `claude -p` runs (entrypoint sdk-cli), which aren't conversations a
// space resumes, so only an interactive one (entrypoint cli) counts.
func latestClaudeSession(projects, root string, since time.Time) (id, path string) {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	dir := filepath.Join(projects, notAlnum.ReplaceAllString(root, "-"))
	files := newestFirst(dir, since, func(name string) bool { return strings.HasSuffix(name, ".jsonl") })
	for _, f := range files {
		if filepath.Dir(f) != dir {
			continue // a subagent's transcript, in <session-id>/subagents/
		}
		if claudeInteractive(f, root) {
			return strings.TrimSuffix(filepath.Base(f), ".jsonl"), f
		}
	}
	return "", ""
}

// claudeInteractive reports whether the transcript at path is an interactive
// session launched in root: its first record that says how it was started.
func claudeInteractive(path, root string) bool {
	// #nosec G304 -- Reads a transcript selected from the owner's agent session directories.
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	for range 200 {
		var r struct {
			Entrypoint string `json:"entrypoint"`
			Cwd        string `json:"cwd"`
		}
		if dec.Decode(&r) != nil {
			return false
		}
		if r.Entrypoint != "" {
			return r.Entrypoint == "cli" && r.Cwd == root
		}
	}
	return false
}

// latestCodexSession is the newest interactive conversation Codex had in root.
// Codex files every session under sessions/YYYY/MM/DD/rollout-*.jsonl, whose
// first record names its id, its directory, and how it was started; `codex
// exec` runs (source exec) aren't conversations a space resumes. Resuming
// appends to the session's original file, so recency is the file's, not its
// name's.
func latestCodexSession(sessions, root string, since time.Time) (id, path string) {
	// Codex records the directory as the OS reports it, with symlinks resolved.
	roots := map[string]bool{root: true}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		roots[real] = true
	}
	files := newestFirst(sessions, since, func(name string) bool {
		return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl")
	})
	for _, f := range files {
		if id := codexSessionIn(f, roots); id != "" {
			return id, f
		}
	}
	return "", ""
}

// codexSessionIn is the id of the rollout at path when it is an interactive
// session in one of roots, else "".
func codexSessionIn(path string, roots map[string]bool) string {
	// #nosec G304 -- Reads a transcript selected from the owner's agent session directories.
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	var r struct {
		Type    string `json:"type"`
		Payload struct {
			ID     string `json:"id"`
			Cwd    string `json:"cwd"`
			Source string `json:"source"`
		} `json:"payload"`
	}
	if json.NewDecoder(f).Decode(&r) != nil || r.Type != "session_meta" {
		return ""
	}
	if !roots[r.Payload.Cwd] || r.Payload.Source != "cli" {
		return ""
	}
	return r.Payload.ID
}

// HandoffMark opens every message atelier writes into an agent on a swap, so an
// export can leave the previous handoff out of the next one.
const HandoffMark = "[atelier handoff]"

// Export renders agent k's conversation in the transcript at path, from since
// on, as Markdown another agent can pick up: what the operator asked, what the
// agent said, and one line per tool it ran. Tool output and reasoning are left
// out — they are most of a transcript's bytes, and the next agent can re-run
// what it needs.
func Export(k Kind, path string, since time.Time) (string, error) {
	// #nosec G304 -- Reads a transcript selected from the owner's agent session directories.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	w := &exportWriter{agent: k.Label()}
	switch k {
	case Codex:
		err = exportCodex(f, since, w)
	case Grok:
		err = exportGrok(f, since, w)
	case Cursor:
		err = exportMessages(f, since, w, true)
	default:
		err = exportClaude(f, since, w)
	}
	return w.String(), err
}

// exportWriter accumulates an export, heading each run of messages with who
// wrote it.
type exportWriter struct {
	strings.Builder
	agent string
	last  string
}

func (w *exportWriter) say(who, text string) {
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, HandoffMark) {
		return
	}
	if who != w.last {
		fmt.Fprintf(w, "\n## %s\n\n", who)
		w.last = who
	}
	_, _ = w.WriteString(text + "\n\n") // strings.Builder writes cannot fail.
}

func (w *exportWriter) tool(name, input string) {
	if w.last != w.agent {
		fmt.Fprintf(w, "\n## %s\n\n", w.agent)
		w.last = w.agent
	}
	input = strings.Join(strings.Fields(input), " ")
	if r := []rune(input); len(r) > 160 {
		input = string(r[:159]) + "…"
	}
	fmt.Fprintf(w, "- ran `%s` %s\n", name, input)
}

// exportClaude walks a Claude transcript. Each record is one message part: a
// user prompt (a string, or text blocks beside tool results), or an assistant
// text, thinking, or tool_use block. Prompts atelier or Claude itself injected —
// meta records, command echoes, task notifications — are marked or use known
// tags, and are skipped; a compaction summary is kept, since it stands in for
// everything before it.
func exportClaude(r io.Reader, since time.Time, w *exportWriter) error {
	return exportMessages(r, since, w, false)
}

func exportMessages(r io.Reader, since time.Time, w *exportWriter, cursor bool) error {
	type block struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	dec := json.NewDecoder(bufio.NewReader(r))
	for {
		var rec struct {
			Type             string    `json:"type"`
			Role             string    `json:"role"`
			Timestamp        time.Time `json:"timestamp"`
			IsMeta           bool      `json:"isMeta"`
			IsSidechain      bool      `json:"isSidechain"`
			IsCompactSummary bool      `json:"isCompactSummary"`
			IsAPIError       bool      `json:"isApiErrorMessage"`
			Message          struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := dec.Decode(&rec); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if cursor {
			rec.Type = rec.Role
		}
		if (rec.Type != "user" && rec.Type != "assistant") || (!cursor && rec.Timestamp.Before(since)) ||
			rec.IsMeta || rec.IsSidechain || rec.IsAPIError {
			continue
		}
		var text string
		var blocks []block
		if json.Unmarshal(rec.Message.Content, &text) != nil {
			_ = json.Unmarshal(rec.Message.Content, &blocks)
		}
		if rec.Type == "user" {
			who := "Operator"
			if rec.IsCompactSummary {
				who = "Earlier conversation (compacted)"
			}
			for _, b := range blocks {
				if b.Type == "text" {
					text += b.Text
				}
			}
			// Cursor's JSONL has no per-message timestamps. The last handoff
			// marker separates this tenure from earlier conversation history.
			if cursor && !since.IsZero() && strings.HasPrefix(strings.TrimSpace(text), HandoffMark) {
				w.Reset()
				w.last = ""
			}
			if rec.IsCompactSummary || !syntheticClaudeText(text) {
				w.say(who, text)
			}
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				w.say(w.agent, b.Text)
			case "tool_use":
				w.tool(b.Name, toolInput(b.Input))
			}
		}
	}
}

func syntheticClaudeText(text string) bool {
	text = strings.TrimSpace(text)
	for _, tag := range []string{"<command-name>", "<command-message>", "<local-command-stdout>", "<local-command-caveat>", "<task-notification>"} {
		if strings.HasPrefix(text, tag) {
			return true
		}
	}
	return false
}

// toolInput is the gist of a tool call's input: a shell command, a file, or
// else the input itself.
func toolInput(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) == nil {
		for _, k := range []string{"command", "file_path", "path", "pattern", "url", "description"} {
			if s, ok := in[k].(string); ok {
				return s
			}
		}
	}
	return string(raw)
}

// exportCodex walks a Codex rollout. The conversation's clean text is in its
// event_msg records (user_message, agent_message); the tools it ran are its
// response_item function and custom tool calls.
func exportCodex(r io.Reader, since time.Time, w *exportWriter) error {
	dec := json.NewDecoder(bufio.NewReader(r))
	for {
		var rec struct {
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Payload   struct {
				Type      string `json:"type"`
				Message   string `json:"message"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Input     string `json:"input"`
			} `json:"payload"`
		}
		if err := dec.Decode(&rec); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if rec.Timestamp.Before(since) {
			continue
		}
		p := rec.Payload
		switch {
		case rec.Type == "event_msg" && p.Type == "user_message":
			w.say("Operator", p.Message)
		case rec.Type == "event_msg" && p.Type == "agent_message":
			w.say(w.agent, p.Message)
		case rec.Type == "response_item" && p.Type == "function_call":
			w.tool(p.Name, toolInput(json.RawMessage(p.Arguments)))
		case rec.Type == "response_item" && p.Type == "custom_tool_call":
			w.tool(p.Name, p.Input)
		}
	}
}
