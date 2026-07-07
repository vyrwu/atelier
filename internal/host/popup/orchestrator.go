// Package popup (in internal/host) provides safe popup orchestration on top
// of tmuxhost. It implements the bash-equivalent of tmux_outer_popup: list
// clients, classify outer vs inner (popup) clients by session-name prefix,
// register a one-shot client-detached hook that opens the new popup on the
// outer client, then detach the inner clients. The hook fires AFTER the
// last detach, ensuring the new popup lands on the outer client.
//
// Critically, this never calls bare `detach-client` (which would detach the
// USER's main client from tmux entirely). It targets inner clients by name.
package popup

import (
	"fmt"
	"strings"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// PopupStyleArgs returns the tmux display-popup args for a tool's preferred
// popup style (border, title, geometry, start_cwd). Driven by the tool's
// manifest Binding.
func PopupStyleArgs(b *manifest.Binding) []string {
	style := manifest.StyleFull
	title := ""
	startCwd := false
	if b != nil {
		if b.Style != "" {
			style = b.Style
		}
		title = b.Title
		startCwd = b.StartCwd
	}

	var out []string
	switch style {
	case manifest.StylePicker:
		out = append(out, "-B", "-w", "70%", "-h", "70%")
	default:
		out = append(out, "-b", "rounded", "-S", "fg=colour103")
		if title != "" {
			out = append(out, "-T", fmt.Sprintf("#[align=centre] %s ", title))
		}
		out = append(out, "-w", "100%", "-h", "99%", "-y", "S")
	}
	if startCwd {
		out = append(out, "-d", "#{pane_current_path}")
	}
	return out
}

// SpinnerStyleArgs returns display-popup args for a small centered
// spinner popup — the "Building workspace..." transient step. Sized to
// just fit a single spinner line (60x3: rounded border + one content
// row) so the popup border wraps the text tightly rather than floating
// in empty space. Colour 141 (light purple) + italic label (set by
// spinner package) signal a transient in-flight state distinct from
// picker green / tool-full-popup gray.
//
// tmux display-popup centers by default when -x/-y are omitted, so no
// explicit position is passed.
//
// Callers should invoke via OpenOnOuter so the current (parent) popup
// closes BEFORE this one opens — otherwise the parent popup rectangle
// sits behind the spinner as a "carved shadow" on the outer terminal.
func SpinnerStyleArgs(title string) []string {
	return []string{
		"-b", "rounded",
		"-S", "fg=colour141",
		"-T", fmt.Sprintf("#[align=centre,italics,fg=colour141] %s ", title),
		"-w", "60",
		"-h", "3",
	}
}

// OpenOnOuter opens a display-popup on the user's outer (non-popup) client.
//
// If any inner (popup) clients exist, they're detached by name; a one-shot
// `client-detached` hook is set up to open the new popup AFTER the last
// inner detach completes. This avoids stacking popups and matches bash's
// tmux_outer_popup semantics.
//
// If there are no inner clients (e.g., invoked directly from outer), the
// popup is opened immediately on the outer client without the hook dance.
//
// styleArgs: the popup geometry/border args (typically PopupStyleArgs).
// invoke:    shell command to run as the popup's -E target.
func OpenOnOuter(h *tmuxhost.Client, styleArgs []string, invoke string) error {
	outer, inner, err := listClients(h)
	if err != nil {
		return err
	}

	// Picker-style popups (Selector / Creator) need `-c <outer>` so any
	// switch-client they issue lands on the workspace client, not the
	// popup pseudo-client. For other styles we still pass it when
	// available — harmless on the no-inner path (same client) and
	// required on the with-inner path (outer client survives the inner
	// detach).
	args := []string{}
	if outer != "" {
		args = append(args, "-c", outer)
	}
	args = append(args, styleArgs...)
	args = append(args, "-E", invoke)

	if len(inner) == 0 {
		// No inner popup client to detach. Use `run-shell -b` so the
		// current popup (if any) closes BEFORE the new popup opens —
		// otherwise tmux silently drops the second display-popup. Matches
		// bash tmux_outer_popup's else branch verbatim.
		shellCmd := "tmux display-popup " + tmuxShellQuoteArgs(args)
		_, err := h.Run("run-shell", "-b", shellCmd)
		return err
	}

	// With inner popup(s) present: set a one-shot client-detached hook
	// that fires display-popup on the outer client AFTER the detach
	// completes. We wrap the popup invocation in `run-shell -b` so it
	// defers to tmux's idle phase — invoking display-popup synchronously
	// from a hook context can silently fail when tmux is still tearing
	// down the popup the hook event was triggered for.
	shellCmd := "tmux display-popup " + tmuxShellQuoteArgs(args)
	hookAction := fmt.Sprintf(`run-shell -b %q ; set-hook -ug client-detached`, shellCmd)
	if _, err := h.Run("set-hook", "-g", "client-detached", hookAction); err != nil {
		return err
	}
	for _, c := range inner {
		_, _ = h.Run("detach-client", "-t", c)
	}
	return nil
}

// OverlayOnOuter opens a display-popup on the outer client WITHOUT
// detaching any existing popup clients. The new popup overlays whatever
// is currently shown (tmux 3.4+ supports popup nesting). If the user
// dismisses the new popup, they return to whatever was underneath.
//
// Use this for popup-table bindings (M-; / M-n / M-s from inside a
// tool's popup) where the user is "peeking" at another atelier surface.
// The dispatched tool's own logic (e.g. SelectCommand.dispatch calling
// OpenOnOuter) handles the replace-vs-coexist decision when the user
// actually picks a destination from the overlay.
func OverlayOnOuter(h *tmuxhost.Client, styleArgs []string, invoke string) error {
	outer, _, err := listClients(h)
	if err != nil {
		return err
	}
	args := []string{}
	if outer != "" {
		args = append(args, "-c", outer)
	}
	args = append(args, styleArgs...)
	args = append(args, "-E", invoke)
	shellCmd := "tmux display-popup " + tmuxShellQuoteArgs(args)
	_, err = h.Run("run-shell", "-b", shellCmd)
	return err
}

// tmuxShellQuoteArgs joins args for use as a /bin/sh -c string passed to
// run-shell. Each arg is single-quoted (with embedded `'` escaped the
// POSIX way) so the shell receives them as literal tokens — tmux already
// expanded any `#{...}` formats before run-shell launched the shell.
func tmuxShellQuoteArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellSingleQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	// If there are no shell metachars at all, leave bare for readability.
	safe := true
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '\\' ||
			r == ';' || r == '|' || r == '&' || r == '$' || r == '`' ||
			r == '(' || r == ')' || r == '<' || r == '>' || r == '*' ||
			r == '?' || r == '[' || r == ']' || r == '{' || r == '}' ||
			r == '#' || r == '!' {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// OpenOuter is a legacy entry point preserved for the `atelier popup outer`
// CLI. Routes to OpenOnOuter with default picker style.
func OpenOuter(h *tmuxhost.Client, _ *state.State, shellCmd string, width, height string) error {
	args := []string{"-B"}
	if width != "" {
		args = append(args, "-w", width)
	}
	if height != "" {
		args = append(args, "-h", height)
	}
	return OpenOnOuter(h, args, shellCmd)
}

// CleanupOrphanedPopups kills popup sessions whose parent (session, window)
// no longer exists. Recognizes BOTH atelier (`_atelier_<tool>_<sid>_<wid>`)
// and bash (`_popup_`, `_claudepop_`, `_k8spop_`, `_awspop_`,
// `_lazygitpop_`) naming. Bash format encodes sid + wid as the first two
// underscore-separated tokens (digit-only).
func CleanupOrphanedPopups(h *tmuxhost.Client) error {
	sessions, err := h.ListSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return nil
	}

	windows, err := h.ListWindows()
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(windows))
	for _, line := range windows {
		var sid, wid string
		if _, err := fmt.Sscanf(line, "%s %s", &sid, &wid); err == nil {
			live[digits(sid)+"_"+digits(wid)] = true
		}
	}

	for _, name := range sessions {
		var sid, wid string
		var ok bool
		switch {
		case isAtelierPopup(name):
			sid, wid, ok = parseWorkspaceScopedName(name)
		case isBashPopup(name):
			sid, wid, ok = parseBashPopupName(name)
		default:
			continue
		}
		if !ok {
			continue
		}
		if !live[sid+"_"+wid] {
			_ = h.KillSession(name)
		}
	}

	remaining, _ := h.ListSessions()
	stillAny := false
	for _, n := range remaining {
		if isAtelierPopup(n) || isBashPopup(n) {
			stillAny = true
			break
		}
	}
	if !stillAny {
		_ = state.ClearChain(h)
	}
	return nil
}

var bashPopupPrefixes = []string{
	"_popup_", "_claudepop_", "_k8spop_", "_awspop_", "_lazygitpop_",
}

func isBashPopup(name string) bool {
	for _, p := range bashPopupPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// parseBashPopupName extracts sid+wid from a bash-style popup session name.
// Naming: `<prefix>sid_wid[_extra]` where prefix is one of bashPopupPrefixes,
// and sid/wid are digits. Returns (sid, wid, true) on success.
func parseBashPopupName(name string) (sid, wid string, ok bool) {
	rest := name
	for _, p := range bashPopupPrefixes {
		if strings.HasPrefix(name, p) {
			rest = name[len(p):]
			break
		}
	}
	parts := strings.SplitN(rest, "_", 3)
	if len(parts) < 2 {
		return "", "", false
	}
	if !allDigits(parts[0]) || !allDigits(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// listClients classifies attached clients into (outer, inner) by session-name
// prefix. Sessions starting with "_" are popup-managed (atelier or otherwise);
// the rest are workspace clients. The first non-popup client wins as outer.
func listClients(h *tmuxhost.Client) (outer string, inner []string, err error) {
	out, err := h.Run("list-clients", "-F", "#{client_session}|#{client_name}")
	if err != nil {
		return "", nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		session, name := parts[0], parts[1]
		if strings.HasPrefix(session, "_") {
			inner = append(inner, name)
		} else if outer == "" {
			outer = name
		}
	}
	return outer, inner, nil
}

func isAtelierPopup(name string) bool {
	return len(name) > len(state.SessionNamePrefix) &&
		name[:len(state.SessionNamePrefix)] == state.SessionNamePrefix
}

func parseWorkspaceScopedName(name string) (sid, wid string, ok bool) {
	const prefix = "_atelier_"
	if len(name) <= len(prefix) {
		return "", "", false
	}
	rest := name[len(prefix):]
	parts := splitN(rest, '_', 3)
	if len(parts) < 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func splitN(s string, sep byte, n int) []string {
	out := make([]string, 0, n)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
			if len(out) == n-1 {
				break
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func digits(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}
