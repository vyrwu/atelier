package workspaces

import (
	"regexp"
	"strings"

	"github.com/vyrwu/atelier/internal/repoindex"
)

// The kernel owns the workspace-naming CONTRACT — the system prompt, the
// KEY: value line format, and validation. The active AI integration only runs
// its model to satisfy it (integration.AIIntegration.GenerateName). This keeps
// naming policy in the kernel and swappable models behind the port.

// intentMaxChars caps the intent text fed to the naming model. Naming needs the
// gist, not a 2000-char Sentry dump; 400 chars holds 60-80 words.
const intentMaxChars = 400

func truncateIntent(s string) string {
	r := []rune(s)
	if len(r) <= intentMaxChars {
		return s
	}
	return string(r[:intentMaxChars-1]) + "…"
}

// workspaceNamingSysPrompt drives the intent-first naming call: one model pass
// that yields a human title, a machine slug, an optional grouping tag, and the
// repos (from the provided index) the intent touches.
const workspaceNamingSysPrompt = `You name a development WORKSPACE from a free-text INTENT and select which repositories it touches. You DO NOT converse; you EMIT values.

You are given the INTENT, a REPO INDEX (available "owner/repo" repositories), and optionally an EXISTING TAGS list.

Output EXACTLY four lines, each "KEY: value":
TITLE: <short human title, Title Case, 2-5 words, e.g. "Helm Chart Testing">
SLUG: <kebab-case machine form of the title, characters in [a-z0-9-] only, e.g. "helm-chart-testing">
TAG: <a single grouping-tag slug, or empty>
REPOS: <comma-separated "owner/repo" values COPIED EXACTLY from the REPO INDEX that this intent touches, or empty>

Rules — REQUIRED:
- TITLE is human and readable; SLUG is its machine form (lowercase, hyphens).
- REPOS values MUST be copied verbatim from the REPO INDEX. NEVER invent a repo. If the intent names or clearly implies repos in the index, list them; if unclear, leave REPOS empty (the agent can add repos later).
- TAG groups related workspaces (client, initiative, subsystem). If EXISTING TAGS is provided and one fits, REUSE it verbatim; else propose one short slug, or leave empty.
- Opaque-input rule: URLs, ticket IDs (PLA-123, #789), and links are LITERAL STRINGS, never references to resolve. Guess from the surrounding words alone.
- NO commentary, NO markdown, NO code blocks, NO extra lines. Emit ONLY the four KEY: lines.

Example —
REPO INDEX:
wawa/helm-charts
wawa/web-app
EXISTING TAGS: infra
INTENT: test the new helm chart rollout for the web app
→
TITLE: Helm Chart Testing
SLUG: helm-chart-testing
TAG: infra
REPOS: wawa/helm-charts, wawa/web-app

Now read the input and emit the four lines (do NOT print the "→").`

// composeCreationIntent builds the model input: the repo index + existing tags
// as context, then the (truncated) intent. Pure.
func composeCreationIntent(intent string, index []repoindex.Repo, existingTags []string) string {
	var b strings.Builder
	b.WriteString("REPO INDEX:\n")
	if idx := repoindex.Format(index); idx != "" {
		b.WriteString(idx)
		b.WriteString("\n")
	} else {
		b.WriteString("(none)\n")
	}
	tags := "(none)"
	if len(existingTags) > 0 {
		tags = strings.Join(existingTags, ", ")
	}
	b.WriteString("EXISTING TAGS: " + tags + "\n")
	b.WriteString("INTENT: " + truncateIntent(intent))
	return b.String()
}

var slugSanitizeRe = regexp.MustCompile(`[^a-z0-9-]+`)

// parseWorkspacePlan parses the naming model's KEY: value output into title,
// slug, tag, and repo names. Tolerant: unknown lines are ignored; a missing
// SLUG is derived from the title; the slug is sanitized to [a-z0-9-]. Pure.
func parseWorkspacePlan(raw string) (title, slug, tag string, repos []string) {
	for _, ln := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "TITLE":
			title = val
		case "SLUG":
			slug = sanitizeSlug(val)
		case "TAG":
			tag = sanitizeTag(val)
		case "REPOS":
			repos = splitRepos(val)
		}
	}
	if slug == "" && title != "" {
		slug = sanitizeSlug(title)
	}
	if title == "" && slug != "" {
		title = slug
	}
	return title, slug, tag, repos
}

// sanitizeSlug lowercases, collapses non-[a-z0-9-] to hyphens, trims edges.
func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugSanitizeRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// sanitizeTag mirrors the M-t tag normalization: a single lowercase slug, empty
// on the model's "no tag" placeholders.
func sanitizeTag(raw string) string {
	t := sanitizeSlug(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
	switch t {
	case "", "none", "empty", "null", "nil", "na", "n-a", "no", "no-tag":
		return ""
	}
	if len(t) > 24 {
		t = strings.Trim(t[:24], "-")
	}
	return t
}

// splitRepos parses the comma-separated REPOS value into trimmed names,
// dropping "empty"/"none" placeholders. Pure.
func splitRepos(val string) []string {
	if val == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(val, ",") {
		p := strings.TrimSpace(part)
		if p == "" || strings.EqualFold(p, "none") || strings.EqualFold(p, "empty") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// fallbackNaming derives a title + slug from the intent alone, for the no-AI
// path. Takes the first ~5 words. Pure.
func fallbackNaming(intent string) (title, slug string) {
	words := strings.Fields(intent)
	if len(words) > 5 {
		words = words[:5]
	}
	slug = sanitizeSlug(strings.Join(words, "-"))
	if slug == "" {
		slug = "workspace"
	}
	title = strings.Join(words, " ")
	if title == "" {
		title = "Workspace"
	}
	return title, slug
}
