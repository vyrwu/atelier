package popup

import (
	"errors"
	"strings"
	"testing"
)

// trackingClient records the SEQUENCE of operations a Client receives,
// so OpenWorkspaceScoped tests can assert orchestration order — not
// just the final state. Each method appends a synthetic event string to
// `calls`.
type trackingClient struct {
	*fakeClient
	calls         []string
	sessionExists bool   // result for HasSession
	createCmd     string // captured shellCmd from NewSessionWithCommand
	attached      string // captured session from Attach
	attachErr     error  // simulate attach failure
	ensureErr     error  // simulate NewSessionWithCommand failure
}

func (c *trackingClient) Run(args ...string) ([]byte, error) {
	c.calls = append(c.calls, "Run "+strings.Join(args, " "))
	return nil, nil
}
func (c *trackingClient) HasSession(name string) (bool, error) {
	c.calls = append(c.calls, "HasSession "+name)
	return c.sessionExists, nil
}
func (c *trackingClient) NewSessionWithCommand(name, cmd string) error {
	c.calls = append(c.calls, "NewSessionWithCommand "+name)
	c.createCmd = cmd
	return c.ensureErr
}
func (c *trackingClient) Attach(name string) error {
	c.calls = append(c.calls, "Attach "+name)
	c.attached = name
	return c.attachErr
}

func newTrackingClient() *trackingClient {
	return &trackingClient{
		fakeClient: &fakeClient{
			displayMsg: map[string]string{
				"#{session_id}": "$1",
				"#{window_id}":  "@2",
			},
		},
	}
}

// TestOpenWorkspaceScoped_HappyPath locks in the canonical ordering:
// resolve → ensure (create) → apply style → attach. Five tools used to
// inline this; the helper is now the single audit point.
func TestOpenWorkspaceScoped_HappyPath(t *testing.T) {
	c := newTrackingClient()
	spec := &WorkspaceScoped{Tool: "myplugin", DefaultCmd: "myplugin --start"}

	if err := OpenWorkspaceScoped(c, spec); err != nil {
		t.Fatalf("OpenWorkspaceScoped: %v", err)
	}

	wantSession := "_atelier_myplugin_1_2"
	if c.attached != wantSession {
		t.Errorf("attached session = %q, want %q", c.attached, wantSession)
	}
	if c.createCmd != "myplugin --start" {
		t.Errorf("create cmd = %q, want spec.DefaultCmd %q", c.createCmd, "myplugin --start")
	}

	// HasSession must come BEFORE NewSessionWithCommand (the
	// idempotency check) and Attach must come LAST.
	hasIdx := indexOfPrefix(c.calls, "HasSession ")
	newIdx := indexOfPrefix(c.calls, "NewSessionWithCommand ")
	styleIdx := indexOfPrefix(c.calls, "Run set-option ")
	attachIdx := indexOfPrefix(c.calls, "Attach ")
	if hasIdx >= newIdx || newIdx >= styleIdx || styleIdx >= attachIdx {
		t.Errorf("ordering violated: has=%d new=%d style=%d attach=%d\ncalls: %v",
			hasIdx, newIdx, styleIdx, attachIdx, c.calls)
	}
}

// TestOpenWorkspaceScoped_SessionAlreadyExists asserts the idempotency
// path: when the backing session already exists, NewSessionWithCommand
// must NOT be called (would error in real tmux). Style + Attach still
// happen — they're idempotent and required.
func TestOpenWorkspaceScoped_SessionAlreadyExists(t *testing.T) {
	c := newTrackingClient()
	c.sessionExists = true
	spec := &WorkspaceScoped{Tool: "myplugin", DefaultCmd: "myplugin"}

	if err := OpenWorkspaceScoped(c, spec); err != nil {
		t.Fatalf("OpenWorkspaceScoped: %v", err)
	}
	for _, call := range c.calls {
		if strings.HasPrefix(call, "NewSessionWithCommand ") {
			t.Errorf("session existed but NewSessionWithCommand was called: %v", c.calls)
		}
	}
	if c.attached == "" {
		t.Error("Attach must still happen on existing session")
	}
}

// TestOpenWorkspaceScopedWithCmd_FnOverridesCommand covers the claude
// case: a tool computes the launch command from the resolved context
// (e.g., reading @claude_prompt from ctx.WindowID). The returned cmd
// must reach EnsureWithCmd and be used to spawn the session.
func TestOpenWorkspaceScopedWithCmd_FnOverridesCommand(t *testing.T) {
	c := newTrackingClient()
	spec := &WorkspaceScoped{Tool: "claude", DefaultCmd: "claude"}

	var capturedCtx ParentContext
	customCmd := "claude --settings /tmp/foo.json -- 'do the thing'"
	err := OpenWorkspaceScopedWithCmd(c, spec, func(ctx ParentContext) (string, error) {
		capturedCtx = ctx
		return customCmd, nil
	})
	if err != nil {
		t.Fatalf("OpenWorkspaceScopedWithCmd: %v", err)
	}
	if c.createCmd != customCmd {
		t.Errorf("fn-returned cmd was not used. got %q, want %q", c.createCmd, customCmd)
	}
	if capturedCtx.SessionID != "$1" || capturedCtx.WindowID != "@2" {
		t.Errorf("fn received wrong ctx: %+v", capturedCtx)
	}
}

// TestOpenWorkspaceScopedWithCmd_FnError aborts the flow before any
// tmux state-changing call (no Ensure, no Attach). Important: a tool
// that can't compute its launch command (e.g., claudesettings.Ensure
// returns an unrecoverable error) shouldn't half-create a popup.
func TestOpenWorkspaceScopedWithCmd_FnError(t *testing.T) {
	c := newTrackingClient()
	spec := &WorkspaceScoped{Tool: "claude", DefaultCmd: "claude"}

	want := errors.New("settings file unwritable")
	err := OpenWorkspaceScopedWithCmd(c, spec, func(ParentContext) (string, error) {
		return "", want
	})
	if !errors.Is(err, want) {
		t.Errorf("expected fn error to propagate, got %v", err)
	}
	for _, call := range c.calls {
		if strings.HasPrefix(call, "NewSessionWithCommand ") || strings.HasPrefix(call, "Attach ") {
			t.Errorf("fn returned error but %s was still called: %v", call, c.calls)
		}
	}
}

// TestOpenWorkspaceScopedWithCmd_EmptyFnCmdFallsBackToDefault: returning
// ("", nil) from fn means "I have no special command, use spec.DefaultCmd."
// Useful for tools that conditionally inject a prompt — if the prompt
// is unset, they fall through to the default.
func TestOpenWorkspaceScopedWithCmd_EmptyFnCmdFallsBackToDefault(t *testing.T) {
	c := newTrackingClient()
	spec := &WorkspaceScoped{Tool: "t", DefaultCmd: "the-default"}

	err := OpenWorkspaceScopedWithCmd(c, spec, func(ParentContext) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatalf("OpenWorkspaceScopedWithCmd: %v", err)
	}
	if c.createCmd != "the-default" {
		t.Errorf("empty fn cmd should fall back to DefaultCmd. got %q want %q",
			c.createCmd, "the-default")
	}
}

func indexOfPrefix(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}
