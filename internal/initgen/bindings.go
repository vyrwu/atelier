// Package initgen converts plugin manifests into tmux config blocks for
// `atelier init`. The canonical popup styling lives here — tools just
// declare key + style, and initgen produces the binding.
package initgen

import (
	"fmt"
	"strings"

	"github.com/vyrwu/atelier/internal/manifest"
)

// BindingBlock renders the tmux.conf snippet for a single tool's bindings.
// Returns "" if the manifest declares no bindings (or all bindings lack a Key).
//
// Bindings without a Key are valid in the manifest — they declare popup
// style without a keybinding. They're consumed by the tool selector to
// dispatch with the right popup geometry but are skipped here.
func BindingBlock(toolName string, m *manifest.Manifest) string {
	bindings := m.AllBindings()
	var emit []manifest.Binding
	for _, b := range bindings {
		if b.Key != "" {
			emit = append(emit, b)
		}
	}
	if len(emit) == 0 {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# --- %s ---\n", toolName)
	for i, b := range emit {
		if i > 0 {
			out.WriteString("\n")
		}
		renderBinding(&out, toolName, b)
	}
	return out.String()
}

// CoreBindingsBlock returns bindings atelier core owns directly — not
// derived from any plugin manifest. Available everywhere (root + popup
// tables).
//
//	M-?  cheatsheet popup
//	M-q  detach the outer client (server stays alive, background
//	     agents survive). `atelier server kill` is the explicit force-
//	     quit when the user actually wants the server gone.
//	C-]  (popup table only) enter copy-mode inside a popup.
//
// Popup-table M-? uses inline display-popup (same nesting pattern as
// M-; / M-n / M-s) so the cheatsheet overlays cleanly without
// dismissing the origin popup. Popup-table M-q delegates to
// `atelier server quit` so it detaches the OUTER client (from inside a
// popup, bare `detach-client` would only close the popup pty).
//
// C-] lives here, NOT in ThemeBlock, because the popup key-table is an
// ENGINE construct: popup.ApplyStyle sets `prefix None` on every popup
// session, which kills prefix-based copy-mode entry inside popups. The
// escape hatch that compensates must ship with the engine — otherwise
// `atelier init --bare` (IncludeTheme false) strips the prefix but
// emits no way back into copy-mode, and popup scrollback is dead.
func CoreBindingsBlock() string {
	return `# --- atelier core ---
# M-? — show the atelier keybinding cheatsheet, aggregated across every
# discovered plugin's manifest.
unbind -T root  "M-?"
unbind -T popup "M-?"
bind -T root  "M-?" display-popup -b rounded -S "fg=colour103" -T "#[align=centre] Atelier " -w60% -h60% -E 'atelier cheatsheet'
bind -T popup "M-?" display-popup -b rounded -S "fg=colour103" -T "#[align=centre] Atelier " -w60% -h60% -E 'atelier cheatsheet'

# M-q — detach (server keeps running). FR-5.3: explicit force-kill
# moved to ` + "`atelier server kill`" + ` so long-running popup agents survive.
# Root: bare detach-client (current client = outer). Popup: route via
# ` + "`atelier server quit`" + ` so we detach the OUTER client, not the popup pty.
unbind -T root  "M-q"
unbind -T popup "M-q"
bind -T root  "M-q" detach-client
bind -T popup "M-q" run-shell -b 'atelier server quit'

# C-] — enter copy-mode inside a popup. The popup key-table runs with
# prefix unset (popup.ApplyStyle sets prefix None), so the normal
# prefix-based copy-mode entry can't fire there. Bind C-] directly so
# users can still scroll a popup's pane (e.g., Claude output).
unbind -T popup C-]
bind -T popup C-] copy-mode
`
}

// HooksBlock returns the tmux hooks block (cleanup + attention stamping).
//
// The two client-session-changed entries are chained via `set-hook -a`
// (append). tmux fires both in order on every session switch:
//  1. clear @needs_attention on the parent window of the popup we
//     just landed in (so attention sigil stops blinking).
//  2. persist the current session as "last active" so the bundled
//     launcher can resume it on next launch instead of landing on
//     bare "default".
func HooksBlock() string {
	return `# --- hooks ---
set-hook -g window-unlinked       'run-shell "atelier popup cleanup"'
set-hook -g session-closed        'run-shell "atelier popup cleanup"'
set-hook -g after-select-window   'set-window-option -u @needs_attention'
set-hook -g client-session-changed 'run-shell "atelier status attention clear-popup"'
# Persist the current session as "last active" so the bundled
# launcher can resume it on next launch instead of landing on
# bare "default".
set-hook -ag client-session-changed 'run-shell -b "atelier internal stamp-last-active \"#{session_name}\""'
`
}

// RestoreBlock returns the on-tmux-startup restore block. Emitted at
// the END of `atelier init` so bindings + statusline are wired BEFORE
// restore creates sessions (those sessions inherit the bindings via
// key-table).
//
// No cache-cleanup hooks. The natural tmux event that drops a
// workspace — user types `exit` in their shell → window dies →
// session-closed — is NOT a signal that they want the workspace
// deleted. They typically want it back next time tmux starts.
//
// Cache entries are removed ONLY when:
//   - User invokes M-x in the sessions picker (DeleteRowCommand
//     calls statestore.RemoveSession/RemoveWindow directly).
//   - Restore detects the worktree path is gone (handled by
//     workspace.Restore's existence check).
//   - User runs `atelier state sync` manually.
//
// Restore is SYNCHRONOUS (no `-b`). Why: the bundled launcher's
// `tmux new-session -A -s <last-active>` runs immediately after the
// config sources. If restore is backgrounded, the last-active
// session may not exist yet when -A fires, causing tmux to create
// an empty session under that name (and the restore that lands
// later would then skip the session as "already exists"). User
// resumes onto an empty session — wrong.
//
// Synchronous restore takes ~hundreds of ms in practice (mostly
// session-creation tmux calls); user perceives it as part of
// startup, not a hang.
func RestoreBlock() string {
	return `# --- persistence ---
# Restore: rehydrate workspaces persisted from a prior tmux session.
# Idempotent — safe to source-file twice.
#
# No session-closed / window-unlinked hooks: closing tmux (exit /
# kill-server / crash) is the NORMAL case that should preserve state
# for next start, NOT a signal to wipe it. M-x in the sessions picker
# is the explicit-delete path that clears the cache.
run-shell 'atelier state restore'

# Belt-and-suspenders GC: sweep any popup-prefixed sessions whose
# parent is gone. The window-unlinked + session-closed hooks normally
# catch these inline, but a crash mid-popup (or a hook that couldn't
# find atelier on PATH at that instant) can leak orphans across
# server lifecycles. Running the sweep ONCE here at fresh-server
# startup keeps the user-visible state clean without asking them to
# remember a maintenance command.
#
# --startup signals "this is the boot-time sweep" so it can be a
# no-op on testtmux sockets (where tests create orphan-by-construction
# popup fixtures and don't want them GC'd before they're used).
run-shell -b 'atelier popup cleanup --startup'
`
}

// WelcomeBlock arms the launcher default screen (WS-3): on client attach, when
// the runtime has NO workspaces yet, open the M-n intent creator instead of
// dumping the user on a bare launcher shell. Appended (-ag) so it coexists with
// the refresh-loop's own client-attached hook; a no-op the moment any workspace
// exists, so it stops after the first is created. Bundled mode only — an
// embedded (`--bare`) user drives their own tmux and shouldn't get a popup on
// every attach.
func WelcomeBlock() string {
	return `# --- launcher default screen ---
# No workspaces yet → open M-n on attach (guarded: no-op once one exists).
set-hook -ag client-attached 'run-shell -b "atelier internal welcome"'
`
}

// RefreshLoopBlock starts the background refresh daemon (issue #17). One
// long-lived process, launched via `run-shell -b` so it doesn't block config
// sourcing and inherits ATELIER_TMUX_SOCKET for routing. It binds to the tmux
// server by pid and self-exits when that server goes away or is replaced (it
// does not rely on being signaled on server exit), and is singleton-guarded
// with per-tick self-eviction, so re-sourcing the config or attaching a second
// client never stacks a second daemon.
//
// A `client-attached` watchdog re-runs the same guarded launch on every attach,
// so a daemon that died mid-session (crash, OOM, kill) is respawned the next
// time a client attaches — without re-sourcing the config. The singleton lock
// (validated with a signal-0 probe, so a crashed loop's stale pid never blocks
// a fresh one) makes the re-launch a no-op while the loop is alive.
//
// What it does each tick: TTL-throttled forge (PR) sweep + per-workspace AI
// summary + driver recap/attention (so the pickers stay current without the
// user navigating in) plus a level-triggered kernel + layout reconcile (so
// misrouted/phantom attention, orphan popups, and stale worktree symlinks
// self-heal continuously). Rendering was already continuous; this makes
// the DATA continuous too.
func RefreshLoopBlock() string {
	return `# --- background refresh loop ---
# See internal/tools/workspaces/refresh_loop.go. run-shell -b: non-blocking;
# the daemon binds to this server's pid and self-exits when it's gone/replaced,
# and is singleton-guarded internally (a second launch no-ops while one is live).
run-shell -b 'atelier tools workspaces _refresh-loop'
# Watchdog: re-ensure the daemon on every client attach so a mid-session
# crash/kill recovers without re-sourcing the config. Idempotent via the lock.
set-hook -g client-attached 'run-shell -b "atelier tools workspaces _refresh-loop"'
`
}

// StatuslineBlock returns the statusline wiring block.
//
// This is the PLUGIN-mode delivery of the attention rollup: the bundled
// theme (ThemeBlock) puts the ⏺<n> rollup in status-left, but plugin-mode
// users keep their own theme and never get ThemeBlock. stamp-statusline
// injects the rollup non-destructively into their existing
// window-status-current-format (after #W), so the global ⏺ count of
// attention-flagged windows shows up without atelier owning the whole bar.
// In bundled mode the format is empty (no #W), so the stamp is a harmless
// no-op and the rollup renders once, from status-left.
func StatuslineBlock() string {
	return `# --- statusline ---
set -g status-interval 3
# Idempotent stamp: strips any prior atelier addition and re-injects the
# canonical attention segment. Safe to re-source the config any number of
# times — no accumulation. See internalStampStatuslineCmd for the
# strip-and-re-add details.
run-shell -b 'atelier internal stamp-statusline'
`
}

// PopupTableShim emits a single bind into the `popup` key-table to
// force-create it. tmux creates per-table state lazily on the first
// `bind -T <table>` call; `unbind -T <table> ...` BEFORE that errors
// with `table <table> doesn't exist`. Plugin-mode users don't hit
// this because their host tmux already has popup binds from elsewhere
// (their own conf, tpm-installed plugins, etc.). Bundled mode is the
// first thing tmux sees, so we need to create the table ourselves.
//
// F12 is a real tmux key spec (F1-F12 are valid named keys). Action
// is an empty display-message — a no-op visible to nobody. We picked
// F12 specifically because it's rarely-used inside TUIs running in
// popups (claude/k9s/lazygit don't bind it).
func PopupTableShim() string {
	return `# --- popup key-table shim ---
# Create the popup key-table so subsequent ` + "`unbind -T popup ...`" + ` calls
# don't fail on a fresh tmux server. F12 + empty display-message is a
# no-op binding whose sole purpose is to materialize the table.
bind -T popup F12 display-message ""
`
}

// ThemeBlock returns the bundled tmux theme + the distro-grade
// behavioral defaults that turn "fresh atelier" into "usable
// terminal in 30 seconds." Emitted by `atelier init` in default
// (non-bare) mode and consumed by the `atelier` launch command's
// bundled config.
//
// What this block ships:
//   - Terminal capabilities (truecolor, clipboard passthrough).
//   - Behavioral defaults every tmux user customizes within minutes
//     of first launch (mouse, scrollback, escape-time, focus-events,
//     base-index, renumber-windows, vi mode + system-clipboard yank).
//   - automatic-rename / allow-rename off — atelier persists
//     workspaces keyed on window name; tmux must not drift them.
//   - Pane border accent so atelier popups look intentional.
//   - A statusline at the TOP: the current workspace's description
//     (its intent-derived title) at the left, the global attention
//     rollup beside it, the clock at the right. No repo/branch and no
//     per-window chrome. NO powerline glyphs / Nerd Font dependency —
//     the default must render on stock fonts.
//   - User-override hook: ~/.config/atelier/tmux.conf.local is
//     sourced LAST when present, so any user customization wins
//     over atelier's defaults without forking the bundled config.
//
// Power users who want to bring their own theme entirely run
// `atelier init --bare` which omits this block; only the engine
// wiring (bindings, hooks, statusline-injection) emits. This is
// the "atelier-the-distribution" surface. Everything else in
// initgen is engine.
func ThemeBlock() string {
	return `# --- atelier theme + distro-grade defaults ---
# This block is the "fully featured tmux distribution" promise:
# everything a user would customize before tmux feels usable is
# already wired. The bundled mode targets parity-with-personal-
# config out of the box, not minimum-viable.
#
# To override anything here without forking, drop a
# ~/.config/atelier/tmux.conf.local file. It's sourced after this
# block (see the if-shell at the bottom), so your settings win.

# --- Terminal capabilities ---
# Truecolor + italics on xterm-family terminals.
set-option -sa terminal-overrides ",xterm*:Tc"
# Clipboard passthrough: tmux advertises OSC 52 support to the host
# terminal so set-clipboard hits the system pasteboard (iTerm2,
# Ghostty, Alacritty, kitty, WezTerm all honor this). Pairs with
# set-clipboard on below.
set-option -ga terminal-features ",xterm*:clipboard"
set-option -ga terminal-features ",alacritty:clipboard"
set-option -ga terminal-features ",ghostty:clipboard"
set-option -ga terminal-features ",wezterm:clipboard"
set-option -g default-terminal "tmux-256color"

# --- Behavioral defaults ---
set -g mouse on
set -g escape-time 0
set -g focus-events on
# tmux defaults history-limit to 2000 lines — runs out within one
# verbose CI log. 50k is the common "big enough for any reasonable
# scrollback session" pick.
set -g history-limit 50000
set -g base-index 1
set -g pane-base-index 1
set-window-option -g pane-base-index 1
set-option -g renumber-windows on
setw -g mode-keys vi
# repeat-time 0 disables tmux's per-key repeat window. With the
# default 500ms window, M-q M-q (or any double-tap M- chord) is
# absorbed as a single chord; not what users expect from a chord-
# heavy ergonomics-first config.
set -g repeat-time 0
set -g display-time 2000

# Disable tmux automatic-rename: it mutates the window name based
# on the running process (so a workspace named "main" silently
# becomes "zsh" once a shell takes over). atelier persists
# workspaces keyed on (session_name, window_name); a silent
# rename drifts the persistent identity. We name windows
# explicitly at creation; tmux should respect that.
set -g automatic-rename off
set -g allow-rename off

# --- System clipboard ---
# OSC 52 to the terminal — works inside ssh and most modern
# terminals without needing a binary on the remote host.
set -g set-clipboard on

# Copy mode: vi-style begin/end-selection, yank pipes selection to
# the system pasteboard so y inside copy-mode lands on the macOS /
# Linux clipboard transparently. Linux falls back through
# wl-copy → xclip → xsel; macOS prefers pbcopy. Each command is
# tried in order; the first one available on PATH wins.
unbind [
bind ] copy-mode
bind -T copy-mode-vi v send -X begin-selection
bind -T copy-mode-vi y send -X copy-pipe-and-cancel "atelier internal clipboard-copy"
bind -T copy-mode-vi Enter send -X copy-pipe-and-cancel "atelier internal clipboard-copy"
bind -T copy-mode-vi MouseDragEnd1Pane send -X copy-pipe-and-cancel "atelier internal clipboard-copy"

# --- Pane borders + statusline ---
# Subtle accent so atelier popups have a recognizable border style.
set -g pane-border-style "fg=colour240"
set -g pane-active-border-style "fg=colour103"

# Statusline at the TOP: the current WORKSPACE's description (its intent-derived
# title) leads at the left, the global attention rollup beside it, the clock at
# the right. No repo/branch — a workspace is an intent, not a repo, and the
# description already says what it is.
set -g status-position top
set -g status-style "bg=default,fg=default"
# status-left holds the (variable-length) description; give it room.
set -g status-left-length 120
set -g status-right-length 50
# Description via an emitter (reads @workspace_title through show-options — a
# session user-option doesn't resolve in a status FORMAT on every tmux), then
# the global ⏺N attention count. #{client_session} = the focused session.
set -g status-left " #[bold]#(atelier status description '#{client_session}')#[nobold]  #(atelier status attention count) "
set -g status-right " %H:%M "
# The workspace lives in status-left; windows render nothing in the bar.
set -g window-status-format ""
set -g window-status-separator ""
set -g window-status-current-format ""

# --- User override hook ---
# Sourced LAST so user customizations win over every default above.
# File is optional — if absent, this is a silent no-op. Drop your
# personal tmux tweaks (powerline statusline, alternate keybinds,
# custom theme) there; the bundled launcher will pick them up on
# next start.
if-shell '[ -r ~/.config/atelier/tmux.conf.local ]' 'source-file ~/.config/atelier/tmux.conf.local'
`
}

func renderBinding(out *strings.Builder, toolName string, b manifest.Binding) {
	keyTable := b.KeyTable
	if keyTable == "" {
		keyTable = "root"
	}

	fmt.Fprintf(out, "unbind -T %s %s\n", keyTable, quoteKey(b.Key))
	if keyTable == "root" && !needsTableQuote(b.Key) {
		// Bare-key alias for keys also bindable without a table.
		fmt.Fprintf(out, "unbind %s\n", b.Key)
	}
	if b.AlsoInPopup {
		fmt.Fprintf(out, "unbind -T popup %s\n", quoteKey(b.Key))
	}

	invoke := b.Invoke
	if invoke == "" {
		invoke = "open"
	}
	popupOpts := popupOptions(b.Style, b.Title, b.StartCwd)

	// Root-table binding records the outer chain and opens the popup.
	// `-F` is required so `#{...}` format codes expand against the
	// pressing client's pane — without it, set-option stores the literal
	// string and downstream tools end up with empty IDs.
	//
	// `@atelier_outer_client` captures the CLIENT NAME (e.g. /dev/ttys003)
	// of whoever pressed M-;. Tools inside popups that need to issue
	// `switch-client`/`select-window` MUST target that client by `-c`
	// — otherwise tmux non-deterministically picks any attached client,
	// which can land on the user's popupshell session instead of their
	// workspace.
	fmt.Fprintf(out,
		`bind -T %s %s set-option -gF @atelier_outer_pane "#{pane_id}" \; \
    set-option -gF @atelier_outer_session "#{session_id}" \; \
    set-option -gF @atelier_outer_window "#{window_id}" \; \
    set-option -gF @atelier_outer_client "#{client_name}" \; \
    display-popup %s \
    -e TMUX_PARENT_PANE_PWD="#{pane_current_path}" \
    -E 'atelier tools %s %s'
`, keyTable, quoteKey(b.Key), popupOpts, toolName, invoke)

	// Popup-table sibling: fire display-popup INLINE in the tmux binding
	// action. This matches bash's tmux.conf — tmux's binding executor
	// runs display-popup against the current (popup) client's context,
	// which is what makes the new popup NEST cleanly on top of the
	// existing one without disturbing its pty.
	//
	// Why not run-shell -b 'atelier popup goto-tool ...': that breaks
	// nesting because run-shell -b loses the popup-client context. The
	// resulting display-popup -c <wrong-client> disturbs the existing
	// popup's pty, killing the embedded TUI (e.g. k9s crashes,
	// _atelier_k8s session destroyed).
	if b.AlsoInPopup {
		fmt.Fprintf(out,
			`bind -T popup %s display-popup %s \
    -e TMUX_PARENT_PANE_PWD="#{pane_current_path}" \
    -E 'atelier tools %s %s'
`, quoteKey(b.Key), popupOpts, toolName, invoke)
	}
}

// PopupOptions returns the canonical `display-popup` argument string
// (everything between `display-popup` and `-E <cmd>`) for atelier's
// popup styles. Single source of truth — every callsite that opens
// an atelier-styled popup should build its display-popup invocation
// via this helper, not hand-roll the `-b rounded -S "fg=..." -T ...`
// argument string. Changing the border style, accent color, or
// geometry here propagates to every atelier popup automatically.
func PopupOptions(style manifest.Style, title string, startCwd bool) string {
	return popupOptions(style, title, startCwd)
}

func popupOptions(style manifest.Style, title string, startCwd bool) string {
	var parts []string
	switch style {
	case manifest.StylePicker:
		parts = append(parts, "-B", "-w70%", "-h70%")
	case manifest.StyleInput:
		// Compact, CENTERED floating box for a free-text prompt (M-n). No
		// -y anchor → tmux centers it. Carries a border TITLE but no legend —
		// the view owns its heading; the title just frames the popup.
		parts = append(parts, `-b rounded`, `-S "fg=colour103"`)
		if title != "" {
			parts = append(parts, fmt.Sprintf(`-T "#[align=centre] %s "`, title))
		}
		parts = append(parts, "-w60%", "-h30%")
	case manifest.StyleFull, "":
		parts = append(parts, `-b rounded`, `-S "fg=colour103"`)
		if title != "" {
			parts = append(parts, fmt.Sprintf(`-T "#[align=centre] %s "`, title))
		}
		parts = append(parts, "-w100%", "-h99%", "-y S")
	}
	if startCwd {
		parts = append(parts, `-d "#{pane_current_path}"`)
	}
	return strings.Join(parts, " ")
}

func quoteKey(k string) string {
	if needsTableQuote(k) {
		return fmt.Sprintf("%q", k)
	}
	return k
}

func needsTableQuote(k string) bool {
	if strings.HasPrefix(k, "M-") || strings.HasPrefix(k, "C-") {
		return true
	}
	return strings.ContainsAny(k, ";<>")
}
