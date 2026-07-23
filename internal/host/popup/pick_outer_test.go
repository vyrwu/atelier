package popup

import "testing"

// TestPickOuter guards the "tools won't launch with two clients attached"
// bug: OpenOnOuter must target the client the user actually drove
// (@atelier_outer_client), not whichever client tmux lists first. With a
// second terminal attached to the same session, the naive first-client
// choice opened every popup on the wrong terminal.
func TestPickOuter(t *testing.T) {
	cases := []struct {
		name      string
		outers    []string
		preferred string
		want      string
	}{
		{
			name:      "preferred client attached → chosen over first",
			outers:    []string{"/dev/ttys000", "/dev/ttys049"},
			preferred: "/dev/ttys049",
			want:      "/dev/ttys049",
		},
		{
			name:      "preferred is first → still chosen",
			outers:    []string{"/dev/ttys049", "/dev/ttys000"},
			preferred: "/dev/ttys049",
			want:      "/dev/ttys049",
		},
		{
			name:      "preferred not attached → fall back to first",
			outers:    []string{"/dev/ttys000", "/dev/ttys049"},
			preferred: "/dev/ttys099",
			want:      "/dev/ttys000",
		},
		{
			name:      "no preferred → first",
			outers:    []string{"/dev/ttys000", "/dev/ttys049"},
			preferred: "",
			want:      "/dev/ttys000",
		},
		{
			name:      "single client, no preferred",
			outers:    []string{"/dev/ttys000"},
			preferred: "",
			want:      "/dev/ttys000",
		},
		{
			name:      "no clients → empty",
			outers:    nil,
			preferred: "/dev/ttys049",
			want:      "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickOuter(c.outers, c.preferred); got != c.want {
				t.Errorf("pickOuter(%v, %q) = %q, want %q", c.outers, c.preferred, got, c.want)
			}
		})
	}
}
