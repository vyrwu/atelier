package state

import "strings"

// This file is the single owner of atelier's tmux session taxonomy:
// how a session name classifies (workspace / launcher / popup) and how a
// popup-backing session name parses back to its parent (session, window).
//
// Before kernelization these rules were copy-pasted across
// internal/host/popup, internal/cli, internal/workspace, and internal/popup
// — four subtly-different parsers that drifted (one validated digits, one
// didn't; one recognized bash prefixes, one didn't). Every caller now routes
// through here so the taxonomy has exactly one definition.

// SessionKind classifies a tmux session by its role in atelier's graph.
type SessionKind int

const (
	// KindWorkspace is a repo/worktree session — the outer surface the
	// user actually works in and the only valid target for the outer pointer.
	KindWorkspace SessionKind = iota
	// KindLauncher is the bundled launcher bootstrap shell (LauncherSessionName).
	// A client sitting here is NOT a valid outer — stamping it as the outer
	// workspace is the "lands in a weird default shell" bug.
	KindLauncher
	// KindPopup is an atelier- or bash-managed popup-backing session (a tool).
	KindPopup
)

func (k SessionKind) String() string {
	switch k {
	case KindLauncher:
		return "launcher"
	case KindPopup:
		return "popup"
	default:
		return "workspace"
	}
}

// LauncherSessionName is the bundled launcher's bootstrap session name
// (`tmux new-session -A -s default`). It carries no @repo_path /
// @ai_workspace_kind, so it is never a workspace. Hardcoded because atelier
// never creates a workspace session literally named "default".
const LauncherSessionName = "default"

// BashPopupPrefixes are the legacy bash popup session-name prefixes atelier
// still recognizes for cleanup + attention-rollup parity. Single source of
// truth — replaces five inline copies. None is a prefix of another, so match
// order is irrelevant.
var BashPopupPrefixes = []string{
	"_popup_", "_claudepop_", "_k8spop_", "_awspop_", "_lazygitpop_",
}

// PopupForm identifies which popup naming scheme a session name matches.
type PopupForm int

const (
	FormNone    PopupForm = iota // not a parseable popup-with-parent name
	FormAtelier                  // _atelier_<tool>_<sid>_<wid>
	FormBash                     // <bashprefix><sid>_<wid>[_extra]
)

// PopupInfo is the parse result for a popup-backing session name.
type PopupInfo struct {
	Form     PopupForm
	Tool     string // FormAtelier only; "" for the bash form
	SidDigit string // parent session_id, digits only (no "$")
	WidDigit string // parent window_id, digits only (no "@")
}

// ClassifySession returns the kind of a session by name. Popup detection wins
// first (a popup session could never be named "default"), then the exact
// launcher name, else workspace.
func ClassifySession(name string) SessionKind {
	if IsPopupSession(name) {
		return KindPopup
	}
	if name == LauncherSessionName {
		return KindLauncher
	}
	return KindWorkspace
}

// IsPopupSession reports whether name is any atelier or bash popup-backing
// session. Note: a session-global atelier popup (`_atelier_<tool>`, no
// parent) is still a popup session — it just has no parseable parent, so
// ParsePopup returns ok=false for it while this returns true.
func IsPopupSession(name string) bool {
	if strings.HasPrefix(name, SessionNamePrefix) {
		return true
	}
	for _, p := range BashPopupPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ParsePopup parses a popup-backing session name in EITHER form and returns
// the form + parent sid/wid digits. ok=false for non-popup names and for
// session-global atelier popups (`_atelier_<tool>` with no _sid_wid).
//
// Atelier form is lenient on digits (the ids always are digits in practice,
// derived via Digits at creation); the bash form requires both tokens be
// all-digits — the stricter of the two historical parsers. A bash name whose
// tokens aren't digits simply doesn't parse, which every caller treats as a
// safe no-op.
func ParsePopup(name string) (PopupInfo, bool) {
	if strings.HasPrefix(name, SessionNamePrefix) {
		rest := strings.TrimPrefix(name, SessionNamePrefix)
		parts := strings.Split(rest, "_")
		if len(parts) < 3 {
			return PopupInfo{}, false
		}
		return PopupInfo{Form: FormAtelier, Tool: parts[0], SidDigit: parts[1], WidDigit: parts[2]}, true
	}
	for _, p := range BashPopupPrefixes {
		if !strings.HasPrefix(name, p) {
			continue
		}
		rest := strings.TrimPrefix(name, p)
		parts := strings.SplitN(rest, "_", 3)
		if len(parts) < 2 || !allDigits(parts[0]) || !allDigits(parts[1]) {
			return PopupInfo{}, false
		}
		return PopupInfo{Form: FormBash, SidDigit: parts[0], WidDigit: parts[1]}, true
	}
	return PopupInfo{}, false
}

// Digits returns the digit-only substring of s, stripping tmux sigils
// ($ session, @ window, % pane). Single home for the four former copies
// (host/popup digits, cli digitsOf, workspace digitsOnly, popup digits).
func Digits(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// Listable reports whether a window with the given @repo_path /
// @ai_workspace_kind is a real workspace — one the M-s picker lists and the
// attention rollup counts. A window with neither is a raw shell, a spent
// popup, or a workspace that lost its metadata: not something to route
// attention to. Single owner of the predicate; workspace.Listable delegates
// here so the picker, the rollup, and the invariants can't disagree.
func Listable(repoPath, workspaceKind string) bool {
	return repoPath != "" || workspaceKind != ""
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
