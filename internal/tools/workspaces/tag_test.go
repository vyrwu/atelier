package workspaces

import (
	"reflect"
	"testing"
)

func TestParseTagList(t *testing.T) {
	// Dedupes, drops empties/whitespace, sorts.
	out := []byte("infra\n\nclient-x\ninfra\n  spike  \n\n")
	got := parseTagList(out)
	want := []string{"client-x", "infra", "spike"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTagList = %v, want %v", got, want)
	}
	if got := parseTagList([]byte("   \n\n")); len(got) != 0 {
		t.Errorf("all-empty → %v, want []", got)
	}
}

func TestNormalizeTag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"infra", "infra"},
		{"  infra  ", "infra"},
		{"#billing", "billing"},
		{"two words", "two-words"},
		{"  multi   word  tag ", "multi-word-tag"},
	}
	for _, c := range cases {
		if got := normalizeTag(c.in); got != c.want {
			t.Errorf("normalizeTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
