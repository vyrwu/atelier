package workspace

import "testing"

// TestSessionName guards the "cloudnativedenmark.dk breaks workspace creation"
// bug: tmux rewrites '.' and ':' to '_' in session names, so atelier must
// normalize the same way before using a derived name as a tmux target or a
// statestore key. See SessionName's doc comment.
func TestSessionName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cloudnativedenmark/cloudnativedenmark.dk", "cloudnativedenmark/cloudnativedenmark_dk"},
		{"vyrwu/atelier", "vyrwu/atelier"}, // no delimiters → unchanged
		{"a.b.c", "a_b_c"},
		{"owner/name:x", "owner/name_x"},
		{"owner/name_dk", "owner/name_dk"}, // already normalized → no-op
		{"", ""},
	}
	for _, c := range cases {
		if got := SessionName(c.in); got != c.want {
			t.Errorf("SessionName(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Idempotent: normalizing a tmux-origin (already normalized) name is safe,
	// which is what lets us apply it liberally at every derivation site.
	once := SessionName("cloudnativedenmark/cloudnativedenmark.dk")
	if twice := SessionName(once); twice != once {
		t.Errorf("SessionName not idempotent: %q -> %q", once, twice)
	}
}
