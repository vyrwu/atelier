package workspaces

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/repoindex"
)

func TestParseWorkspacePlan(t *testing.T) {
	raw := "TITLE: Helm Chart Testing\nSLUG: helm-chart-testing\nTAG: infra\nREPOS: wawa/helm-charts, wawa/web-app"
	title, slug, tag, repos := parseWorkspacePlan(raw)
	if title != "Helm Chart Testing" {
		t.Errorf("title = %q", title)
	}
	if slug != "helm-chart-testing" {
		t.Errorf("slug = %q", slug)
	}
	if tag != "infra" {
		t.Errorf("tag = %q", tag)
	}
	if len(repos) != 2 || repos[0] != "wawa/helm-charts" || repos[1] != "wawa/web-app" {
		t.Errorf("repos = %v", repos)
	}
}

func TestParseWorkspacePlan_Tolerant(t *testing.T) {
	// Missing SLUG → derived from TITLE; unknown lines ignored; empty TAG/REPOS.
	raw := "chatter before\nTITLE: Fix Billing Webhook\nTAG:\nREPOS:\nnoise"
	title, slug, tag, repos := parseWorkspacePlan(raw)
	if title != "Fix Billing Webhook" {
		t.Errorf("title = %q", title)
	}
	if slug != "fix-billing-webhook" {
		t.Errorf("slug = %q", slug)
	}
	if tag != "" {
		t.Errorf("tag = %q, want empty", tag)
	}
	if len(repos) != 0 {
		t.Errorf("repos = %v, want empty", repos)
	}
}

func TestParseWorkspacePlan_SlugOnly(t *testing.T) {
	title, slug, _, _ := parseWorkspacePlan("SLUG: only-slug")
	if slug != "only-slug" || title != "only-slug" {
		t.Errorf("title=%q slug=%q; title should fall back to slug", title, slug)
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := map[string]string{
		"Helm Chart Testing": "helm-chart-testing",
		"  Foo/Bar!!  ":      "foo-bar",
		"already-kebab":      "already-kebab",
		"UPPER_snake.dots":   "upper-snake-dots",
		"---edges---":        "edges",
		"":                   "",
	}
	for in, want := range cases {
		if got := sanitizeSlug(in); got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeTag(t *testing.T) {
	for _, ph := range []string{"none", "None", "empty", "null", "nil", "na", "no-tag", "#none", ""} {
		if got := sanitizeTag(ph); got != "" {
			t.Errorf("sanitizeTag(%q) = %q, want empty", ph, got)
		}
	}
	if got := sanitizeTag("#Billing"); got != "billing" {
		t.Errorf("sanitizeTag(#Billing) = %q", got)
	}
	long := strings.Repeat("a", 40)
	if got := sanitizeTag(long); len(got) > 24 {
		t.Errorf("sanitizeTag long = %q (len %d), want <=24", got, len(got))
	}
}

func TestSplitRepos(t *testing.T) {
	got := splitRepos("wawa/a, wawa/b ,, none , wawa/c")
	want := []string{"wawa/a", "wawa/b", "wawa/c"}
	if len(got) != len(want) {
		t.Fatalf("splitRepos = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("splitRepos[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if r := splitRepos(""); r != nil {
		t.Errorf("splitRepos(empty) = %v, want nil", r)
	}
}

func TestFallbackNaming(t *testing.T) {
	title, slug := fallbackNaming("add dark mode toggle to the settings page")
	// Capped at 5 words.
	if slug != "add-dark-mode-toggle-to" {
		t.Errorf("slug = %q", slug)
	}
	if title != "add dark mode toggle to" {
		t.Errorf("title = %q", title)
	}
	title, slug = fallbackNaming("   ")
	if slug != "workspace" || title != "Workspace" {
		t.Errorf("empty intent: title=%q slug=%q", title, slug)
	}
}

func TestComposeCreationIntent(t *testing.T) {
	index := []repoindex.Repo{{Slug: "o/a"}, {Slug: "o/b"}}
	out := composeCreationIntent("do the thing", index, []string{"infra", "billing"})
	if !strings.Contains(out, "REPO INDEX:\no/a\no/b") {
		t.Errorf("missing repo index:\n%s", out)
	}
	if !strings.Contains(out, "EXISTING TAGS: infra, billing") {
		t.Errorf("missing tags:\n%s", out)
	}
	if !strings.HasSuffix(out, "INTENT: do the thing") {
		t.Errorf("missing intent suffix:\n%s", out)
	}
	// No index / no tags → "(none)".
	out = composeCreationIntent("x", nil, nil)
	if !strings.Contains(out, "REPO INDEX:\n(none)") || !strings.Contains(out, "EXISTING TAGS: (none)") {
		t.Errorf("empty index/tags not (none):\n%s", out)
	}
}

func TestTruncateIntent(t *testing.T) {
	if got := truncateIntent("short"); got != "short" {
		t.Errorf("truncateIntent short = %q", got)
	}
	long := strings.Repeat("x", intentMaxChars+50)
	got := truncateIntent(long)
	if len([]rune(got)) != intentMaxChars {
		t.Errorf("truncateIntent len = %d, want %d", len([]rune(got)), intentMaxChars)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateIntent should end with ellipsis")
	}
}
