package workspaces

import (
	"reflect"
	"strings"
	"testing"
)

// TestTagColor_StableAndInPalette: the color is a pure function of the tag
// name — same name always maps to the same palette entry regardless of
// call order — and it always lands inside the curated palette.
func TestTagColor_StableAndInPalette(t *testing.T) {
	inPalette := func(c string) bool {
		for _, p := range tagPalette {
			if p == c {
				return true
			}
		}
		return false
	}
	names := []string{"client-x", "infra", "spike", "billing", "", "a", "zzzzz"}
	for _, n := range names {
		c1 := tagColor(n)
		c2 := tagColor(n)
		if c1 != c2 {
			t.Errorf("tagColor(%q) not stable: %q vs %q", n, c1, c2)
		}
		if !inPalette(c1) {
			t.Errorf("tagColor(%q) = %q not in palette", n, c1)
		}
	}
}

func TestRenderTagPill(t *testing.T) {
	if got := renderTagPill(""); got != "" {
		t.Errorf("empty tag → %q, want empty", got)
	}
	got := renderTagPill("infra")
	if !strings.HasPrefix(got, " ") {
		t.Errorf("pill must be spliceable (leading space): %q", got)
	}
	if !strings.Contains(got, "#infra") {
		t.Errorf("pill must contain the #-prefixed name: %q", got)
	}
	if !strings.Contains(got, "\033[3;38;5;"+tagColor("infra")+"m") {
		t.Errorf("pill must be italic + carry the tag's stable color: %q", got)
	}
}

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

func TestResolveTagChoice(t *testing.T) {
	cases := []struct {
		selection, query, want string
	}{
		{"infra", "inf", "infra"},         // matched existing tag wins over query
		{"", "new-tag", "new-tag"},        // typed, no match → create
		{"", "", ""},                      // empty → clear
		{"  ", "  ", ""},                  // whitespace-only → clear
		{"", "#billing", "billing"},       // strip a leading '#'
		{"", "two words", "two-words"},    // collapse interior spaces
		{"  infra  ", "", "infra"},        // selection trimmed
		{clearTagLabel, "", ""},           // clear sentinel → clear
		{clearTagLabel, "infra", ""},      // clear sentinel wins over any query
		{"#billing", "", "billing"},       // list rows return "#tag" → strip the '#'
		{"#infra (current)", "", "infra"}, // active row is annotated → drop marker
	}
	for _, c := range cases {
		if got := resolveTagChoice(c.selection, c.query); got != c.want {
			t.Errorf("resolveTagChoice(%q,%q) = %q, want %q", c.selection, c.query, got, c.want)
		}
	}
}

func TestTagPickerItems(t *testing.T) {
	tags := []string{"billing", "infra"}
	strip := func(items []string) []string {
		out := make([]string, len(items))
		for i, s := range items {
			out[i] = ansiRE.ReplaceAllString(s, "")
		}
		return out
	}

	// No current tag → no clear row; each tag rendered as a "#tag" pill.
	got := tagPickerItems("", tags)
	if want := []string{"#billing", "#infra"}; !reflect.DeepEqual(strip(got), want) {
		t.Errorf("untagged items (stripped) = %v, want %v", strip(got), want)
	}

	// Current tag → red clear row is first (the highlighted default on empty
	// query), then the pills; the active tag is annotated "(current)".
	got = tagPickerItems("infra", tags)
	if want := []string{clearTagLabel, "#billing", "#infra" + currentMarker}; !reflect.DeepEqual(strip(got), want) {
		t.Errorf("tagged items (stripped) = %v, want %v", strip(got), want)
	}
	if !strings.Contains(got[0], "\033[38;5;"+clearTagColor+"m") {
		t.Errorf("clear row must be red: %q", got[0])
	}
	if !strings.Contains(got[1], "\033[3;38;5;"+tagColor("billing")+"m") {
		t.Errorf("tag row must carry the tag's italic pill color: %q", got[1])
	}
}

// TestFormatTagPreview covers the M-t preview's focus states: a pending tag
// (typed or hovered) renders its live pill leading "branch repo"; the clear-tag
// row and the empty state both render "branch repo" with no pill (the tag
// disappears, previewing removal).
func TestFormatTagPreview(t *testing.T) {
	// Typed new tag → its pill leads, colored with the tag's stable color;
	// order is pill < branch < repo.
	got := formatTagPreview("mybranch", "myrepo", "36", "client-x", "")
	vis := ansiRE.ReplaceAllString(got, "")
	iPill := strings.Index(vis, "#client-x")
	iBranch := strings.Index(vis, "mybranch")
	iRepo := strings.Index(vis, "myrepo")
	if iPill < 0 || iBranch <= iPill || iRepo <= iBranch {
		t.Errorf("want pill<branch<repo, got pill@%d branch@%d repo@%d in %q", iPill, iBranch, iRepo, vis)
	}
	if !strings.Contains(got, "\033[3;38;5;"+tagColor("client-x")+"m") {
		t.Errorf("typed tag must render its live pill color: %q", got)
	}

	// Hovering an existing tag row (no query) resolves to that tag's pill.
	if got := formatTagPreview("mybranch", "myrepo", "36", "", "#billing"); !strings.Contains(got, "#billing") ||
		!strings.Contains(got, "\033[3;38;5;"+tagColor("billing")+"m") {
		t.Errorf("hovered tag row must preview that tag's pill: %q", got)
	}

	// Clear-tag row focused → tag disappears: no pill, just "branch repo".
	got = formatTagPreview("mybranch", "myrepo", "36", "", clearTagLabel)
	if vis := ansiRE.ReplaceAllString(got, ""); strings.Contains(vis, "#") || vis != "Preview: mybranch myrepo" {
		t.Errorf("clear preview must drop the pill, got %q", vis)
	}

	// Nothing resolvable → no pill, just the "Preview:" label + "branch repo".
	got = formatTagPreview("mybranch", "myrepo", "36", "", "")
	if vis := ansiRE.ReplaceAllString(got, ""); strings.Contains(vis, "#") || vis != "Preview: mybranch myrepo" {
		t.Errorf("empty preview must be %q, got %q", "Preview: mybranch myrepo", vis)
	}
}
