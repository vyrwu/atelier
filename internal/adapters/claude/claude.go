// Package claude is atelier's AIIntegration adapter for Claude Code. It
// satisfies the kernel's integration.AIIntegration port: it opens Claude in
// a workspace popup, generates branch names from a kernel-supplied naming
// instruction, and re-derives the workspace recap + attention verdict by
// reading the agent's transcript (RefreshRecap, pulled by the refresh loop).
// Everything Claude-specific — resume semantics, project encoding, the
// `--settings`/`--append-system-prompt` flags — lives here, behind the port.
// Swap Claude for codex/gemini by writing another adapter and selecting it in
// config; the kernel does not change. Note: atelier installs NO hook into
// Claude's own config — recap + attention are pull-only.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/adapters/claude/claudegen"
	"github.com/vyrwu/atelier/internal/adapters/claude/claudeproj"
	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// Metadata keys + their derived tmux window option names. The adapter owns
// its `ai.*` metadata namespace; the kernel never inspects these keys. Each
// `Meta*` is the canonical metadata key; each `Opt*` is its tmux option name.
const (
	MetaPrompt          = "ai.prompt"
	MetaWorkspaceKind   = "ai.workspace_kind"
	MetaActiveSessionID = "ai.active_session_id"

	WorkspaceKindMultiRepo = "multi-repo"
)

// Tmux option names — derived via the canonical dots→underscores translation.
var (
	OptPrompt          = statestore.MetadataKeyToOptionName(MetaPrompt)
	OptWorkspaceKind   = statestore.MetadataKeyToOptionName(MetaWorkspaceKind)
	OptActiveSessionID = statestore.MetadataKeyToOptionName(MetaActiveSessionID)
)

// Spec is the workspace-scoped popup spec for the Claude backing session.
var Spec = &popup.WorkspaceScoped{
	Tool:        "claude",
	DefaultCmd:  "claude",
	Description: "Per-window Claude Code popup",
}

// Adapter satisfies integration.AIIntegration for Claude Code.
type Adapter struct{}

// New constructs the Claude AI adapter.
func New() *Adapter { return &Adapter{} }

var _ integration.AIIntegration = (*Adapter)(nil)

// Name identifies the adapter.
func (Adapter) Name() string { return "claude" }

// DisplayName is the user-facing product name shown in the tool selector.
func (Adapter) DisplayName() string { return "Claude Code" }

// OpenAgent opens the Claude popup for the current workspace. Reads
// @ai_prompt / @ai_workspace_kind (one-shot) + @ai_active_session_id
// (durable resume pointer) off the parent window, ensures atelier's Claude
// settings exist, builds the launch command, and delegates to the kernel's
// popup lifecycle. (Was `atelier tools claude open`.)
func (Adapter) OpenAgent(h *tmuxhost.Client) error {
	return popup.OpenWorkspaceScopedWithCmd(h, Spec, func(ctx popup.ParentContext) (string, error) {
		debuglog.Logf("claude.OpenAgent: parentSession=%q parentWindow=%q",
			ctx.SessionID, ctx.WindowID)

		prompt, _ := h.GetWindowOption(ctx.WindowID, OptPrompt)
		kind, _ := h.GetWindowOption(ctx.WindowID, OptWorkspaceKind)
		storedID, _ := h.GetWindowOption(ctx.WindowID, OptActiveSessionID)
		resumeID := resumeIDForLaunch(storedID)
		if storedID != "" && resumeID == "" {
			debuglog.Logf("claude.OpenAgent: transcript for @ai_active_session_id=%q not found — fresh this launch, id preserved", storedID)
		}
		if resumeID == "" && prompt == "" && ctx.Cwd != "" {
			if id := latestSessionIDForCwd(ctx.Cwd); id != "" {
				debuglog.Logf("claude.OpenAgent: no tracked id; resuming latest transcript for cwd=%q id=%q", ctx.Cwd, id)
				resumeID = id
				_ = h.SetWindowOption(ctx.WindowID, OptActiveSessionID, id)
				_ = workspace.PersistWindowMetadata(h, ctx.WindowID, MetaActiveSessionID, id)
			}
		}

		// One-shot: consume the prompt so it can't be replayed.
		// @ai_workspace_kind is DURABLE (the picker's only signal for
		// multi-repo workspaces) and @ai_active_session_id stays too — the
		// persistent conversation pointer.
		clearLaunchPrompt(h, ctx.WindowID, prompt)

		cfg, _ := LoadConfig()
		// No --settings injection: atelier no longer installs a Stop hook into
		// Claude's config. The recap + attention verdict are pulled by the
		// refresh loop reading the transcript (RefreshRecap), so Claude launches
		// with the user's own untouched config.
		return buildClaudeStartCmd(prompt, kind, cfg.Prompts.MultiRepo, "", resumeID), nil
	})
}

// SetPrompt queues the initial prompt (and optional workspace kind) for the
// next OpenAgent on windowID. Empty prompt clears it. (Was `set-prompt`.)
func (Adapter) SetPrompt(h *tmuxhost.Client, windowID, prompt, kind string) error {
	if windowID == "" {
		return fmt.Errorf("claude.SetPrompt: windowID required")
	}
	if prompt == "" {
		if err := h.UnsetWindowOption(windowID, OptPrompt); err != nil {
			return err
		}
	} else if err := h.SetWindowOption(windowID, OptPrompt, prompt); err != nil {
		return err
	}
	if kind != "" {
		if err := h.SetWindowOption(windowID, OptWorkspaceKind, kind); err != nil {
			return err
		}
	}
	return nil
}

// clearLaunchPrompt consumes the one-shot @ai_prompt after OpenAgent has
// folded it into the launch command — from BOTH the live window AND the
// restore cache. Clearing only the window option (the old behavior) let a
// spent prompt survive in the statestore cache and get re-stamped on the next
// tmux server restart; a restored window then carried both a dead prompt and a
// live @ai_active_session_id, and buildClaudeStartCmd forked a fresh session
// off the prompt instead of resuming. Best-effort: the cache mirror is only
// cleared when there was actually something to clear so a plain resume doesn't
// leave empty keys behind.
//
// @ai_workspace_kind is deliberately NOT cleared. Unlike the prompt it is
// durable: it's the M-s picker's ONLY signal that a repo-less multi-repo
// workspace is a workspace at all (those have no @repo_path). Clearing it on
// launch made every multi-repo workspace vanish from the picker after its
// first Claude launch, while the later Stop-hook @needs_attention flag still
// inflated the status-line rollup — a phantom notification with no workspace.
func clearLaunchPrompt(h *tmuxhost.Client, windowID, prompt string) {
	_ = h.UnsetWindowOption(windowID, OptPrompt)
	if prompt != "" {
		_ = workspace.PersistWindowMetadata(h, windowID, MetaPrompt, "")
	}
}

// GenerateName runs Claude with a kernel-supplied naming instruction and
// returns the model's raw output (trailing newlines trimmed). The kernel
// owns the instruction, the line contract, and validation — a single-line
// contract yields one line; the tag-aware contract yields two (name, then
// tag). This just runs the model. (Was workspaces' inline claudegen.)
func (Adapter) GenerateName(_ context.Context, systemPrompt, intent string) (string, error) {
	cfg, _ := LoadConfig()
	gen := claudegen.New()
	gen.Model = cfg.NamingModel()
	raw, err := gen.RunWithSystemPrompt(systemPrompt, intent)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(raw, "\r\n"), nil
}

// RefreshRecap re-derives the workspace's recap AND attention verdict for
// windowID from the agent's latest session transcript under cwd (the worktree),
// then writes both via the kernel verbs SetRecap / SetAgentStatus.
//
// The 3-state agent status comes from two signals, not just "did the transcript
// grow". Growth alone is a trap: an agent finishing its turn and handing results
// back to the user is ALSO a write, so treating every write as "running" pins an
// idle/waiting workspace to the blue dot forever. Instead:
//
//   - running: the transcript tail shows a tool IN FLIGHT (a dispatched
//     tool_use with no result yet, or a tool_result the agent hasn't replied
//     to). This is deterministic — the shape of the last event, gated on
//     recency so a frozen mid-tool state from a dead process doesn't linger.
//   - blocked/idle: when no tool is in flight the turn has been handed back, so
//     the haiku verdict decides — blocked = waiting on the user (asked a
//     question / needs a decision), idle = finished cleanly or delegated.
//
// So:
//   - tool in flight (any tick)     → running
//   - progressed, turn handed back  → haiku verdict (blocked | idle)
//   - no new content, blocked+unseen → stays blocked until the user visits
//   - no new content, otherwise      → idle
//
// It is a PULL — the refresh loop calls it every tick; the haiku call is
// throttled to actual progression, so a quiet workspace only re-evaluates the
// cheap running↔idle transition. No-op when the workspace has no agent
// transcript.
func (Adapter) RefreshRecap(h *tmuxhost.Client, windowID, cwd string) error {
	if windowID == "" || cwd == "" {
		return nil
	}
	transcript, _ := claudeproj.LatestTranscriptPath(cwd)
	if transcript == "" {
		return nil // no agent session for this workspace
	}
	fi, err := os.Stat(transcript)
	if err != nil {
		return nil
	}
	mtime := fi.ModTime().Unix()
	active := time.Now().Unix()-mtime < int64(runningWindow.Seconds())
	// Deterministic "running": the tail shows a tool in flight. A completed turn
	// ends with an assistant text block; a trailing tool_use/tool_result (or
	// thinking) means work is still going. Gated on recency so a mid-tool state
	// frozen by a dead process settles to idle instead of reading as running.
	working := active && agentActivelyWorking(transcript)

	progressed := true
	if prev, _ := h.GetWindowOption(windowID, workspace.OptRecapTs); strings.TrimSpace(prev) != "" {
		if ts, e := strconv.ParseInt(strings.TrimSpace(prev), 10, 64); e == nil && mtime <= ts {
			progressed = false
		}
	}

	if !progressed {
		// No new transcript content since the last summary — skip the model call.
		// A tool in flight is running; otherwise a workspace already flagged
		// blocked stays blocked until the user visits (after-select-window clears
		// @needs_attention — re-setting here would re-raise it); anything else has
		// handed its turn back and is idle.
		if working {
			return setAgentStatusIfChanged(h, windowID, workspace.AgentRunning)
		}
		if att, _ := h.GetWindowOption(windowID, workspace.OptAttention); strings.TrimSpace(att) == "1" {
			return nil
		}
		return setAgentStatusIfChanged(h, windowID, workspace.AgentIdle)
	}

	// Progressed → re-summarize and classify. Trust the haiku 3-way verdict for
	// the handoff case: a turn that merely wrote its results back to the user is
	// NOT running just because it wrote — that write IS the handoff. Only a tool
	// in flight (deterministic) forces running over the verdict.
	cfg, _ := LoadConfig()
	input := buildRecapInput(dumpTranscript(transcript), worktreeDelta(cwd))
	if input == "" {
		return nil
	}
	gen := claudegen.New()
	gen.Model = cfg.RecapModel()
	out, err := gen.RunWithSystemPrompt(cfg.Prompts.Recap, input)
	if err != nil {
		return err
	}
	recap, verdict := parseRecapVerdict(out)
	if recap != "" {
		// Stamp keyed on the transcript mtime so the throttle above sees "already
		// summarized this state".
		if err := workspace.SetRecapTS(h, windowID, recap, mtime); err != nil {
			return err
		}
	}
	status := verdict
	if working {
		status = workspace.AgentRunning
	}
	return workspace.SetAgentStatus(h, windowID, status)
}

// agentActivelyWorking reports whether the transcript tail shows the agent
// mid-execution rather than a turn handed back to the user. It scans from the
// newest event: an assistant message whose last content block is a tool_use (or
// thinking) is work in flight; a user tool_result is a finished tool the agent
// hasn't yet replied to (still working); an assistant message ending in text is
// a completed turn (not working); a plain human message is not the agent
// working. The model can't see mid-execution in a static snapshot — the shape
// of the last event can. Best-effort: any parse trouble yields false.
func agentActivelyWorking(transcriptPath string) bool {
	for _, line := range lastTranscriptLines(transcriptPath, 40) {
		var e struct {
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Message.Role {
		case "assistant":
			var blocks []struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(e.Message.Content, &blocks) == nil && len(blocks) > 0 {
				return blocks[len(blocks)-1].Type != "text"
			}
			return true
		case "user":
			var blocks []struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(e.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "tool_result" {
						return true // tool finished, agent's reply pending
					}
				}
			}
			return false // plain human message → the agent isn't working
		}
	}
	return false
}

// lastTranscriptLines returns up to n newest non-empty lines of a JSONL
// transcript, newest first, reading only a trailing window so a multi-MB file
// stays cheap to poll every tick. Nil on any read error.
func lastTranscriptLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	const window = 128 << 10
	off := int64(0)
	if fi.Size() > window {
		off = fi.Size() - window
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	raw := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	out := make([]string, 0, n)
	for i := len(raw) - 1; i >= 0 && len(out) < n; i-- {
		if s := strings.TrimSpace(raw[i]); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// setAgentStatusIfChanged writes the status only when it actually differs from
// the current one, so the every-tick running↔idle re-evaluation doesn't churn
// window options (or the picker's change signature) on quiet workspaces.
func setAgentStatusIfChanged(h *tmuxhost.Client, windowID, status string) error {
	cur, _ := h.GetWindowOption(windowID, workspace.OptAgentStatus)
	cur = strings.TrimSpace(cur)
	if cur == status || (status == workspace.AgentIdle && cur == "") {
		return nil
	}
	return workspace.SetAgentStatus(h, windowID, status)
}

// parseRecapVerdict splits the summarizer's two-part output into the recap line
// and the 3-state agent status. The model emits an `ATTENTION: blocked|running|idle`
// line plus the recap. Defensive: an unrecognized/missing verdict means idle,
// and the first non-verdict line is the recap. Pure.
func parseRecapVerdict(raw string) (recap, status string) {
	status = workspace.AgentIdle
	for _, ln := range strings.Split(strings.TrimSpace(raw), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if len(t) >= len(attentionTag) && strings.EqualFold(t[:len(attentionTag)], attentionTag) {
			switch strings.ToLower(firstWord(t[len(attentionTag):])) {
			case workspace.AgentBlocked:
				status = workspace.AgentBlocked
			case workspace.AgentRunning:
				status = workspace.AgentRunning
			default:
				status = workspace.AgentIdle
			}
			continue
		}
		if recap == "" {
			recap = t
		}
	}
	return truncateLine(recap, RecapMaxRunes), status
}

const attentionTag = "ATTENTION:"

func firstWord(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), " ", 2)[0])
}

// dumpTranscript reads the tail of a transcript file, bounded to a token budget
// (maxTranscriptRunes). The tail carries the latest turns — the part that
// decides both the recap and the attention verdict. Empty on read error.
func dumpTranscript(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimRight(string(data), "\n")
	r := []rune(s)
	if len(r) > maxTranscriptRunes {
		s = string(r[len(r)-maxTranscriptRunes:])
		// The rune cut can land mid-line. Drop the partial leading line so the
		// dump starts at a whole JSONL object — otherwise the tail can begin
		// with '-' (a session-id fragment, etc.), which the `claude` CLI parses
		// as an unknown flag, failing every summary call.
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s
}

// AgentPopupSession returns the Claude popup-session name for a parent
// workspace window (`_atelier_claude_<sid>_<wid>`).
func (Adapter) AgentPopupSession(parentSessionID, parentWindowID string) string {
	return Spec.SessionName(parentSessionID, parentWindowID)
}

// HasResumableState reports whether Claude has a resumable conversation for
// the window/worktree: a tracked @ai_active_session_id, or a transcript on
// disk for the cwd (a soft-close prunes the tracked id, so the on-disk check
// is the fallback that lets the first recover-after-delete resume).
func (Adapter) HasResumableState(h *tmuxhost.Client, wid, cwd string) bool {
	if wid != "" {
		if id, _ := h.GetWindowOption(wid, OptActiveSessionID); strings.TrimSpace(id) != "" {
			return true
		}
	}
	return cwd != "" && latestSessionIDForCwd(cwd) != ""
}

// buildClaudeStartCmd assembles the claude command line:
//
//	resume             : claude --settings <atelier.json> --resume <session-id>
//	multi-repo + prompt: claude --settings <atelier.json> --append-system-prompt <sys> <prompt>
//	worktree   + prompt: claude --settings <atelier.json> <prompt>
//	no prompt          : claude --settings <atelier.json>
//
// A validated resumeSessionID (its transcript exists — resumeIDForLaunch
// already checked) takes precedence over any prompt still stamped on the
// window. That conversation already exists and already received its initial
// prompt; replaying the prompt would fork a fresh session and orphan the
// history. This is what made respawned workspaces start over instead of
// resuming: restore re-stamps the one-shot @ai_prompt from the cache, so a
// restored window carries BOTH a spent prompt and a live session id, and the
// resume must win.
func buildClaudeStartCmd(prompt, kind, multiRepoSys, settingsPath, resumeSessionID string) string {
	settings := ""
	if settingsPath != "" {
		settings = "--settings " + shellQuote(settingsPath) + " "
	}
	if resumeSessionID != "" {
		return "claude " + settings + "--resume " + shellQuote(resumeSessionID)
	}
	if prompt == "" {
		return "claude " + settings
	}
	if kind == WorkspaceKindMultiRepo {
		return fmt.Sprintf("claude %s--append-system-prompt %s %s",
			settings, shellQuote(multiRepoSys), shellQuote(prompt))
	}
	return fmt.Sprintf("claude %s%s", settings, shellQuote(prompt))
}

// Thin delegators to the shared claudeproj package.
func transcriptExists(id string) bool         { return claudeproj.TranscriptExists(id) }
func latestSessionIDForCwd(cwd string) string { return claudeproj.LatestSessionID(cwd) }

// resumeIDForLaunch decides the id to pass to `claude --resume`. Returns ""
// (fresh) when there's no stored id or no transcript for it. PURE +
// non-mutating: a missing transcript is often a false negative; skipping
// --resume for one launch is recoverable, erasing the id is not.
func resumeIDForLaunch(storedID string) string {
	if storedID == "" || !transcriptExists(storedID) {
		return ""
	}
	return storedID
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func ensurePrefix(s, prefix string) string {
	if s == "" || strings.HasPrefix(s, prefix) {
		return s
	}
	return prefix + s
}

// truncateLine returns a single-line, length-bounded recap.
func truncateLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.Trim(s, `"'“”‘’`)
	for _, prefix := range []string{"Recap:", "Summary:", "recap:", "summary:"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

// buildRecapInput assembles the summarizer input from the conversation tail and
// the worktree delta, labeling each so the model can weight actual code changes
// over what the chat merely discussed. Either part may be empty.
func buildRecapInput(transcriptTail, delta string) string {
	switch {
	case transcriptTail == "" && delta == "":
		return ""
	case delta == "":
		return transcriptTail
	case transcriptTail == "":
		return "=== Workspace code changes (git delta) ===\n" + delta
	default:
		return transcriptTail +
			"\n\n=== Workspace code changes (git delta) ===\n" + delta
	}
}
