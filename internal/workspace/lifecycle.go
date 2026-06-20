package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// SpawnBgPull fires `atelier-workspaces _bg-pull <repoPath> <defaultBranch>
// <windowID>` detached. Returns immediately; the pull + ahead/behind
// compute + option stamping happen in the background.
//
// Lives in the workspace primitive (not the workspaces tool) because
// EVERY workspace-lifecycle event that lands the user on a workspace
// should refresh freshness: restore, creator flows, sessions pick.
// Without that, the status-line freshness icon stays empty until the
// user happens to M-s into a workspace.
//
// No-op when any arg is empty (e.g. non-git workspace has no
// defaultBranch). Best-effort: a spawn failure is logged but never
// surfaced to the caller.
func SpawnBgPull(repoPath, defaultBranch, windowID string) {
	// Skip in e2e test contexts. The detached `git fetch origin`
	// subprocess holds files open under `.git/objects` after the
	// test function returns, racing with Go's t.TempDir() cleanup
	// (which then fails with "directory not empty"). Tests use
	// `ATELIER_TMUX_SOCKET=atelier-test-<random>` sockets via
	// testtmux.New; this prefix is the reliable test signal.
	//
	// Freshness data is purely cosmetic — its absence doesn't hide
	// real bugs in test runs. Production atelier (any other socket
	// name) is unaffected.
	if strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-") {
		debuglog.Logf("workspace.SpawnBgPull: SKIP (test socket) repo=%q",
			repoPath)
		return
	}
	if repoPath == "" || defaultBranch == "" || windowID == "" {
		debuglog.Logf("workspace.SpawnBgPull: SKIP (missing args) repo=%q branch=%q window=%q",
			repoPath, defaultBranch, windowID)
		return
	}
	self, err := os.Executable()
	if err != nil {
		self = "atelier-workspaces"
	}
	// If we're being called from a non-workspaces tool (claude,
	// k9s, etc.), os.Executable() returns THAT binary's path. The
	// _bg-pull subcommand lives on atelier-workspaces specifically,
	// so swap the basename when ours doesn't end in "atelier-workspaces".
	if !strings.HasSuffix(self, "/atelier-workspaces") && self != "atelier-workspaces" {
		// Replace the trailing component with atelier-workspaces.
		if i := strings.LastIndex(self, "/"); i >= 0 {
			self = self[:i+1] + "atelier-workspaces"
		} else {
			self = "atelier-workspaces"
		}
	}
	cmd := exec.Command(self, "_bg-pull", repoPath, defaultBranch, windowID)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// CRITICAL: put the child in its own process group. Without this,
	// the spawned _bg-pull inherits our process group; when the
	// invoking atelier-workspaces process exits (it returns immediately
	// after spawning — that's the FR-7 "snappy open" win), the tmux
	// popup pty closes, and the kernel sends SIGHUP to everything in
	// our process group, including our detached child. The child dies
	// before it can do the fetch + stamp the freshness options.
	// Setpgid: true breaks that — the child becomes its own pgroup
	// leader and survives the parent's exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		debuglog.LogErr("workspace.SpawnBgPull start", err)
		return
	}
	// Capture pid BEFORE Release — Go 1.25+ resets Process.Pid to -1
	// after Release(), which we discovered when an earlier version of
	// this log line printed pid=-1 misleadingly.
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	debuglog.Logf("workspace.SpawnBgPull: launched pid=%d repo=%s window=%s self=%s",
		pid, repoPath, windowID, self)
}

// OpenDefaultBranch is the single primitive for "land the outer
// client on the default-branch window of a repo". Used by:
//
//   - workspaces clone path (after cloning a fresh repo)
//   - workspaces empty-Enter path (auto + prompt flows)
//   - any future flow that needs to open a repo's main branch
//
// Owns the full sequence atomically:
//
//  1. EnsureSession with @repo_path stamped on the new session.
//  2. EnsureDefaultBranchWindow to make sure the named window
//     exists (no-op if already there from EnsureSession's auto
//     window-1 creation).
//  3. LandOuter so the outer (workspace) client switches to it.
//  4. SpawnBgPull so freshness data populates without the user
//     manually M-s'ing into the workspace.
//  5. RegisterCreatedWorkspace into the on-disk cache so restore
//     can rehydrate it.
//
// Before this primitive existed, callers in tools/workspaces.go
// inlined all five steps with subtle drift between callsites — the
// clone path forgot RegisterCreatedWorkspace for a while, the
// default-branch path forgot SpawnBgPull, etc. Single primitive
// kills that class of bug.
//
// EnsureDefaultBranchWindow is a callback because it lives in the
// `workspaces` tool package (depends on tool-specific git logic)
// and adding it to internal/workspace would create an import cycle.
// Callers pass it in.
func OpenDefaultBranch(
	h *tmuxhost.Client,
	session, repoPath, defaultBranch string,
	ensureDefaultBranchWindow func(h *tmuxhost.Client, session, repoPath, branch string) error,
) error {
	if _, err := EnsureSession(h, session, repoPath, defaultBranch); err != nil {
		return err
	}
	if ensureDefaultBranchWindow != nil {
		if err := ensureDefaultBranchWindow(h, session, repoPath, defaultBranch); err != nil {
			return err
		}
	}
	// LandOuter requires an attached client (switch-client errors
	// "no current client" otherwise). When called from contexts
	// without a client (restore-at-startup, headless tests, hooks
	// firing after the client departed), we want to continue with
	// the rest of the sequence — log and proceed, don't abort.
	// Popup contexts always have a client, so the error path still
	// surfaces real failures.
	if err := LandOuter(h, "="+session, "="+session+":"+defaultBranch); err != nil {
		debuglog.LogErr("workspace.OpenDefaultBranch: LandOuter (continuing)", err)
	}
	if defaultWid, _ := h.DisplayMessageAt(session+":"+defaultBranch, "#{window_id}"); defaultWid != "" {
		SpawnBgPull(repoPath, defaultBranch, defaultWid)
	}
	RegisterCreatedWorkspace(NewWorkspaceInfo{
		Session:    session,
		RepoPath:   repoPath,
		Kind:       "default-branch",
		WindowName: defaultBranch,
		Cwd:        repoPath,
		Branch:     defaultBranch,
	})
	return nil
}

// EnsureSession creates the workspace's tmux session if absent. The
// auto-created window-1 is pre-named to defaultBranch (e.g. "main") so
// the "open default branch" flow has a place to switch to. Stamps
// `@repo_path` on the session.
//
// Returns (created bool): callers that build a new worktree window can
// pass this to CreateWorktreeWindow's KillDefault field to nuke the
// auto-named default-branch window — the user asked for ONE worktree,
// showing an unrequested `main` row in the picker is confusing. The
// default-branch window is lazily recreated by EnsureDefaultBranchWindow
// when the user hits the empty-query → pull-default flow.
//
// Pre-extraction this was a closure copy-pasted across runWorkspaceName
// and runWorkspacePrompt with subtle drift between copies.
func EnsureSession(h *tmuxhost.Client, session, repoPath, defaultBranch string) (created bool, err error) {
	if has, _ := h.HasSession(session); has {
		return false, nil
	}
	if _, err := h.Run("new-session", "-d", "-s", session, "-c", repoPath); err != nil {
		return false, fmt.Errorf("workspace.EnsureSession new-session %s: %w", session, err)
	}
	if _, err := h.Run("rename-window", "-t", session+":1", defaultBranch); err != nil {
		debuglog.LogErr("workspace.EnsureSession rename-window", err)
	}
	if _, err := h.Run("set-option", "-t", session, OptRepoPath, repoPath); err != nil {
		debuglog.LogErr("workspace.EnsureSession set @repo_path", err)
	}
	return true, nil
}

// WorktreeWindowSpec is the input to CreateWorktreeWindow.
//
// Plugin-specific metadata to stamp on the new window goes through
// the generic Metadata map (`<plugin>.<field>` keys). Each entry is
// stamped as a tmux window option `@<plugin>_<field>` and persisted
// to the statestore cache for restore.
type WorktreeWindowSpec struct {
	Session    string // tmux session name (must already exist; call EnsureSession first)
	WtPath     string // worktree path; becomes the new window's cwd
	WindowName string // tmux window name (matches the branch name)

	// Metadata is plugin-namespaced window state to stamp + persist.
	// Empty/nil = no plugin metadata.
	Metadata map[string]string

	// Kind is the workspace shape: "worktree" (default, single-repo)
	// or "multi-repo". Recorded on the statestore Workspace entry.
	// Empty defaults to "worktree" in CreateWorktreeWindow.
	Kind string

	// KillDefaultBranch: if non-empty, kill the window with this name
	// in the session AFTER creating the new one. Used after EnsureSession
	// returned created=true — the auto-created default-branch window
	// gets removed so the picker only shows what the user actually built.
	// Passing the empty string leaves all existing windows alone.
	KillDefaultBranch string
}

// CreateWorktreeWindow adds a new tmux window in `spec.Session` at
// `spec.WtPath` named `spec.WindowName`, stamps Claude metadata on it,
// optionally kills the default-branch window if it was auto-created,
// and registers the workspace in the persisted statestore cache.
//
// Returns the new window's @ID. Targeting subsequent operations by @ID
// instead of by name avoids tmux's `/`-in-name ambiguity (claude branch
// names look like "feat/add-foo" — tmux silently fails to resolve
// `=session:feat/add-foo`).
//
// Pre-extraction this was inline `new-window -P -F ... set-option @claude_prompt
// ... set-option @claude_workspace_kind ... kill-window` in 3 sites in
// workspaces.go. The KillDefaultBranch detail in particular was a
// scattered "if created { ... }" idiom that's easy to miss when adding
// a fourth creation flow.
func CreateWorktreeWindow(h *tmuxhost.Client, spec WorktreeWindowSpec) (windowID string, err error) {
	last := lastWindowIndex(h, spec.Session)
	next := last + 1
	newWidBytes, _ := h.Run("new-window", "-P", "-F", "#{window_id}",
		"-t", fmt.Sprintf("%s:%d", spec.Session, next),
		"-c", spec.WtPath, "-n", spec.WindowName)
	windowID = strings.TrimSpace(string(newWidBytes))
	if windowID == "" {
		return "", fmt.Errorf("workspace.CreateWorktreeWindow: new-window returned no window_id (name=%s wtPath=%s)",
			spec.WindowName, spec.WtPath)
	}
	// Stamp every plugin-namespaced metadata entry as a tmux window
	// option using the canonical `<plugin>.<field>` → `@<plugin>_<field>`
	// translation. Core never inspects the key contents; plugins own
	// their namespaces.
	for key, value := range spec.Metadata {
		if value == "" {
			continue
		}
		_, _ = h.Run("set-option", "-w", "-t", windowID,
			statestore.MetadataKeyToOptionName(key), value)
	}
	if spec.KillDefaultBranch != "" {
		_, _ = h.Run("kill-window", "-t", "="+spec.Session+":"+spec.KillDefaultBranch)
	}

	// Mirror the new workspace + window into the on-disk cache so
	// restore can reconstruct it after a tmux server restart.
	kind := spec.Kind
	if kind == "" {
		kind = "worktree"
	}
	repoPathBytes, _ := h.Run("show-option", "-t", spec.Session, "-qv", OptRepoPath)
	repoPath := strings.TrimSpace(string(repoPathBytes))
	RegisterCreatedWorkspace(NewWorkspaceInfo{
		Session:    spec.Session,
		RepoPath:   repoPath,
		Kind:       kind,
		WindowName: spec.WindowName,
		Cwd:        spec.WtPath,
		Branch:     spec.WindowName,
		Metadata:   spec.Metadata,
	})
	// Fire bg-pull so the new window's freshness icon populates
	// without the user having to M-s back into it. Worktree
	// workspaces only — multi-repo lacks a single default branch.
	if repoPath != "" && kind != "multi-repo" {
		if defaultBranch, err := computeDefaultBranch(repoPath); err == nil && defaultBranch != "" {
			SpawnBgPull(repoPath, defaultBranch, windowID)
		}
	}
	return windowID, nil
}

// LandOuter brings the outer (workspace) client onto a target session
// (and optionally a specific window within it).
//
// Target arguments are tmux target strings — either `=session` /
// `=session:name` form, or a raw `@<id>` from `new-window -P -F
// '#{window_id}'`. Either may be empty; the empty side is skipped.
//
// Reads @atelier_outer_client to target the right client by name; falls
// back to a bare switch-client if absent. Without -c outer, a bare
// switch-client from inside a popup pty would switch the POPUP-client
// (rendering the workspace inside the popup) — this was the M-; →
// Select Workspace → opens-inside-Claude-popup bug.
//
// Order matters: select-window FIRST sets the session's current window
// (select-window does NOT accept -c). switch-client -c <outer> -t
// =<session> THEN attaches the outer client to that session, which
// displays the window we just set. NEVER plain attach — that creates
// a parallel client.
func LandOuter(h *tmuxhost.Client, sessionTarget, windowTarget string) error {
	if windowTarget != "" {
		if _, err := h.Run("select-window", "-t", windowTarget); err != nil {
			return fmt.Errorf("workspace.LandOuter select-window %q: %w", windowTarget, err)
		}
	}
	if sessionTarget == "" {
		return nil
	}
	outerClient, _ := h.ShowGlobalOption("@atelier_outer_client")
	debuglog.Logf("workspace.LandOuter: session=%q window=%q outer=%q",
		sessionTarget, windowTarget, outerClient)
	args := []string{"switch-client"}
	if outerClient != "" {
		args = append(args, "-c", outerClient)
	}
	args = append(args, "-t", sessionTarget)
	if _, err := h.Run(args...); err != nil {
		return fmt.Errorf("workspace.LandOuter switch-client: %w", err)
	}
	// Dismiss popup-clients for OTHER workspaces. Without this, a
	// Claude/k9s/etc. popup the user had open on workspace A stays
	// visually on top after they M-s to workspace B (the popup lives
	// on its own popup-pty client; switch-client on the outer client
	// doesn't touch it). User then sees the wrong workspace's tool.
	//
	// Same-workspace popups are kept — picking the workspace you're
	// already on shouldn't dismiss your live Claude session.
	detachStalePopups(h, sessionTarget, windowTarget)
	return nil
}

// detachStalePopups closes popup overlays whose backing session is
// scoped to a DIFFERENT (session_id, window_id) than the target the
// user just landed on. Popup-backing session names are
// `_atelier_<tool>_<sid_digits>_<wid_digits>` — we keep the suffix
// matching the target and detach everything else's client(s).
//
// Detaches are dispatched via `run-shell -b` (deferred / background)
// so we don't accidentally kill our own popup pty (which is one of
// these clients) mid-call: by the time the deferred command runs,
// we've returned and our popup has closed naturally.
func detachStalePopups(h *tmuxhost.Client, sessionTarget, windowTarget string) {
	keepSidWid := resolveSidWidDigits(h, sessionTarget, windowTarget)
	out, err := h.Run("list-clients", "-F", "#{client_session}|#{client_name}")
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		clientSession := parts[0]
		clientName := parts[1]
		if !shouldDetachPopupClient(clientSession, keepSidWid) {
			continue
		}
		debuglog.Logf("workspace.LandOuter: detaching stale popup client=%q session=%q",
			clientName, clientSession)
		_, _ = h.Run("run-shell", "-b", "tmux detach-client -t "+clientName)
	}
}

// shouldDetachPopupClient decides whether a client attached to a
// given session should be detached on a workspace switch. Pure
// function — extracted for unit-testability without spinning up
// tmux.
//
// Rules:
//   - Only `_atelier_*` popup-backing sessions are candidates;
//     foreign sessions are left alone.
//   - When keepSidWid is non-empty AND the popup session name ends
//     with `_<keepSidWid>`, the popup is scoped to the workspace
//     we're landing on — preserve it.
//   - All other atelier popups are stale (scoped to a different
//     workspace) and get detached.
//   - Empty keepSidWid (failed resolution) → detach everything.
//     Safer to over-clean than to leave a popup of unknown scope.
func shouldDetachPopupClient(clientSession, keepSidWid string) bool {
	if !strings.HasPrefix(clientSession, "_atelier_") {
		return false
	}
	if keepSidWid == "" {
		return true
	}
	return !strings.HasSuffix(clientSession, "_"+keepSidWid)
}

// resolveSidWidDigits returns "<sidDigits>_<widDigits>" for the
// target session+window so detachStalePopups can compare against
// popup-backing session names. Returns "" if either lookup fails —
// in which case detachStalePopups falls back to detaching ALL
// popups (safer than detaching the wrong ones).
func resolveSidWidDigits(h *tmuxhost.Client, sessionTarget, windowTarget string) string {
	if sessionTarget == "" {
		return ""
	}
	// `display-message -t =sess` silently returns empty; tmux only
	// accepts the `=` exact-match form for certain commands
	// (switch-client, kill-session). For display-message we strip it.
	sid, err := h.DisplayMessageAt(stripEqualsPrefix(sessionTarget), "#{session_id}")
	if err != nil || sid == "" {
		return ""
	}
	wt := windowTarget
	if wt == "" {
		wt = sessionTarget
	}
	wid, err := h.DisplayMessageAt(stripEqualsPrefix(wt), "#{window_id}")
	if err != nil || wid == "" {
		return ""
	}
	return digitsOnly(sid) + "_" + digitsOnly(wid)
}

func stripEqualsPrefix(s string) string {
	if strings.HasPrefix(s, "=") {
		return s[1:]
	}
	return s
}

func digitsOnly(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// lastWindowIndex returns the highest window index in `session`.
// Replaces the lastWindowIndex helper that was inline in workspaces.go.
func lastWindowIndex(h *tmuxhost.Client, session string) int {
	out, err := h.Run("list-windows", "-t", "="+session, "-F", "#I")
	if err != nil {
		return 0
	}
	last := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n > last {
			last = n
		}
	}
	return last
}
