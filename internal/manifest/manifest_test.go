package manifest

import (
	"strings"
	"testing"
)

func TestValidate_Ok(t *testing.T) {
	m := &Manifest{Name: "foo"}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
}

func TestValidate_MissingName(t *testing.T) {
	m := &Manifest{}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("Validate: expected name error, got %v", err)
	}
}

func TestValidate_BindingWithoutKey_AllowedForStyleOnly(t *testing.T) {
	// A Binding with no Key declares popup style only — used by the tool
	// selector to dispatch with the right popup geometry. initgen skips it.
	m := &Manifest{
		Name:    "foo",
		Binding: &Binding{Title: "Foo", Style: StyleFull},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: expected no error for style-only binding, got %v", err)
	}
}

func TestValidate_UnknownPopupKind(t *testing.T) {
	m := &Manifest{Name: "foo", Popup: "nonsense"}
	if err := m.Validate(); err == nil {
		t.Fatalf("Validate: expected error for unknown popup kind")
	}
}
