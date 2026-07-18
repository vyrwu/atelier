package workspaces

import (
	"strings"
	"testing"
)

func TestParseNameAndTag(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantName string
		wantTag  string
	}{
		{"single line no tag", "feat/dark-mode", "feat/dark-mode", ""},
		{"two lines", "feat/billing-webhook-500s\nbilling", "feat/billing-webhook-500s", "billing"},
		{"blank second line", "feat/foo\n", "feat/foo", ""},
		{"blank line then tag", "feat/foo\n\nbilling", "feat/foo", "billing"},
		{"trailing commentary ignored", "feat/foo\nbilling\nHope that helps!", "feat/foo", "billing"},
		{"surrounding whitespace", "  feat/foo  \n  billing  ", "feat/foo", "billing"},
		{"crlf", "feat/foo\r\nbilling\r\n", "feat/foo", "billing"},
		{"empty", "", "", ""},
		{"whitespace only", "   \n   ", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotTag := parseNameAndTag(tc.raw)
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q", gotName, tc.wantName)
			}
			if gotTag != tc.wantTag {
				t.Errorf("tag = %q, want %q", gotTag, tc.wantTag)
			}
		})
	}
}

func TestSanitizeAutoTag(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"clean slug", "billing", "billing"},
		{"uppercase lowered", "Billing", "billing"},
		{"spaces to hyphens", "acme corp", "acme-corp"},
		{"leading hash stripped", "#client-x", "client-x"},
		{"invalid chars collapsed", "acme_corp/2!", "acme-corp-2"},
		{"edges trimmed", "  --billing--  ", "billing"},
		{"empty", "", ""},
		{"placeholder none", "none", ""},
		{"placeholder empty word", "empty", ""},
		{"placeholder no-tag", "no tag", ""},
		{"placeholder nil", "nil", ""},
		{"length capped", strings.Repeat("a", 40), strings.Repeat("a", autoTagMaxLen)},
		{"length cap trims dangling hyphen", strings.Repeat("ab-", 20), strings.TrimRight(strings.Repeat("ab-", 20)[:autoTagMaxLen], "-")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeAutoTag(tc.raw); got != tc.want {
				t.Errorf("sanitizeAutoTag(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSanitizeAutoTag_LengthCapBounds(t *testing.T) {
	if got := sanitizeAutoTag(strings.Repeat("z", 100)); len(got) > autoTagMaxLen {
		t.Errorf("len = %d, want <= %d", len(got), autoTagMaxLen)
	}
}

func TestComposeNamingIntent(t *testing.T) {
	t.Run("with tags", func(t *testing.T) {
		got := composeNamingIntent("fix the thing", []string{"billing", "infra"})
		want := "EXISTING TAGS: billing, infra\nINTENT: fix the thing"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("no tags renders none", func(t *testing.T) {
		got := composeNamingIntent("fix the thing", nil)
		want := "EXISTING TAGS: (none)\nINTENT: fix the thing"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("intent truncated", func(t *testing.T) {
		long := strings.Repeat("x", branchPromptMaxChars+50)
		got := composeNamingIntent(long, nil)
		intent := strings.TrimPrefix(got, "EXISTING TAGS: (none)\nINTENT: ")
		if len([]rune(intent)) != branchPromptMaxChars {
			t.Errorf("intent rune len = %d, want %d", len([]rune(intent)), branchPromptMaxChars)
		}
		if !strings.HasSuffix(intent, "…") {
			t.Errorf("truncated intent should end with ellipsis, got %q", intent[len(intent)-10:])
		}
	})
}
