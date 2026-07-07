package popup

import (
	"errors"
	"testing"
)

// fakeClient is a programmable popup.Client for unit tests. Each method
// returns the value from its `*Result` table keyed by argument.
type fakeClient struct {
	globals       map[string]string
	displayMsg    map[string]string // format string → result
	displayMsgAt  map[string]string // target|format → result
	displayMsgErr error
}

func (f *fakeClient) Run(args ...string) ([]byte, error) { return nil, nil }
func (f *fakeClient) HasSession(string) (bool, error)    { return false, nil }
func (f *fakeClient) ShowGlobalOption(name string) (string, error) {
	if v, ok := f.globals[name]; ok {
		return v, nil
	}
	return "", nil
}
func (f *fakeClient) DisplayMessage(format string) (string, error) {
	if f.displayMsgErr != nil {
		return "", f.displayMsgErr
	}
	if v, ok := f.displayMsg[format]; ok {
		return v, nil
	}
	return "", nil
}
func (f *fakeClient) DisplayMessageAt(target, format string) (string, error) {
	if v, ok := f.displayMsgAt[target+"|"+format]; ok {
		return v, nil
	}
	return "", nil
}
func (f *fakeClient) NewSessionWithCommand(string, string) error { return nil }
func (f *fakeClient) KillSession(string) error                   { return nil }
func (f *fakeClient) Attach(string) error                        { return nil }

// envFn returns a getenv-shaped closure backed by a map.
func envFn(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestResolveParentContext_EnvWins covers the priority-1 path: when
// TMUX_PARENT_* env vars are set (the binding case), they're used even
// if state globals / current pane would also yield values.
func TestResolveParentContext_EnvWins(t *testing.T) {
	h := &fakeClient{
		globals: map[string]string{
			"@atelier_outer_session": "$99",
			"@atelier_outer_window":  "@99",
		},
		displayMsg: map[string]string{
			"#{session_id}": "$77",
			"#{window_id}":  "@77",
		},
	}
	env := envFn(map[string]string{
		"TMUX_PARENT_SESSION_ID": "$1",
		"TMUX_PARENT_WINDOW_ID":  "@2",
		"TMUX_PARENT_PANE_PWD":   "/home/u/code",
	})
	got, err := resolveParentContext(h, env)
	if err != nil {
		t.Fatalf("resolveParentContext: %v", err)
	}
	if got.SessionID != "$1" || got.WindowID != "@2" || got.Cwd != "/home/u/code" {
		t.Errorf("env-priority path: got %+v", got)
	}
}

// TestResolveParentContext_GlobalFallback covers priority-2: env empty,
// atelier globals set (the "popup opened from inside another popup"
// case where the binding didn't pass -e env).
func TestResolveParentContext_GlobalFallback(t *testing.T) {
	h := &fakeClient{
		globals: map[string]string{
			"@atelier_outer_session": "$5",
			"@atelier_outer_window":  "@6",
			"@atelier_outer_pane":    "%7",
		},
		displayMsgAt: map[string]string{
			"%7|#{pane_current_path}": "/projects/foo",
		},
	}
	got, err := resolveParentContext(h, envFn(nil))
	if err != nil {
		t.Fatalf("resolveParentContext: %v", err)
	}
	if got.SessionID != "$5" {
		t.Errorf("SessionID from global: got %q want %q", got.SessionID, "$5")
	}
	if got.WindowID != "@6" {
		t.Errorf("WindowID from global: got %q want %q", got.WindowID, "@6")
	}
	if got.Cwd != "/projects/foo" {
		t.Errorf("Cwd from outer pane: got %q want %q", got.Cwd, "/projects/foo")
	}
}

// TestResolveParentContext_CurrentPaneFallback covers priority-3: env
// empty, globals empty, tool invoked from a regular shell. We fall back
// to tmux's current session/window so the popup still has a home.
// Lazygit and popupshell used to error here; the helper now silently
// resolves — strictly an improvement for direct CLI invocations.
func TestResolveParentContext_CurrentPaneFallback(t *testing.T) {
	h := &fakeClient{
		displayMsg: map[string]string{
			"#{session_id}": "$0",
			"#{window_id}":  "@0",
		},
	}
	got, err := resolveParentContext(h, envFn(nil))
	if err != nil {
		t.Fatalf("resolveParentContext: %v", err)
	}
	if got.SessionID != "$0" || got.WindowID != "@0" {
		t.Errorf("current-pane fallback: got %+v", got)
	}
}

// TestResolveParentContext_AllSourcesEmpty asserts the error path:
// when env, globals, AND current display-message all fail, the
// helper returns an explanatory error rather than a half-resolved
// context.
func TestResolveParentContext_AllSourcesEmpty(t *testing.T) {
	h := &fakeClient{displayMsgErr: errors.New("no tmux server")}
	_, err := resolveParentContext(h, envFn(nil))
	if err == nil {
		t.Fatal("expected error when no source resolves, got nil")
	}
}

// TestResolveParentContext_SigilRestored asserts that env vars passed
// without `$`/`@` sigils (the binding's `-e SID=#{session_id}` strips
// them) get the sigils restored so downstream tmux target specs match.
func TestResolveParentContext_SigilRestored(t *testing.T) {
	h := &fakeClient{}
	env := envFn(map[string]string{
		"TMUX_PARENT_SESSION_ID": "3", // unprefixed
		"TMUX_PARENT_WINDOW_ID":  "4", // unprefixed
	})
	got, err := resolveParentContext(h, env)
	if err != nil {
		t.Fatalf("resolveParentContext: %v", err)
	}
	if got.SessionID != "$3" {
		t.Errorf("SessionID sigil restore: got %q want %q", got.SessionID, "$3")
	}
	if got.WindowID != "@4" {
		t.Errorf("WindowID sigil restore: got %q want %q", got.WindowID, "@4")
	}
}

// TestResolveParentContext_PartialEnv covers mixed sources: session
// from env, window from atelier global. This happens when a binding
// passes one but not the other (defensive).
func TestResolveParentContext_PartialEnv(t *testing.T) {
	h := &fakeClient{
		globals: map[string]string{"@atelier_outer_window": "@42"},
	}
	env := envFn(map[string]string{"TMUX_PARENT_SESSION_ID": "$1"})
	got, err := resolveParentContext(h, env)
	if err != nil {
		t.Fatalf("resolveParentContext: %v", err)
	}
	if got.SessionID != "$1" || got.WindowID != "@42" {
		t.Errorf("mixed env+global: got %+v", got)
	}
}
