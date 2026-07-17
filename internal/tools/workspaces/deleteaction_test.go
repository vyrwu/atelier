package workspaces

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// A recap with an unbalanced backtick + open paren — the exact shape that
// made the old inline `echo "…{}…"` bind unparseable, killing both delete
// and enter for the workspace.
const backtickRecapLine = "wawafertility/wawa-helm-charts\tclinic-worker-migration-wait-timeout\tDISPLAY\t\n    · real & automatic in `rails runner`. caches `schem… (`us"

func TestFzfDeleteAction_ConfirmDropsRecapAndBuildsSafeAction(t *testing.T) {
	got := fzfDeleteAction("enter", "Confirm? y/n: ", backtickRecapLine, "_delete-row", "_session-list", sessionsPromptGlyph)

	if !strings.HasPrefix(got, "execute-silent(") {
		t.Fatalf("want execute-silent action, got %q", got)
	}
	if !strings.Contains(got, "reload(") || !strings.Contains(got, "change-prompt("+sessionsPromptGlyph+")") {
		t.Fatalf("missing reload/change-prompt: %q", got)
	}
	// The free-form recap (and its shell/fzf metacharacters) must never
	// reach the emitted action — that leak was the whole bug.
	for _, leak := range []string{"`", "rails runner", "schem", "(`us"} {
		if strings.Contains(got, leak) {
			t.Fatalf("recap leaked %q into action: %q", leak, got)
		}
	}
	// Only the clean session\twindow pair is embedded, shell-quoted.
	wantRow := shellSingleQuote("wawafertility/wawa-helm-charts\tclinic-worker-migration-wait-timeout")
	if !strings.Contains(got, wantRow) {
		t.Fatalf("want embedded row %q in %q", wantRow, got)
	}
}

func TestFzfDeleteAction_Branches(t *testing.T) {
	line := "repo\tbranch\tDISPLAY"
	reset := "change-prompt(" + sessionsPromptGlyph + ")"
	cases := []struct{ key, prompt, want string }{
		{"y", sessionsPromptGlyph, "put(y)"},
		{"enter", sessionsPromptGlyph, "accept"},
		{"y", "Cannot delete — close attached workspaces first. ", reset},
		{"enter", "Cannot delete. ", reset},
	}
	for _, c := range cases {
		if got := fzfDeleteAction(c.key, c.prompt, line, "_delete-row", "_session-list", sessionsPromptGlyph); got != c.want {
			t.Errorf("key=%q prompt=%q: got %q want %q", c.key, c.prompt, got, c.want)
		}
	}
}

func TestFzfDeleteAction_MalformedRowDoesNotDelete(t *testing.T) {
	got := fzfDeleteAction("enter", "Confirm? y/n: ", "onlyonefield", "_delete-row", "_session-list", sessionsPromptGlyph)
	if strings.Contains(got, "execute-silent") {
		t.Fatalf("malformed row must not trigger delete: %q", got)
	}
	if got != "change-prompt("+sessionsPromptGlyph+")" {
		t.Fatalf("want prompt reset, got %q", got)
	}
}

func TestFzfDeleteAction_RecoverUsesRecoverSubs(t *testing.T) {
	got := fzfDeleteAction("enter", "Confirm? y/n: ", "repo\tbranch\tDISP", "_recover-delete-row", "_recover-rows", recoverPromptGlyph)
	if !strings.Contains(got, "_recover-delete-row") || !strings.Contains(got, "_recover-rows") {
		t.Fatalf("recover subs missing: %q", got)
	}
	if !strings.Contains(got, "change-prompt("+recoverPromptGlyph+")") {
		t.Fatalf("recover glyph missing: %q", got)
	}
}

// shellSingleQuote must survive a real shell round-trip: backticks and
// $() inside the quoted string stay literal (never command-substituted).
func TestShellSingleQuote_NeutralizesShellMetacharacters(t *testing.T) {
	payload := "a`whoami`b$(id)c\tbranch/name"
	out, err := exec.Command("/bin/sh", "-c", "printf %s "+shellSingleQuote(payload)).Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if string(out) != payload {
		t.Fatalf("shell mangled payload: got %q want %q", out, payload)
	}
}

// DeleteActionCommand receives {} as a single (fzf-single-quoted) argument,
// so a backtick-laden recap reaches Go untouched — no shell interpretation,
// and the recap is dropped from the emitted action.
func TestDeleteActionCommand_EmitsSafeAction(t *testing.T) {
	cmd := DeleteActionCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"enter", "Confirm? y/n: ", "repo\tbranch\tDISPLAY\t· recap `x` $(boom)"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "execute-silent(") {
		t.Fatalf("want execute-silent, got %q", out)
	}
	if strings.ContainsAny(out, "`") || strings.Contains(out, "boom") {
		t.Fatalf("recap metacharacters leaked into action: %q", out)
	}
}
