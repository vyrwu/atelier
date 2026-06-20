package popup_test

import (
	"reflect"
	"testing"

	"github.com/vyrwu/atelier/internal/popup"
)

// recorderClient is a fake popup.Client that captures every Run() call.
// Used to assert the exact tmux command sequence the popup helpers emit
// without spinning up a real tmux server.
type recorderClient struct {
	runs [][]string
}

func (r *recorderClient) Run(args ...string) ([]byte, error) {
	r.runs = append(r.runs, append([]string(nil), args...))
	return nil, nil
}
func (r *recorderClient) HasSession(string) (bool, error)              { return false, nil }
func (r *recorderClient) ShowGlobalOption(string) (string, error)      { return "", nil }
func (r *recorderClient) DisplayMessage(string) (string, error)        { return "", nil }
func (r *recorderClient) DisplayMessageAt(string, string) (string, error) {
	return "", nil
}
func (r *recorderClient) NewSessionWithCommand(string, string) error { return nil }
func (r *recorderClient) KillSession(string) error                    { return nil }
func (r *recorderClient) Attach(string) error                         { return nil }

// TestApplyStyle_EmitsCanonicalOptionSequence locks in the canonical
// popup style: five set-option calls, in this exact order, with these
// exact flags. Five tools (claude/lazygit/k8s/pg/popupshell) used to
// copy-paste this block — drift in any one was a silent UX bug. This
// test guards the extracted helper against regression.
func TestApplyStyle_EmitsCanonicalOptionSequence(t *testing.T) {
	r := &recorderClient{}
	popup.ApplyStyle(r, "_atelier_test_0_0")

	want := [][]string{
		{"set-option", "-s", "-t", "_atelier_test_0_0", "key-table", "popup"},
		{"set-option", "-s", "-t", "_atelier_test_0_0", "status", "off"},
		{"set-option", "-s", "-t", "_atelier_test_0_0", "prefix", "None"},
		{"set-option", "-s", "-t", "_atelier_test_0_0", "prefix2", "None"},
		{"set-option", "-g", "-t", "_atelier_test_0_0", "aggressive-resize", "on"},
	}
	if !reflect.DeepEqual(r.runs, want) {
		t.Errorf("ApplyStyle emitted wrong sequence.\n got: %v\nwant: %v", r.runs, want)
	}
}

// TestApplyStyle_SessionScopeForBehaviorOptions asserts that the four
// behavior options use server-scope `-s` (server-side option, affecting
// every client viewing that session) while the resize option uses
// global `-g` scope — matching bash's choice. This isn't an aesthetic
// detail: `-s` is required for `key-table` to actually intercept popup
// keys, and `-g` is required for `aggressive-resize` to apply to the
// popup pane (a window-scoped value would never take effect).
func TestApplyStyle_SessionScopeForBehaviorOptions(t *testing.T) {
	r := &recorderClient{}
	popup.ApplyStyle(r, "session")

	for i, run := range r.runs {
		if len(run) < 2 {
			t.Fatalf("call %d: too few args: %v", i, run)
		}
		opt := run[len(run)-2]
		flag := run[1]
		wantFlag := "-s"
		if opt == "aggressive-resize" {
			wantFlag = "-g"
		}
		if flag != wantFlag {
			t.Errorf("set-option %s wants flag %q, got %q (call: %v)", opt, wantFlag, flag, run)
		}
	}
}
