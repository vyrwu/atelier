package workspaces

import (
	"sort"
	"strings"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/tui"
	"github.com/vyrwu/atelier/internal/workspace"
)

// Workspace tagging (M-t in the session picker): a user-assigned label that
// groups workspaces across repos/branches. The tag is stored as a per-window
// tmux option (workspace.OptWorkspaceTag, the source of truth) and rendered
// as a stable-colored pill (tui.TagPill hashes the name → color) after the
// window name, so the eye can cluster related workspaces at a glance.

// runTagPrompt is the M-t flow: a text prompt (pre-filled with the current
// tag, existing tags listed as reuse hints) that tags the selected workspace.
// Empty input clears the tag. Returns tui.ErrCancelled on Esc so the caller
// reopens the picker unchanged.
func runTagPrompt(h *tmuxhost.Client, session, window string) error {
	windowID, err := h.DisplayMessageAt(session+":"+window, "#{window_id}")
	if err != nil || windowID == "" {
		debuglog.Logf("workspaces.tag: no window id for %s/%s: %v", session, window, err)
		return nil
	}
	current, _ := h.GetWindowOption(windowID, workspace.OptWorkspaceTag)

	header := "empty input clears the tag"
	if existing := collectTags(h); len(existing) > 0 {
		header = "reuse: " + strings.Join(existing, " · ")
	}

	outcome, err := tui.Run(tui.NewPrompt(tui.TagTheme(), tui.PromptConfig{
		Title:       " Tag Workspace ",
		Glyph:       "宛 ",
		Placeholder: "tag name",
		Initial:     current,
		Header:      header,
	}))
	if err != nil {
		return err // ErrCancelled bubbles up; the picker loop ignores it
	}
	chosen := normalizeTag(outcome.Query)
	if chosen == current {
		return nil
	}
	if err := workspace.SetTag(h, windowID, chosen); err != nil {
		debuglog.LogErr("workspaces.tag", err)
		return err
	}
	debuglog.Logf("workspaces.tag: %s/%s (%s) tag=%q (was %q)", session, window, windowID, chosen, current)
	return nil
}

// collectTags returns every distinct tag currently assigned across all
// windows — the tag registry the M-t prompt offers as reuse hints. The live
// tmux state is the registry: no separate file to keep in sync.
func collectTags(h *tmuxhost.Client) []string {
	out, err := h.Run("list-windows", "-a", "-F", "#{"+workspace.OptWorkspaceTag+"}")
	if err != nil {
		return nil
	}
	return parseTagList(out)
}

// parseTagList extracts the sorted, de-duplicated set of non-empty tags from
// `list-windows -a -F #{@workspace_tag}` output. Pure.
func parseTagList(out []byte) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// normalizeTag trims a raw typed tag and collapses interior whitespace to
// single hyphens (tags are single tokens so the pill stays clean). A leading
// `#` the user may type is stripped. Pure.
func normalizeTag(raw string) string {
	t := strings.TrimSpace(raw)
	t = strings.TrimPrefix(t, "#")
	return strings.Join(strings.Fields(t), "-")
}
