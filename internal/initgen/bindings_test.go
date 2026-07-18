package initgen

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/manifest"
)

func TestBindingBlock_FullStyle(t *testing.T) {
	m := &manifest.Manifest{
		Name: "popupshell",
		Binding: &manifest.Binding{
			Key:      "p",
			Title:    "Popup",
			Style:    manifest.StyleFull,
			StartCwd: true,
		},
	}
	block := BindingBlock("popupshell", m)
	for _, want := range []string{
		"# --- popupshell ---",
		"unbind p",
		"bind -T root p set-option -gF @atelier_outer_pane",
		`-b rounded`,
		`"fg=colour103"`,
		`"#[align=centre] Popup "`,
		"-w100%", "-h99%", "-y S",
		`-d "#{pane_current_path}"`,
		`atelier tools popupshell open`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("missing %q in:\n%s", want, block)
		}
	}
}

func TestBindingBlock_PickerStyle(t *testing.T) {
	m := &manifest.Manifest{
		Name: "workspaces",
		Binding: &manifest.Binding{
			Key:    "M-n",
			Style:  manifest.StylePicker,
			Invoke: "pick",
		},
	}
	block := BindingBlock("workspaces", m)
	for _, want := range []string{
		`unbind -T root "M-n"`,
		`-B`,
		`-w70%`,
		`-h70%`,
		`atelier tools workspaces pick`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("missing %q in:\n%s", want, block)
		}
	}
	for _, no := range []string{`-b rounded`, `colour103`} {
		if strings.Contains(block, no) {
			t.Errorf("unexpected %q in picker block:\n%s", no, block)
		}
	}
}

func TestBindingBlock_AlsoInPopup(t *testing.T) {
	m := &manifest.Manifest{
		Name: "toolselector",
		Binding: &manifest.Binding{
			Key:         "M-;",
			Style:       manifest.StylePicker,
			Invoke:      "select",
			AlsoInPopup: true,
		},
	}
	block := BindingBlock("toolselector", m)
	for _, want := range []string{
		`unbind -T root "M-;"`,
		`unbind -T popup "M-;"`,
		`bind -T root "M-;" set-option`,
		`bind -T popup "M-;" display-popup`,
		`-E 'atelier tools toolselector select'`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("missing %q in:\n%s", want, block)
		}
	}
}

func TestBindingBlock_MultipleBindings(t *testing.T) {
	m := &manifest.Manifest{
		Name: "workspaces",
		Binding: &manifest.Binding{
			Key: "M-n", Style: manifest.StylePicker, Invoke: "pick", AlsoInPopup: true,
		},
		Bindings: []manifest.Binding{
			{Key: "M-s", Style: manifest.StylePicker, Invoke: "sessions", AlsoInPopup: true},
		},
	}
	block := BindingBlock("workspaces", m)
	for _, want := range []string{
		`atelier tools workspaces pick`,
		`atelier tools workspaces sessions`,
		`bind -T popup "M-n" display-popup`,
		`bind -T popup "M-s" display-popup`,
		`-E 'atelier tools workspaces pick'`,
		`-E 'atelier tools workspaces sessions'`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("missing %q in:\n%s", want, block)
		}
	}
}

func TestBindingBlock_NoBinding(t *testing.T) {
	m := &manifest.Manifest{Name: "headless"}
	if got := BindingBlock("headless", m); got != "" {
		t.Fatalf("expected empty block for headless tool, got:\n%s", got)
	}
}

func TestQuoteKey(t *testing.T) {
	cases := map[string]string{
		"p":   "p",
		"M-n": `"M-n"`,
		"C-s": `"C-s"`,
		"M-;": `"M-;"`,
	}
	for k, want := range cases {
		if got := quoteKey(k); got != want {
			t.Errorf("quoteKey(%q): got %q want %q", k, got, want)
		}
	}
}

func TestHooksBlock_ContainsExpectedHooks(t *testing.T) {
	b := HooksBlock()
	for _, h := range []string{"window-unlinked", "session-closed", "after-select-window", "client-session-changed"} {
		if !strings.Contains(b, h) {
			t.Errorf("hooks block missing %q", h)
		}
	}
}

// TestThemeBlock_DistroGradeDefaults locks the distro-promise:
// every option a user would customize within 5 minutes of fresh
// tmux is already set by atelier's bundled mode. If any of these
// drift out, the "shortest path to value" claim breaks — users
// hit a stock-tmux quirk and assume atelier is half-baked.
//
// Grouped by what the user would otherwise have to discover:
//   - clipboard: set-clipboard + copy-mode-vi y → system pasteboard
//   - scrollback: 50k lines (vs tmux default 2k)
//   - focus-events: needed for vim/nvim to detect focus inside tmux
//   - repeat-time 0: kill the chord-swallowing repeat window
//   - automatic-rename off: atelier persists windows by name
func TestThemeBlock_DistroGradeDefaults(t *testing.T) {
	b := ThemeBlock()
	required := map[string]string{
		"set-clipboard on":                            "OSC 52 clipboard passthrough — `set -g set-clipboard on`",
		"history-limit 50000":                         "scrollback bumped from tmux's 2000-line default",
		"focus-events on":                             "needed for vim/nvim FocusGained/FocusLost inside tmux",
		"repeat-time 0":                               "kill chord-swallowing repeat window",
		"atelier internal clipboard-copy":             "copy-mode yank pipes to system clipboard",
		"copy-mode-vi y send -X copy-pipe-and-cancel": "y binding wired to clipboard pipe",
		"bind -T popup C-] copy-mode":                 "popup key-table needs an explicit copy-mode entry (prefix is unset there)",
		"automatic-rename off":                        "window names persist across shell process changes",
		"allow-rename off":                            "same",
	}
	for opt, why := range required {
		if !strings.Contains(b, opt) {
			t.Errorf("ThemeBlock missing %q (%s). full block:\n%s", opt, why, b)
		}
	}
}

// TestThemeBlock_HidesInactiveWindows locks the "only the current workspace
// in the bar" contract. A repo session holds one window per worktree, so a
// non-empty window-status-format renders EVERY branch name in the status bar
// (the bug the user hit — a flood of workspace names). It must be empty so
// only window-status-current-format renders. Regressing to " #W " floods the
// bar again.
func TestThemeBlock_HidesInactiveWindows(t *testing.T) {
	b := ThemeBlock()
	// Background windows render only when they need attention; the current
	// workspace renders via window-status-current-format. So the bar shows
	// "current + anything waiting", never a flood of every branch name (the
	// bug) and never hiding a background workspace that needs the user.
	if !strings.Contains(b, `set -g window-status-format "#{?@needs_attention, #W ,}"`) {
		t.Errorf("window-status-format must be attention-conditional; block:\n%s", b)
	}
	if strings.Contains(b, `set -g window-status-format " #W "`) {
		t.Error("window-status-format ' #W ' renders EVERY window name in the bar (flood regression)")
	}
	if !strings.Contains(b, `set -g window-status-current-format "#[bold] #W #[nobold]"`) {
		t.Errorf("window-status-current-format missing/changed — current workspace must still render; block:\n%s", b)
	}
}

// TestThemeBlock_UserOverrideHookIsLast locks the override
// contract: the if-shell that sources ~/.config/atelier/tmux.conf.local
// must be the FINAL line emitted by ThemeBlock so user settings
// override every atelier-set default above them. If something gets
// appended after the hook, those new defaults would silently
// outrank the user's local config — a confusing override-of-an-
// override that breaks the "your overrides win" promise.
func TestThemeBlock_UserOverrideHookIsLast(t *testing.T) {
	b := ThemeBlock()
	want := "if-shell '[ -r ~/.config/atelier/tmux.conf.local ]' 'source-file ~/.config/atelier/tmux.conf.local'"
	if !strings.Contains(b, want) {
		t.Fatalf("ThemeBlock missing user-override hook:\n%s", b)
	}
	// Stripping trailing whitespace, the hook line must be the last
	// meaningful instruction. tmux ignores blank lines and comments
	// for ordering, but the LAST `set`-like directive must be the
	// source-file hook.
	lines := strings.Split(strings.TrimSpace(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !strings.Contains(l, "source-file ~/.config/atelier/tmux.conf.local") {
			t.Errorf("override hook must be the LAST directive in ThemeBlock; found %q after it.\nfull block:\n%s",
				l, b)
		}
		return
	}
}

// TestRestoreBlock_RunsPopupCleanupAtStartup locks the auto-GC
// contract: every fresh atelier tmux server kicks off a popup
// orphan sweep on startup. Without this, hook failures (cleanup
// command not on PATH at hook-fire time, popup crash skipping
// hooks) leak orphans across launches — which is exactly the
// "user must remember to run a maintenance command" friction
// we decommissioned the doctor-report-only path to avoid.
func TestRestoreBlock_RunsPopupCleanupAtStartup(t *testing.T) {
	b := RestoreBlock()
	// The startup sweep MUST use --startup so testtmux sockets
	// bypass the GC — otherwise tests that create orphan-by-
	// construction popup fixtures get them swept before use.
	want := "atelier popup cleanup --startup"
	if !strings.Contains(b, want) {
		t.Errorf("restore block must invoke %q at server-startup; got:\n%s", want, b)
	}
}

// TestHooksBlock_LastActiveChainedToClientSessionChanged locks in the
// chained-hook contract: the last-active stamp uses `set-hook -ag` to
// APPEND to client-session-changed rather than replacing the existing
// `clear-popup` handler. Without -a, attaching this hook would wipe
// the attention-clear behavior on every popup landing.
func TestHooksBlock_LastActiveChainedToClientSessionChanged(t *testing.T) {
	b := HooksBlock()
	if !strings.Contains(b, "set-hook -ag client-session-changed") {
		t.Errorf("hooks block must use `set-hook -ag` (append) for last-active; got:\n%s", b)
	}
	if !strings.Contains(b, "atelier internal stamp-last-active") {
		t.Errorf("hooks block missing stamp-last-active invocation; got:\n%s", b)
	}
	if !strings.Contains(b, `#{session_name}`) {
		t.Errorf("hooks block must pass #{session_name} to stamp-last-active; got:\n%s", b)
	}
}

// TestHooksBlock_NoStampLastSeen guards against the removed @last_seen
// machinery creeping back: the picker's Age column reads @created_ts, not
// a switch-away timestamp, so no last-seen hook should be emitted.
func TestHooksBlock_NoStampLastSeen(t *testing.T) {
	if b := HooksBlock(); strings.Contains(b, "stamp-last-seen") || strings.Contains(b, "@last_seen") {
		t.Errorf("hooks block must not reference the removed last-seen machinery; got:\n%s", b)
	}
}
