//go:build e2e

package workspace_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

func TestList_ReturnsCurrentWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	workspaces, err := workspace.List(srv.Client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, w := range workspaces {
		if w.Session == "work" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'work' session in list, got %+v", workspaces)
	}
}

func TestList_FiltersAtelierPopups(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")
	if err := srv.Client.NewSession("_atelier_popupshell_1_2", true); err != nil {
		t.Fatalf("create atelier popup: %v", err)
	}

	workspaces, err := workspace.List(srv.Client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, w := range workspaces {
		if strings.HasPrefix(w.Session, "_atelier_") {
			t.Fatalf("List returned atelier-managed session: %+v", w)
		}
	}
}

func TestInfo_OnRegularWorkspace(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	w, err := workspace.Info(srv.Client, "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if w.Session != "work" {
		t.Fatalf("expected session 'work', got %q", w.Session)
	}
	if w.PaneID == "" || w.WindowID == "" {
		t.Fatalf("expected non-empty IDs, got %+v", w)
	}
}

func TestSetAttention_RoundTrip(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	w, _ := workspace.Info(srv.Client, "")
	if err := workspace.SetAttention(srv.Client, w.WindowID, true); err != nil {
		t.Fatalf("SetAttention on: %v", err)
	}
	again, _ := workspace.Info(srv.Client, "")
	if !again.Attention {
		t.Fatalf("expected Attention=true after SetAttention")
	}
	if err := workspace.SetAttention(srv.Client, w.WindowID, false); err != nil {
		t.Fatalf("SetAttention off: %v", err)
	}
	cleared, _ := workspace.Info(srv.Client, "")
	if cleared.Attention {
		t.Fatalf("expected Attention=false after clear")
	}
}
