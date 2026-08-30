// Command atelier is the whole tool: the UI overlay, the tiny CLI the hooks and
// status line call, and the MCP server — one binary (NFR-S2). It runs its
// sessions on a dedicated tmux socket so it never disturbs your main tmux.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/forge"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/mcp"
	"github.com/vyrwu/atelier/internal/tmux"
	"github.com/vyrwu/atelier/internal/ui"
)

// version is stamped by goreleaser at release time (-X main.version).
var version = "dev"

func main() {
	core.LoadConfig() // applies ATELIER_ROOT; harmless everywhere
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
	case "open": // the overlay. A flag picks the starting screen (M-a/M-w/M-n/M-r/M-t).
		return ui.Run(tmux.GlobalOption("@atelier_outer"), startScreen(args))
	case "home": // the landing splash (the home window's program)
		return ui.RunHome()
	case "create": // headless background workspace builder (fired detached by M-n)
		return createWorkspace(args)
	case "win": // atelier win <session> <claude|shell> — intra-workspace window nav
		return winNav(arg(args, 0), arg(args, 1))
	case "hook": // atelier hook <working|blocked|idle>
		return hook(arg(args, 0))
	case "status": // status-line emitters
		return status(args)
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
	if err := ensureBindings(); err != nil {
		return err
	}
	if !tmux.HasSession("home") {
		home, _ := os.UserHomeDir()
		self, err := os.Executable()
		if err != nil {
			self = "atelier"
		}
		// The home window is the landing splash, not a bare shell, so attaching
		// always lands on a real screen. The status bar is chrome for workspaces
		// (title + attention badge) — hide it on the splash for a clean landing.
		_ = tmux.NewSession("home", home, nil, self+" home")
		_, _ = tmux.Run("set-option", "-t", "home", "status", "off")
	}
	// If we're already inside atelier's OWN server, re-attaching would attach
	// it to itself — a hall-of-mirrors loop. No-op instead.
	if insideOwnServer() {
		fmt.Println("atelier: already running here — M-a active · M-n new · M-q detach.")
		return nil
	}
	// Hand the terminal to tmux (attach to the atelier server).
	return execTmux("attach")
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

// hook records agent status; on idle (turn end) it also refreshes the
// workspace's PRs (FR-E4) — the one place a network call is triggered, and it
// is event-driven, never polled.
func hook(status string) error {
	if err := agent.HandleHook(status); err != nil {
		return err
	}
	if status != "idle" {
		return nil
	}
	session := os.Getenv("ATELIER_SESSION")
	if session == "" {
		return nil
	}
	w := core.Load().FindBySession(session)
	if w == nil {
		return nil
	}
	prs := forge.Refresh(w.PRs, git.Worktrees(w.Root()))
	return core.Update(func(s *core.State) {
		if x := s.FindBySession(session); x != nil {
			x.PRs = prs
			x.PRsRefreshed = time.Now()
		}
	})
}

// status renders the tmux status-line segments (called from the fragment).
func status(args []string) error {
	switch arg(args, 0) {
	case "attention":
		if n := agent.BlockedCount(tmux.ListSessions()); n > 0 {
			fmt.Printf("#[fg=colour214,bold]● %d#[default]", n)
		}
	case "title":
		if w := core.Load().FindBySession(arg(args, 1)); w != nil {
			fmt.Print(w.Title)
		}
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
	fmt.Println("Run `atelier` to start. Inside: M-a active · M-w all · M-n new · M-r PRs · M-t worktrees · M-c Claude · M-s shell · M-q detach.")
	return nil
}

// ensureBindings writes atelier's tmux fragment and sources it into its own
// server. This is atelier's dedicated server, not the user's config (NFR-S4).
func ensureBindings() error {
	self, err := os.Executable()
	if err != nil {
		self = "atelier"
	}
	frag := fmt.Sprintf(`# atelier — generated bindings for its own tmux server
set -g status-position top
set -g status-style "bg=default,fg=default"
set -g status-left " #[bold]#(%[1]s status title '#{client_session}')#[nobold] "
set -g status-right "#(%[1]s status attention)  %%H:%%M "
set -g status-left-length 80
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
# M-a active · M-w all · M-n new · M-r PRs · M-t worktrees.
bind -n M-a set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open"
bind -n M-w set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --all"
bind -n M-n set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --new"
bind -n M-r set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --prs"
bind -n M-t set-option -gF @atelier_outer "#{client_name}" \; display-popup -B -w 70%% -h 70%% -E "%[1]s open --worktrees"
# Intra-workspace window nav (run-shell DOES expand #{session_name}). M-c always
# brings Claude back (respawns it if you exited it); M-s is a workspace-dir shell.
bind -n M-c run-shell "%[1]s win '#{session_name}' claude"
bind -n M-s run-shell "%[1]s win '#{session_name}' shell"
# M-q detaches atelier — back to the shell that spawned it.
bind -n M-q detach-client
`, self)
	path := filepath.Join(filepath.Dir(core.ConfigPath()), "atelier.tmux")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(frag), 0o644); err != nil {
		return err
	}
	// Fail loudly rather than silently leaving atelier without its bindings.
	if _, err := tmux.Run("source-file", path); err != nil {
		return fmt.Errorf("sourcing bindings: %w", err)
	}
	return nil
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
	case hasFlag(args, "--all"):
		return "all"
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

// createWorkspace builds a workspace headlessly: name it (Claude/Sonnet), lay out
// the dir, launch the agent, record it, and toast when ready. Run detached by M-n
// so the popup can close immediately. With --switch it also lands you in it.
func createWorkspace(args []string) error {
	intent := strings.TrimSpace(arg(args, 0))
	if intent == "" {
		return nil
	}
	cfg := core.LoadConfig()
	ttl := ""
	if cfg.Naming {
		ttl = agent.GenerateTitle(intent, cfg.NamingModel)
	}
	if ttl == "" {
		ttl = deterministicTitle(intent)
	}
	slug := core.Slug(ttl)
	root := filepath.Join(core.AteliersRoot(), slug)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	_ = agent.PrepareProject(root, slug)
	if guide, err := agent.EnsureWorkspaceGuide(); err == nil {
		_ = os.Symlink(guide, filepath.Join(root, "CLAUDE.md"))
	}
	env := map[string]string{"ATELIER_SESSION": slug, "ATELIER_SLUG": slug}
	if err := tmux.NewSession(slug, root, env, agent.LaunchCmd(intent)); err != nil {
		return err
	}
	_ = tmux.RenameWindow(slug, agent.ClaudeWindow) // so M-c can find/respawn it
	_ = core.AddWorkspace(core.Workspace{
		Slug: slug, Title: ttl, Intent: intent, Created: time.Now(), Session: slug,
	})
	outer := flagVal(args, "--outer")
	_ = tmux.Notify(outer, "✓ "+ttl+"  ·  M-a to open")
	if hasFlag(args, "--switch") {
		_ = tmux.SelectWindow(slug, agent.ClaudeWindow)
		_ = tmux.SwitchClient(outer, slug)
	}
	return nil
}

// winNav is the M-c / M-s intra-workspace window switch. It no-ops off a
// workspace (e.g. the home splash).
func winNav(session, target string) error {
	ws := core.Load().FindBySession(session)
	if ws == nil {
		return nil
	}
	switch target {
	case "claude":
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

// deterministicTitle is the fallback workspace title from the raw intent.
func deterministicTitle(intent string) string {
	s := strings.Join(strings.Fields(intent), " ")
	if len(s) > 60 {
		s = strings.TrimSpace(s[:60]) + "…"
	}
	return s
}
