// Package claude is atelier's AIIntegration adapter for Claude Code. It
// satisfies the kernel's integration.AIIntegration port: it opens Claude in
// a workspace popup, generates branch names from a kernel-supplied naming
// instruction, handles Claude's Stop hook (flagging attention + refreshing
// the summary via the kernel's workspace verbs), and installs the Stop-hook
// wiring. Everything Claude-specific — resume semantics, project encoding,
// hook payload shape, the `--settings`/`--append-system-prompt` flags —
// lives here, behind the port. Swap Claude for codex/gemini by writing
// another adapter and selecting it in config; the kernel does not change.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/vyrwu/atelier/internal/adapters/claude/claudegen"
	"github.com/vyrwu/atelier/internal/adapters/claude/claudeproj"
	"github.com/vyrwu/atelier/internal/adapters/claude/claudesettings"
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

		// One-shot: consume the prompt + kind so they can't be replayed.
		// @ai_active_session_id stays — the persistent conversation pointer.
		clearLaunchPrompt(h, ctx.WindowID, prompt, kind)

		cfg, _ := LoadConfig()
		settingsPath, settingsErr := claudesettings.Ensure()
		if settingsErr != nil {
			debuglog.LogErr("claudesettings.Ensure", settingsErr)
		}
		return buildClaudeStartCmd(prompt, kind, cfg.MultiRepoSystemPrompt, settingsPath, resumeID), nil
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

// clearLaunchPrompt consumes the one-shot @ai_prompt / @ai_workspace_kind
// after OpenAgent has folded them into the launch command — from BOTH the
// live window AND the restore cache. Clearing only the window option (the old
// behavior) let a spent prompt survive in the statestore cache and get
// re-stamped on the next tmux server restart; a restored window then carried
// both a dead prompt and a live @ai_active_session_id, and buildClaudeStartCmd
// forked a fresh session off the prompt instead of resuming. Best-effort: the
// cache mirror is only cleared when there was actually something to clear so a
// plain resume doesn't leave empty keys behind.
func clearLaunchPrompt(h *tmuxhost.Client, windowID, prompt, kind string) {
	_ = h.UnsetWindowOption(windowID, OptPrompt)
	_ = h.UnsetWindowOption(windowID, OptWorkspaceKind)
	if prompt != "" {
		_ = workspace.PersistWindowMetadata(h, windowID, MetaPrompt, "")
	}
	if kind != "" {
		_ = workspace.PersistWindowMetadata(h, windowID, MetaWorkspaceKind, "")
	}
}

// GenerateName runs Claude with a kernel-supplied naming instruction and
// returns the model's raw output (trailing newlines trimmed). The kernel
// owns the instruction, the line contract, and validation — a single-line
// contract yields one line; the tag-aware contract yields two (name, then
// tag). This just runs the model. (Was workspaces' inline claudegen.)
func (Adapter) GenerateName(_ context.Context, systemPrompt, intent string) (string, error) {
	gen := claudegen.New()
	gen.Model = "sonnet"
	raw, err := gen.RunWithSystemPrompt(systemPrompt, intent)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(raw, "\r\n"), nil
}

// OnStop handles Claude's Stop hook: resolve the target outer window, flag
// attention (unless the popup is currently attached), track the resume
// session id from the transcript path, and spawn a detached summary refresh
// so the hook returns instantly. Uses the kernel verb workspace.SetAttention.
// (Was `notify-attention`.)
func (Adapter) OnStop(h *tmuxhost.Client, windowID string, payload []byte) error {
	session, _ := h.DisplayMessage("#{session_name}")
	attached := "0"
	target := windowID

	if t := targetFromAgentSession(session); t != "" {
		attached, _ = h.DisplayMessageAt(session, "#{session_attached}")
		target = t
	} else if target == "" {
		if v := os.Getenv("TMUX_PARENT_WINDOW"); v != "" {
			target = v
		} else if v := os.Getenv("TMUX_PARENT_WINDOW_ID"); v != "" {
			target = v
		} else if v, _ := h.ShowGlobalOption("@atelier_outer_window"); v != "" {
			target = v
		} else if v, err := h.DisplayMessage("#{window_id}"); err == nil {
			target = v
		}
	}
	if target == "" {
		return fmt.Errorf("claude.OnStop: could not resolve target window")
	}
	target = ensurePrefix(target, "@")
	debuglog.Logf("claude.OnStop: session=%q attached=%q target=%q", session, attached, target)

	if attached == "" || attached == "0" {
		_ = workspace.SetAttention(h, target, true)
	}

	// No hook payload → attention only (matches the old notify-attention:
	// no transcript to track a resume id from, no recap to generate).
	if len(payload) == 0 {
		return nil
	}
	var probe struct {
		TranscriptPath string `json:"transcript_path"`
	}
	if err := json.Unmarshal(payload, &probe); err == nil && probe.TranscriptPath != "" {
		if sid := claudeSessionIDFromPath(probe.TranscriptPath); sid != "" {
			_ = h.SetWindowOption(target, OptActiveSessionID, sid)
			_ = workspace.PersistWindowMetadata(h, target, MetaActiveSessionID, sid)
		}
	}

	// Detached summary refresh so the Stop hook returns instantly.
	self, err := os.Executable()
	if err != nil {
		self = "atelier"
	}
	cmd := exec.Command(self, "ai", "recap", "--window", target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// targetFromAgentSession derives the outer workspace window id (`@N`) from a
// Claude popup backing-session name (`_atelier_claude_<sid>_<wid>` or the
// legacy `_claudepop_<sid>_<wid>`). Returns "" when session is not an agent
// popup. Pure — unit-tested.
func targetFromAgentSession(session string) string {
	if !strings.HasPrefix(session, "_atelier_claude_") && !strings.HasPrefix(session, "_claudepop_") {
		return ""
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(session, "_atelier_claude_"), "_claudepop_")
	if i := strings.LastIndex(rest, "_"); i >= 0 {
		return "@" + rest[i+1:]
	}
	return ""
}

// Summarize refreshes the workspace summary for windowID from the latest
// Claude transcript under projectDir (resolved from the workspace when
// empty), via the kernel verb workspace.SetRecap. (Was `recap`.)
func (Adapter) Summarize(h *tmuxhost.Client, windowID, projectDir string) error {
	if windowID == "" {
		return fmt.Errorf("claude.Summarize: windowID required")
	}
	if projectDir == "" {
		// Resolve cwd from the TARGET window (@N, a real workspace), NOT the
		// current pane: OnStop spawns `atelier ai recap` from inside the
		// agent popup, whose pane is an atelier popup session that
		// workspace.Info rejects. Using windowID targets the outer workspace.
		if w, err := workspace.Info(h, windowID); err == nil {
			projectDir = w.Cwd
		}
	}
	recap, err := latestRecap(projectDir)
	if err != nil || recap == "" {
		return err
	}
	return workspace.SetRecap(h, windowID, recap)
}

// EnsureHooks installs atelier's Claude settings (the Stop hook that routes
// to `atelier ai on-stop`). Idempotent. Called by OpenAgent and available to
// doctor.
func (Adapter) EnsureHooks() error {
	_, err := claudesettings.Ensure()
	return err
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
func claudeSessionIDFromPath(p string) string { return claudeproj.SessionIDFromPath(p) }
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

func tailNLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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

// latestRecap finds the most-recent Claude transcript for projectDir and asks
// Claude to summarize it. Returns "" if no transcript.
func latestRecap(projectDir string) (string, error) {
	transcript, err := claudeproj.LatestTranscriptPath(projectDir)
	if err != nil || transcript == "" {
		return "", err
	}
	cfg, _ := LoadConfig()
	gen := claudegen.New()
	if cfg.RecapModel != "" {
		gen.Model = cfg.RecapModel
	}
	if data, err := os.ReadFile(transcript); err == nil {
		ctx := tailNLines(string(data), 100)
		out, err := gen.RunWithSystemPrompt(cfg.RecapSystemPrompt, ctx)
		if err != nil {
			return "", err
		}
		return truncateLine(out, RecapMaxRunes), nil
	}
	return gen.RecapFromTranscript(transcript)
}
