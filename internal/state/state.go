// Package state is atelier's single source of truth for runtime context
// across tmux invocations.
//
// Why this exists: in the bash setup, each script reconstructed "where am I,
// who's the outer client, which popup chain am I in" from scratch by parsing
// env vars, session names, and tmux queries. Disagreements between scripts
// caused bugs — notably the M-s case where one binding would detach the
// inner popup so another binding could render a picker on the outer client.
//
// In atelier, every command starts by calling state.Capture, gets a typed
// snapshot of the runtime context, and uses it to make safe decisions:
//
//   - "Am I inside an atelier popup?" — InPopup
//   - "What's the outermost workspace pane that started this chain?" — OuterPane
//   - "Where should this picker render?" — OuterSession (for `popup outer`)
//
// Across invocations, atelier persists the outer-pane fields via global tmux
// options (@atelier_outer_*), set by the first popup in a chain and cleared
// when the chain ends.
package state

import (
	"fmt"
	"strings"

	"github.com/vyrwu/atelier/internal/tmuxhost"
)

const (
	OptOuterPane    = "@atelier_outer_pane"
	OptOuterSession = "@atelier_outer_session"
	OptOuterWindow  = "@atelier_outer_window"

	// SessionNamePrefix is the prefix every atelier-managed popup session
	// starts with. Anything else is treated as a regular workspace session.
	SessionNamePrefix = "_atelier_"
)

// State is a snapshot of atelier's runtime context inside tmux.
type State struct {
	// Where atelier itself is running right now.
	CurrentPane    string
	CurrentSession string // session id ($N)
	CurrentWindow  string // window id (@N)
	CurrentName    string // session name (used for popup detection)

	// True if the current session is an atelier-managed popup
	// (session name starts with SessionNamePrefix).
	InPopup   bool
	PopupTool string // tool name extracted from session name, if InPopup

	// The outermost (non-popup) workspace pane that began the chain.
	// When not in a popup chain, these equal Current*.
	OuterPane    string
	OuterSession string
	OuterWindow  string
}

// Capture reads tmux + atelier's global options and returns a State snapshot.
func Capture(h *tmuxhost.Client) (*State, error) {
	s := &State{}

	out, err := h.DisplayMessage("#{pane_id}|#{session_id}|#{window_id}|#{session_name}")
	if err != nil {
		return nil, fmt.Errorf("capture state: %w", err)
	}
	parts := strings.Split(out, "|")
	if len(parts) < 4 {
		return nil, fmt.Errorf("display-message returned %d fields, expected 4: %q", len(parts), out)
	}
	s.CurrentPane = parts[0]
	s.CurrentSession = parts[1]
	s.CurrentWindow = parts[2]
	s.CurrentName = parts[3]

	if strings.HasPrefix(s.CurrentName, SessionNamePrefix) {
		s.InPopup = true
		s.PopupTool = extractTool(s.CurrentName)
	}

	s.OuterPane, _ = h.ShowGlobalOption(OptOuterPane)
	s.OuterSession, _ = h.ShowGlobalOption(OptOuterSession)
	s.OuterWindow, _ = h.ShowGlobalOption(OptOuterWindow)

	// If no popup chain is active, the current pane IS the outer.
	if s.OuterPane == "" {
		s.OuterPane = s.CurrentPane
		s.OuterSession = s.CurrentSession
		s.OuterWindow = s.CurrentWindow
	}
	return s, nil
}

// MarkChainStart writes the current pane to global options as the outer of
// a new popup chain. Called by the first popup-opening tool in a chain.
// Idempotent — does nothing if the chain is already active.
func MarkChainStart(h *tmuxhost.Client, s *State) error {
	if s.InPopup {
		return nil // already inside a chain; don't overwrite
	}
	existing, _ := h.ShowGlobalOption(OptOuterPane)
	if existing != "" {
		return nil
	}
	if err := h.SetGlobalOption(OptOuterPane, s.CurrentPane); err != nil {
		return err
	}
	if err := h.SetGlobalOption(OptOuterSession, s.CurrentSession); err != nil {
		return err
	}
	if err := h.SetGlobalOption(OptOuterWindow, s.CurrentWindow); err != nil {
		return err
	}
	return nil
}

// ClearChain wipes the global outer-pane tracking options. Called when the
// last popup in a chain closes (via `atelier popup cleanup` or hook).
func ClearChain(h *tmuxhost.Client) error {
	_ = h.UnsetGlobalOption(OptOuterPane)
	_ = h.UnsetGlobalOption(OptOuterSession)
	_ = h.UnsetGlobalOption(OptOuterWindow)
	return nil
}

func extractTool(sessionName string) string {
	if !strings.HasPrefix(sessionName, SessionNamePrefix) {
		return ""
	}
	rest := sessionName[len(SessionNamePrefix):]
	if i := strings.Index(rest, "_"); i >= 0 {
		return rest[:i]
	}
	return rest
}
