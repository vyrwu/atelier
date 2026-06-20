package manifest

import (
	"strings"
	"testing"
)

func TestValidate_Ok(t *testing.T) {
	m := &Manifest{APIVersion: APIVersion, Name: "foo"}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
}

func TestValidate_MissingAPIVersion(t *testing.T) {
	m := &Manifest{Name: "foo"}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "api_version") {
		t.Fatalf("Validate: expected api_version error, got %v", err)
	}
}

func TestValidate_WrongAPIVersion(t *testing.T) {
	m := &Manifest{APIVersion: 999, Name: "foo"}
	if err := m.Validate(); err == nil {
		t.Fatalf("Validate: expected error for wrong api_version")
	}
}

func TestValidate_MissingName(t *testing.T) {
	m := &Manifest{APIVersion: APIVersion}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("Validate: expected name error, got %v", err)
	}
}

func TestValidate_BindingWithoutKey_AllowedForStyleOnly(t *testing.T) {
	// A Binding with no Key declares popup style only — used by the tool
	// selector to dispatch with the right popup geometry. initgen skips it.
	m := &Manifest{
		APIVersion: APIVersion, Name: "foo",
		Binding: &Binding{Title: "Foo", Style: StyleFull},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: expected no error for style-only binding, got %v", err)
	}
}

func TestValidate_UnknownPopupKind(t *testing.T) {
	m := &Manifest{APIVersion: APIVersion, Name: "foo", Popup: "nonsense"}
	if err := m.Validate(); err == nil {
		t.Fatalf("Validate: expected error for unknown popup kind")
	}
}

func TestFromJSON_RoundTrip(t *testing.T) {
	src := &Manifest{
		APIVersion:  APIVersion,
		Name:        "test",
		Description: "test description",
		Binding:     &Binding{Key: "x", Style: StyleFull, Title: "Test"},
		Popup:       KindWorkspace,
		Requires:    []string{"git", "fzf"},
	}
	data, err := src.AsJSON()
	if err != nil {
		t.Fatalf("AsJSON: %v", err)
	}
	parsed, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if parsed.Name != src.Name || parsed.Popup != src.Popup {
		t.Fatalf("round-trip mismatch: got %+v want %+v", parsed, src)
	}
}

func TestFromJSON_InvalidJSON(t *testing.T) {
	if _, err := FromJSON([]byte("not json")); err == nil {
		t.Fatalf("expected error parsing invalid JSON")
	}
}
