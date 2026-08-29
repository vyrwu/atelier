package notify

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestToast_TimeoutQuits(t *testing.T) {
	_, cmd := newToast(Info, "hi").Update(timeoutMsg{})
	if cmd == nil {
		t.Fatal("timeout should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("timeout should quit; got %T", cmd())
	}
}

func TestToast_AnyKeyDismisses(t *testing.T) {
	_, cmd := newToast(Info, "hi").Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cmd == nil {
		t.Fatal("a keypress should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("a keypress should dismiss (quit); got %T", cmd())
	}
}

func TestToast_ViewRendersMessageAndIcon(t *testing.T) {
	m := newToast(Error, "workspace create failed")
	m.width, m.height = 60, 12
	v := m.View()
	if !strings.Contains(v, "workspace create failed") {
		t.Errorf("view should show the message; got:\n%s", v)
	}
	if !strings.Contains(v, "✗") {
		t.Errorf("error toast should show the ✗ icon; got:\n%s", v)
	}
}

func TestToastStyle_DistinctPerKind(t *testing.T) {
	seen := map[string]Kind{}
	for _, k := range []Kind{Info, Success, Error} {
		icon, color := toastStyle(k)
		if icon == "" || color == "" {
			t.Errorf("kind %d has empty icon/color", k)
		}
		if prev, ok := seen[icon+color]; ok {
			t.Errorf("kinds %d and %d share icon+color", prev, k)
		}
		seen[icon+color] = k
	}
}
