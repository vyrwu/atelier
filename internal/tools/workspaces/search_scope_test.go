package workspaces

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestPickerSearchScope_NameNotRecap drives the real fzf binary with the exact
// flag combination the M-s picker uses (--with-nth=3,4 --nth=1) to prove the
// search matches the workspace NAME line and never the recap line. fzf renumbers
// fields for --nth relative to the --with-nth projection, so this is easy to get
// wrong; the guard runs fzf in non-interactive --filter mode (no pty).
func TestPickerSearchScope_NameNotRecap(t *testing.T) {
	if _, err := exec.LookPath("fzf"); err != nil {
		t.Skip("fzf not on PATH")
	}

	// Two records in the picker's on-the-wire format:
	// session \t window \t <name-line> \t <recap-line>, NUL-terminated.
	rec := func(session, window, recap, tag string) string {
		name := formatSessionDisplay("5m ", "○ ", "  ", "", "36", session, window, tag)
		return fmt.Sprintf("%s\t%s\t%s\t%s\x00", session, window, name,
			formatRecapLine(recap, recapIndentCells(false)))
	}
	stdin := rec("vyrwu/atelier", "fzf-multiline", "authenticate the user", "billing") +
		rec("api", "widgets", "fix the login page", "")

	filter := func(query string) []string {
		cmd := exec.Command("fzf", "--read0", "--print0", "--ansi",
			"--delimiter=\t", "--with-nth=3,4", "--nth=1", "--filter="+query)
		cmd.Stdin = strings.NewReader(stdin)
		var out bytes.Buffer
		cmd.Stdout = &out
		_ = cmd.Run() // exit 1 on no match — out stays empty
		var got []string
		for _, r := range strings.Split(out.String(), "\x00") {
			if f := strings.SplitN(r, "\t", 3); len(f) >= 2 && f[0] != "" {
				got = append(got, f[0]+"/"+f[1])
			}
		}
		return got
	}

	cases := []struct {
		query     string
		wantMatch bool // whether the matching record should appear
		where     string
	}{
		{"multiline", true, "window name"},
		{"atelier", true, "session name"},
		{"widgets", true, "window name"},
		{"billing", true, "tag pill — searchable"},
		{"#billing", true, "tag pill with # — searchable"},
		{"authenticate", false, "recap only — must NOT match"},
		{"login", false, "recap only — must NOT match"},
	}
	for _, c := range cases {
		got := filter(c.query)
		if c.wantMatch && len(got) == 0 {
			t.Errorf("query %q (%s): expected a match, got none", c.query, c.where)
		}
		if !c.wantMatch && len(got) != 0 {
			t.Errorf("query %q (%s): recap must not be searchable, but matched %v", c.query, c.where, got)
		}
	}
}
