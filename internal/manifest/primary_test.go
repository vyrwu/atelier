package manifest

import "testing"

func TestPrimary_ExplicitPrimaryInvoke(t *testing.T) {
	m := &Manifest{Name: "pg", PrimaryInvoke: "pgcli"}
	if got := m.Primary(); got != "pgcli" {
		t.Fatalf("Primary: got %q want pgcli", got)
	}
}

func TestPrimary_FallsBackToBindingInvoke(t *testing.T) {
	m := &Manifest{
		Name:    "workspaces",
		Binding: &Binding{Key: "M-n", Invoke: "pick"},
	}
	if got := m.Primary(); got != "pick" {
		t.Fatalf("Primary: got %q want pick", got)
	}
}

func TestPrimary_DefaultsToOpen(t *testing.T) {
	m := &Manifest{Name: "lazygit"}
	if got := m.Primary(); got != "open" {
		t.Fatalf("Primary: got %q want open", got)
	}
}
