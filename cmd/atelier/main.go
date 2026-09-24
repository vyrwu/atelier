// Command atelier is the whole tool: the UI overlay, the tiny CLI the hooks and
// status line call, and the MCP server — one binary (NFR-S2). It runs its
// sessions on a dedicated tmux socket so it never disturbs your main tmux.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/mcp"
	"github.com/vyrwu/atelier/internal/tmux"
	"github.com/vyrwu/atelier/internal/ui"
)

// version is stamped by goreleaser at release time (-X main.version).
var version = "dev"

func main() {
	core.LoadConfig() // applies ATELIER_ROOT; harmless everywhere
	ui.Version = version
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	if err := run(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "atelier:", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	switch cmd {
	case "", "up":
		return up()
	case "open": // the overlay; --prs / --worktrees / --new / --trash pick the starting screen
		refreshBindings()
		return ui.Run(tmux.GlobalOption("@atelier_outer"), startScreen(args))
	case "home": // the landing splash — the home window's program, or --switch to move a client onto it (M-h)
		if hasFlag(args, "--switch") {
			return switchHome()
		}
		return ui.RunHome()
	case "create": // headless background workspace builder (fired detached by M-n)
		return createWorkspace(args)
	case "win": // atelier win <session> <agent|shell|diff|checks> [window] — intra-workspace window nav
		refreshBindings()
		return winNav(arg(args, 0), arg(args, 1), arg(args, 2))
	case "hook": // atelier hook <working|blocked|idle>
		return hook(arg(args, 0))
	case "mcp":
		return mcp.Serve()
	case "install":
		return install()
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// up ensures the atelier server, bindings, hooks, and a place to land, then
// attaches the caller to it. This is the entry point users run.
func up() error {
	if err := tmux.EnsureServer(); err != nil {
		return err
	}
	// Dev mode ($ATELIER_DEV) shares the real ~/.claude (for auth + the guarded,
	// env-routed hooks) but must not rewrite it — so it skips hook install.
	if os.Getenv("ATELIER_DEV") == "" {
		_ = agent.InstallHooks()
	}
	// Create the landing splash BEFORE sourcing bindings: a tmux server with no
	// sessions exits immediately, so `source-file` needs a session alive first.
	// (Sourcing before any session is why bindings silently failed to load.)
	homeWasRunning := tmux.HasSession(tmux.HomeSession)
	if err := ensureHome(); err != nil {
		return err
	}
	if homeWasRunning {
		// The splash has been running since the server started, on whatever
		// binary was installed then; restart it so this one's is what you see.
		dir, _ := os.UserHomeDir()
		_ = tmux.RestartHome(dir, self()+" home")
	}
	if err := ensureBindings(); err != nil {
		return err
	}
	syncGitIdentity()        // pin the operator's git identity onto existing sessions
	agent.RefreshAttention() // seed the attention badge from any already-blocked sessions
	// If we're already inside atelier's OWN server, re-attaching would attach
	// it to itself — a hall-of-mirrors loop. No-op instead.
	if insideOwnServer() {
		fmt.Println("atelier: already running here — M-a active · M-n new · M-q detach.")
		return nil
	}
	// Hand the terminal to tmux (attach to the atelier server).
	return execTmux("attach")
}

// ensureHome creates the landing splash session if it is missing and (re)applies
// its status line. Both `up` and M-h go through it.
func ensureHome() error {
	dir, _ := os.UserHomeDir()
	// The home window is the landing splash, not a bare shell, so attaching
	// always lands on a real screen.
	return tmux.EnsureHome(dir, self()+" home")
}

// switchHome points the invoking client at the splash (M-h). The client comes
// from @atelier_outer, which the binding captures before running this.
func switchHome() error {
	if err := ensureHome(); err != nil {
		return err
	}
	return tmux.SwitchClient(tmux.GlobalOption("@atelier_outer"), "home")
}

// insideOwnServer reports whether the caller is already inside a tmux client of
// atelier's own socket (so attach would recurse). $TMUX is
// "<socket-path>,<pid>,<session>"; its socket basename is the -L name.
func insideOwnServer() bool {
	t := os.Getenv("TMUX")
	if t == "" {
		return false
	}
	sock, _, _ := strings.Cut(t, ",")
	return filepath.Base(sock) == core.LoadConfig().Socket
}

// hook records agent status and, when the status actually changes, pushes the
// new marker to the workspace's status line (event-driven, never polled). PR
// status is not refreshed here: the agent only registers PRs (via the MCP
// tools); the UI re-queries their status on demand when the PR view opens.
func hook(status string) error {
	session := os.Getenv("ATELIER_SESSION")
	// Read the prior status before recording, so we redraw only on a transition
	// (a busy agent fires PreToolUse/PostToolUse constantly — no need to flicker
	// the bar when working→working).
	prior := ""
	if session != "" {
		prior = string(agent.Status(session, true))
	}
	if err := agent.HandleHook(status); err != nil {
		return err
	}
	if session != "" && status != prior {
		_ = tmux.SetStatusMarker(session, agent.StatusMarker(core.AgentStatus(status)))
		agent.RefreshAttention() // recompute + push the blocked-count badge + redraw
	}
	return nil
}

// install wires the Claude hooks and prints how to run atelier.
func install() error {
	if err := agent.InstallHooks(); err != nil {
		return err
	}
	if err := ensureBindings(); err != nil {
		return err
	}
	if _, err := agent.EnsureWorkspaceGuide(); err != nil {
		return err
	}
	// Backfill trust + MCP registration for workspaces created before this ran
	// (or by a build that wrote to the wrong Claude config dir).
	for _, w := range core.Load().Workspaces {
		_ = agent.PrepareProject(w.Root(), w.Slug)
	}
	fmt.Println("atelier: hooks installed, bindings written.")
	fmt.Println("Run `atelier` to start. Inside: M-s spaces · M-p PRs · M-w worktrees · M-t trash · M-n new · M-a agent · M-c shell · M-h home · M-q detach.")
	return nil
}

// rgbSetting tells tmux the terminal can take 24-bit colour, when the terminal
// atelier was started from says it can. Without it tmux quantises every colour a
// program emits down to the 256-palette, which is why themes inside atelier can
// look flatter than the same tool run outside it. Read from the launching
// terminal rather than assumed, so a terminal that can't do it isn't promised.
func rgbSetting() string {
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return "set -as terminal-features \",*:RGB\"\n"
	}
	return ""
}

// self is the path to this binary, for the tmux bindings and the splash session
// to invoke. Falls back to the PATH lookup.
func self() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "atelier"
}

// ensureBindings writes atelier's tmux fragment and sources it into its own
// server. This is atelier's dedicated server, not the user's config (NFR-S4).
func ensureBindings() error { return writeBindings(true) }

// refreshBindings re-syncs the bindings when the running server's differ from
// this binary's, and does nothing when they don't. The popup and the window
// keys call it, so a rebuilt or upgraded atelier corrects its own bindings the
// first time it runs, instead of the old ones calling actions the new binary no
// longer has until someone thinks to restart.
func refreshBindings() { _ = writeBindings(false) }

// liveBindings is the version of the bindings the running server last sourced,
// read from the marker the fragment sets. The server's own record, not the file
// on disk: the file can be current while the server never sourced it (written
// by `atelier install` with no server reachable, say).
var liveBindings = func() string { return tmux.GlobalOption("@atelier_bindings") }

// writeBindings writes the fragment and sources it into atelier's server —
// always when force is set, otherwise only if the server's bindings differ.
func writeBindings(force bool) error {
	frag, version := bindingsFragment()
	if !force && liveBindings() == version {
		return nil
	}
	path := filepath.Join(filepath.Dir(core.ConfigPath()), "atelier.tmux")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(frag), 0o644); err != nil {
		return err
	}
	// Best-effort source: a session-less server has already exited (install with
	// nothing running), which is fine — up() sources this after creating `home`,
	// and a live server picks it up immediately.
	_, _ = tmux.Run("source-file", path)
	return nil
}

// bindingsFragment is atelier's tmux configuration for its own server, and its
// version: a hash of the bindings, which the fragment's last line records in the
// server as @atelier_bindings when it is sourced.
func bindingsFragment() (frag, version string) {
	body := bindingsBody()
	sum := sha256.Sum256([]byte(body))
	version = hex.EncodeToString(sum[:8])
	return body + fmt.Sprintf("set -g @atelier_bindings %q\n", version), version
}

func bindingsBody() string {
	return fmt.Sprintf(`# atelier — generated bindings for its own tmux server
%[2]sset -g status-position top
set -g status-style "bg=colour236,fg=colour252"
# Brand (accent "atelier") + live status glyph + workspace name (white) ·
# window-context marker (grey) so you can tell Claude from a shell, working from
# idle. @atelier_status is pushed by the lifecycle hooks (event-driven, no poll).
# Only fg is ever changed (never bg), so the subtle grey bar stays intact.
set -g status-left " #[fg=colour110,bold]atelier#[nobold] #[fg=colour252]#{@atelier_status}#{@atelier_title}#[fg=colour245] · #{?#{==:#{window_name},claude},Agent,Command Line}#[fg=colour252] "
# While a background action runs, an activity spinner (@atelier_spin) takes the
# attention slot; otherwise the attention badge (@atelier_attention, pushed by
# the hooks — event-driven, no polling) shows.
set -g status-right "#{?#{@atelier_spin},#{@atelier_spin},#{@atelier_attention}}  %%H:%%M "
set -g status-left-length 80
# Toast (display-message) look: subtle raised gray with soft-green italic text.
set -g message-style "bg=colour238,fg=colour151,italics"
set -g window-status-format ""
set -g window-status-current-format ""
set -g mouse on
# Alt-chord (M-a/M-c/M-s…) responsiveness.
set -g escape-time 0
# Window names are atelier's identity for nav (claude/shell/<worktree>); keep
# tmux and the running program from renaming them out from under M-c/M-s/M-t.
set -g automatic-rename off
set -g allow-rename off
# Overlay openers. Capture the invoking client into @atelier_outer FIRST:
# set-option -F expands #{client_name}, but display-popup -E does NOT — so the
# UI reads the client from @atelier_outer to switch-client on select. All popups
# are the same size (70x70) so the overlay never jumps between views.
# M-s spaces · M-p PRs · M-w worktrees · M-t trash · M-n new.
bind -n M-s set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open"
bind -n M-p set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --prs"
bind -n M-w set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --worktrees"
bind -n M-t set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --trash"
bind -n M-n set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --new"
# Intra-workspace window nav (run-shell DOES expand #{session_name}). M-a brings
# the agent (Claude) back, respawning it if you exited it — everywhere, a PR's
# windows included. M-c is the workspace base shell. Both toast (no-op) when
# pressed off a workspace, e.g. the splash.
bind -n M-a run-shell "%[1]s win '#{session_name}' agent"
# A PR's two windows swap in place: M-e in its diff (pr-*) opens its checks,
# M-d in its checks (ci-*) goes back to the diff. Anywhere else each key is
# passed through untouched — Alt-d is delete-word in a shell and in Claude's input.
bind -n M-e if-shell -F "#{m:pr-*,#{window_name}}" "run-shell \"%[1]s win '#{session_name}' checks '#{window_name}'\"" "send-keys M-e"
bind -n M-d if-shell -F "#{m:ci-*,#{window_name}}" "run-shell \"%[1]s win '#{session_name}' diff '#{window_name}'\"" "send-keys M-d"
bind -n M-c run-shell "%[1]s win '#{session_name}' shell"
# M-h returns to the landing splash — the keymap and the dependency doctor live
# there. It is a session switch, not a popup: the space you left keeps running.
bind -n M-h set-option -gF @atelier_outer "#{client_name}" \; run-shell "%[1]s home --switch"
# M-q detaches atelier — back to the shell that spawned it.
bind -n M-q detach-client
`, self(), rgbSetting())
}

func execTmux(args ...string) error {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	argv := append([]string{"tmux", "-L", core.LoadConfig().Socket}, args...)
	// Strip $TMUX/$TMUX_PANE so attaching works even when launched from inside
	// another tmux (e.g. `make dev` from your real atelier) — tmux otherwise
	// refuses to attach a nested session.
	return syscall.Exec(bin, argv, envWithout("TMUX", "TMUX_PANE"))
}

// envWithout returns the environment minus the named variables.
func envWithout(drop ...string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	var out []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if !skip[k] {
			out = append(out, e)
		}
	}
	return out
}

// --- tiny arg helpers ---

func arg(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

func hasFlag(a []string, name string) bool {
	for _, s := range a {
		if s == name {
			return true
		}
	}
	return false
}

// startScreen maps the `open` flags to the UI's starting screen.
func startScreen(args []string) string {
	switch {
	case hasFlag(args, "--trash"):
		return "trash"
	case hasFlag(args, "--new"):
		return "new"
	case hasFlag(args, "--prs"):
		return "prs"
	case hasFlag(args, "--worktrees"):
		return "worktrees"
	}
	return ""
}

// flagVal returns the value following the named flag, or "".
func flagVal(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// createWorkspace builds a workspace headlessly, launching Claude INSTANTLY under
// a friendly reference name (dir + session), then naming it with Claude/Sonnet
// mid-work. The referential name is the stable, path-keyed identity (trust, MCP,
// transcript, worktrees all anchor to it — never renamed); the title is a mutable
// display label. Run detached by M-n so the popup closes immediately; --switch
// also lands you in it right away, before the naming call.
func createWorkspace(args []string) error {
	intent := strings.TrimSpace(arg(args, 0))
	if intent == "" {
		return nil
	}
	// Reserve an instant, unique referential name — no model call on the hot path.
	w, err := core.AddUniqueWorkspace(core.Workspace{
		Title: deterministicTitle(intent), Intent: intent, Created: time.Now(),
	}, core.FriendlyName)
	if err != nil {
		return err
	}
	slug := w.Slug
	// Spin the status line while the (multi-second, network-bound) build runs, so
	// the background work is visible; resolves into a check-mark when named.
	finish := startSpinner("creating " + slug)
	defer finish("") // safety net: clear the spinner on any early return
	root := filepath.Join(core.AteliersRoot(), slug)
	if err := os.MkdirAll(root, 0o755); err != nil {
		_ = core.RemoveWorkspace(slug)
		return err
	}
	_ = agent.PrepareProject(root, slug)
	if guide, err := agent.EnsureWorkspaceGuide(); err == nil {
		_ = os.Symlink(guide, filepath.Join(root, "CLAUDE.md"))
	}
	env := map[string]string{"ATELIER_SESSION": slug, "ATELIER_SLUG": slug}
	gitCfg := realGitConfigGlobal()
	if gitCfg != "" {
		env["GIT_CONFIG_GLOBAL"] = gitCfg // so the agent commits as the operator
	}
	if err := tmux.NewSession(slug, root, env, agent.LaunchCmd(intent)); err != nil {
		_ = core.RemoveWorkspace(slug)
		return err
	}
	if gitCfg != "" {
		_ = tmux.SetSessionEnv(slug, "GIT_CONFIG_GLOBAL", gitCfg) // future windows too
	}
	_ = tmux.RenameWindow(slug, agent.ClaudeWindow)                        // so M-c can find/respawn it
	_ = tmux.SetTitle(slug, w.Title)                                       // placeholder title on the status line
	_ = tmux.SetStatusMarker(slug, agent.StatusMarker(core.StatusWorking)) // it's launching — show working at once

	outer := flagVal(args, "--outer")
	if hasFlag(args, "--switch") {
		_ = tmux.SelectWindow(slug, agent.ClaudeWindow)
		_ = tmux.SwitchClient(outer, slug)
	}

	// Name it properly, mid-work: update the title in state and the status line.
	title := w.Title
	if cfg := core.LoadConfig(); cfg.Naming {
		if ttl := agent.GenerateTitle(intent, cfg.NamingModel); ttl != "" {
			title = ttl
			_ = core.Update(func(s *core.State) {
				if x := s.FindBySession(slug); x != nil {
					x.Title = ttl
				}
			})
			_ = tmux.SetTitle(slug, ttl)
			_ = tmux.RefreshStatus()
		}
	}
	// Resolve the spinner into a green check-mark with the final name, held in the
	// same status-line slot (no separate toast).
	finish("#[fg=colour71]✓#[fg=colour252] " + tmux.EscapeText(title))
	return nil
}

// spinnerHold is how long the finished (check-mark) message lingers in the
// status-line slot before it clears.
const spinnerHold = 5 * time.Second

// startSpinner animates a status-line activity indicator (#{@atelier_spin}) for
// the duration of a background action. The returned finish func stops the
// animation and, given a non-empty message, swaps the same slot to it (e.g. a
// check-mark), holds it briefly, then clears — so completion reads as the
// spinner resolving in place, not a separate toast. The goroutine is scoped to
// the running action — it lives only as long as the action and polls no state,
// so it is not a daemon (NFR).
func startSpinner(label string) func(doneMsg string) {
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	stop := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(stopped)
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = tmux.SetGlobal("@atelier_spin", fmt.Sprintf("#[fg=colour110]%c#[fg=colour252] %s", frames[i%len(frames)], label))
				_ = tmux.RefreshStatus()
				i++
			}
		}
	}()
	return func(doneMsg string) {
		once.Do(func() {
			close(stop)
			<-stopped // ensure no late frame overwrites the done message
			if doneMsg != "" {
				_ = tmux.SetGlobal("@atelier_spin", doneMsg)
				_ = tmux.RefreshStatus()
				time.Sleep(spinnerHold)
			}
			_ = tmux.SetGlobal("@atelier_spin", "")
			_ = tmux.RefreshStatus()
		})
	}
}

// winNav is the M-a (agent) / M-c (base shell) intra-workspace window switch.
// Pressed off a workspace (e.g. the home splash) it toasts a hint instead of
// silently doing nothing.
func winNav(session, target, window string) error {
	ws := core.Load().FindBySession(session)
	if ws == nil {
		return tmux.Notify("", "no workspace here  ·  M-n to create one")
	}
	switch target {
	case "diff", "checks":
		// M-d in a PR's checks window, M-e in its diff window: swap to the other.
		to, key := ui.DiffView, "M-d"
		if target == "checks" {
			to, key = ui.ChecksView, "M-e"
		}
		if ok, err := ui.SwitchPRView(session, window, to); ok || err != nil {
			return err
		}
		// Not a PR's window after all (a worktree shell whose repo starts "pr-"
		// or "ci-", say): the key reaches the program, as it does everywhere else.
		_, err := tmux.Run("send-keys", "-t", "="+session+":"+window, key)
		return err
	case "agent":
		if err := agent.EnsureClaude(session, ws.Root(), ws.Slug); err != nil {
			return err
		}
		if tmux.SelectWindow(session, agent.ClaudeWindow) != nil {
			_ = tmux.SelectFirstWindow(session)
		}
	case "shell":
		return tmux.EnsureShell(session, ws.Root())
	}
	return nil
}

// realGitConfigGlobal locates the operator's real global git config so workspace
// agents commit as them, not the OS default. Under `make dev` the XDG redirect
// hides ~/.config/git/config and a long-lived tmux server can lack
// GIT_CONFIG_GLOBAL, so resolving it by explicit path (rather than trusting env
// inheritance) is what makes agent attribution reliable. Prefers an existing
// GIT_CONFIG_GLOBAL (Makefile/prod), then the standard locations, picking the
// first that actually configures an identity. "" if none is found.
func realGitConfigGlobal() string {
	var cands []string
	if g := os.Getenv("GIT_CONFIG_GLOBAL"); g != "" {
		cands = append(cands, g)
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".gitconfig"))
	}
	for _, p := range cands {
		if hasGitEmail(p) {
			return p
		}
	}
	return ""
}

// hasGitEmail reports whether the git config file at path actually configures a
// user.email — parsed with git, not a substring match: a "[sendemail]" header or
// a URL containing "email" would false-match, and since GIT_CONFIG_GLOBAL
// *replaces* the global config chain (not augments it), a wrong pick would strand
// the agent with no identity.
func hasGitEmail(path string) bool {
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return false
	}
	out, err := exec.Command("git", "config", "--file", path, "--get", "user.email").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// syncGitIdentity pins GIT_CONFIG_GLOBAL on every existing workspace session, so
// a respawned agent or a new shell commits as the operator even if the session
// was created before this fix (the already-running agent needs a relaunch).
func syncGitIdentity() {
	gc := realGitConfigGlobal()
	if gc == "" {
		return
	}
	for _, w := range core.Load().Workspaces {
		_ = tmux.SetSessionEnv(w.Session, "GIT_CONFIG_GLOBAL", gc)
	}
}

// deterministicTitle is the fallback workspace title from the raw intent.
func deterministicTitle(intent string) string {
	s := strings.Join(strings.Fields(intent), " ")
	// Cut on runes, not bytes: 60 bytes into "zażółć gęślą jaźń" lands inside a
	// character, and the title would end in a broken glyph.
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimSpace(string(r[:60])) + "…"
	}
	return s
}
