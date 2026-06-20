package popup_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/popup"
)

func TestWorkspaceScoped_SessionName(t *testing.T) {
	w := &popup.WorkspaceScoped{Tool: "lazygit"}
	got := w.SessionName("$12", "@34")
	want := "_atelier_lazygit_12_34"
	if got != want {
		t.Fatalf("SessionName: got %q want %q", got, want)
	}
}

func TestSessionGlobal_SessionName(t *testing.T) {
	s := &popup.SessionGlobal{Tool: "k8s"}
	if got, want := s.SessionName(), "_atelier_k8s"; got != want {
		t.Fatalf("SessionName: got %q want %q", got, want)
	}
}

func TestSessionPrefix_SharedAcrossTools(t *testing.T) {
	w := &popup.WorkspaceScoped{Tool: "lazygit"}
	s := &popup.SessionGlobal{Tool: "k8s"}
	if !strings.HasPrefix(w.SessionName("$0", "@1"), popup.SessionPrefix) {
		t.Fatalf("workspace-scoped name should start with %q", popup.SessionPrefix)
	}
	if !strings.HasPrefix(s.SessionName(), popup.SessionPrefix) {
		t.Fatalf("session-global name should start with %q", popup.SessionPrefix)
	}
}
