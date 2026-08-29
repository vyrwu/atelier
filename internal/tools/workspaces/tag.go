package workspaces

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// Workspace tagging (M-t in the session picker): a user-assigned label
// that groups workspaces across repos/branches. The tag is stored as a
// per-window tmux option (workspace.OptWorkspaceTag, the source of
// truth) and rendered as a stable-colored pill after the window name in
// the picker. Color is derived from the tag name — same name always maps
// to the same palette entry, independent of creation order — so the eye
// can cluster related workspaces without reading every name.

// tagPalette is a hand-curated slice of 256-color codes: distinct hues
// stepped ~25° apart around the whole color wheel so every pair is
// unmistakable at a glance, but kept in the vivid-but-soft band (lowest
// channel lifted off 0, e.g. #ff5f5f not #ff0000) so they read as clear,
// separate colors without the eye-searing harshness of pure neon primaries.
// None blends into the dim grey chrome or the muted brick-red clear row.
// The color assigned to a tag is tagPalette[fnv32a(name) % len(tagPalette)]
// — a pure function of the name, stable across restarts and machines.
var tagPalette = []string{
	"203", // red
	"209", // orange
	"215", // amber
	"221", // yellow
	"155", // lime
	"84",  // green
	"79",  // teal
	"45",  // cyan
	"75",  // sky blue
	"69",  // blue
	"99",  // violet
	"171", // purple
	"207", // magenta
	"205", // pink
}

// tagColor returns the 256-color code assigned to a tag name. Pure and
// order-independent: fnv32a(name) mod palette size.
func tagColor(tag string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(tag))
	return tagPalette[h.Sum32()%uint32(len(tagPalette))]
}

// renderTagPill returns the spliceable, ANSI-styled pill token (leading
// space) for a tag, e.g. " #client-x", rendered italic in the tag's muted
// color. Italic (SGR 3) matches the recap line and keeps the pill reading
// as a soft label. The `#` and name are plain visible text so fzf's
// name-field search matches "#client-x" or bare "client-x". Empty tag →
// "" (no pill). Pure.
func renderTagPill(tag string) string {
	if tag == "" {
		return ""
	}
	return " \033[3;38;5;" + tagColor(tag) + "m#" + tag + "\033[0m"
}

// parseTagList extracts the sorted, de-duplicated set of non-empty tags
// from `list-windows -a -F #{@workspace_tag}` output. Pure.
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

// collectTags returns every distinct tag currently assigned across all
// windows — the tag registry the M-t picker offers. The live tmux state
// is the registry: no separate file to keep in sync.
func collectTags(h *tmuxhost.Client) []string {
	out, err := h.Run("list-windows", "-a", "-F", "#{"+workspace.OptWorkspaceTag+"}")
	if err != nil {
		return nil
	}
	return parseTagList(out)
}

// clearTagLabel is the visible text of the synthetic first row offered
// when the workspace already has a tag. Selecting it (the highlighted
// default when the query is empty) clears the tag — spec: "clearing with
// an empty selection removes the tag". A real tag can never equal this
// (tags are normalized to a single hyphen-joined token with no spaces).
// fzf runs --ansi, so the returned selection is this text with color
// stripped — the resolveTagChoice comparison is against the bare label.
const clearTagLabel = "✗ clear tag"

// clearTagColor is the 256-color code for the clear row — a muted brick
// red that reads as the reset action without the harshness of pure red,
// staying in the same medium band as the tag palette.
const clearTagColor = "131"

// currentMarker annotates the workspace's active tag in the picker list
// (instead of naming it in the header), rendered in the dim structural
// grey. It is stripped off the selection in resolveTagChoice.
const currentMarker = " (current)"

// tagPickerItems is the list the M-t picker offers: each existing tag
// rendered as its colored "#tag" pill (the active one annotated
// "(current)"), with a red "clear tag" row prepended when a tag is
// currently set (so empty-query Enter clears). fzf --ansi strips the
// color on return, so the selection comes back as the bare
// "#tag [(current)]" / clear-label text. Pure.
func tagPickerItems(current string, tags []string) []string {
	items := make([]string, 0, len(tags)+1)
	if current != "" {
		items = append(items, "\033[38;5;"+clearTagColor+"m"+clearTagLabel+"\033[0m")
	}
	for _, t := range tags {
		// Same pill styling as the M-s list, minus the leading space.
		item := strings.TrimSpace(renderTagPill(t))
		if t == current {
			item += "\033[38;5;103m" + currentMarker + "\033[0m"
		}
		items = append(items, item)
	}
	return items
}

// resolveTagChoice decides which tag to apply from the tag picker's
// result: the clear row clears; a matched existing tag (selection) wins;
// otherwise the typed query creates/reuses a tag; an empty query clears
// the tag. The selection comes back as "#tag" (list rows show the pill,
// the active one suffixed "(current)"), so the marker is dropped and the
// rest normalized like a typed query — leading '#' stripped, interior
// whitespace collapsed to hyphens. Pure.
func resolveTagChoice(selection, query string) string {
	s := strings.TrimSpace(selection)
	if s == clearTagLabel {
		return ""
	}
	s = strings.TrimSuffix(s, currentMarker)
	if s != "" {
		return normalizeTag(s)
	}
	return normalizeTag(query)
}

// normalizeTag trims a raw typed tag and collapses interior whitespace to
// single hyphens (tags are single tokens so the picker search and the
// pill stay clean). A leading `#` the user may type is stripped. Pure.
func normalizeTag(raw string) string {
	t := strings.TrimSpace(raw)
	t = strings.TrimPrefix(t, "#")
	return strings.Join(strings.Fields(t), "-")
}

// sgrRe strips SGR color sequences from an fzf item so the preview can read
// the plain tag text off the hovered row ({} passes the raw, still-colored
// line; fzf only strips color from the RETURNED selection, not from preview
// placeholders).
var sgrRe = regexp.MustCompile("\033\\[[0-9;]*m")

// formatTagPreview renders the M-t preview line: the target workspace's title
// row as it WILL look once the pending tag choice is applied, so the user sees
// the result before committing. Cases, by what fzf currently has focused:
//
//   - an existing tag row focused, or a new tag typed → that tag's live pill;
//   - the clear-tag row focused, or nothing resolvable → just the title, no pill.
//
// hovered is the raw (colored) focused item; query is the live typed text. Pure.
func formatTagPreview(title, query, hovered string) string {
	titleCol := "\033[38;5;255m" + title + "\033[0m"
	tag := resolveTagChoice(sgrRe.ReplaceAllString(hovered, ""), query)
	pill := ""
	if tag != "" {
		pill = strings.TrimSpace(renderTagPill(tag)) + " "
	}
	return "\033[2mPreview:\033[0m " + pill + titleCol
}

// TagPreviewCommand is the hidden `_tag-preview`: the M-t picker's live
// header-preview command. The --title flag carries the workspace title; the two
// trailing positionals are fzf's live {q} (query) and {} (hovered row). Pure
// render — runs on every keystroke.
func TagPreviewCommand() *cobra.Command {
	var title string
	c := &cobra.Command{
		Use:    "_tag-preview [query] [hovered]",
		Short:  "internal: render the M-t tag preview line",
		Hidden: true,
		Args:   cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			query, hovered := "", ""
			if len(args) > 0 {
				query = args[0]
			}
			if len(args) > 1 {
				hovered = args[1]
			}
			fmt.Fprintln(cmd.OutOrStdout(), formatTagPreview(title, query, hovered))
			return nil
		},
	}
	c.Flags().StringVar(&title, "title", "", "workspace title")
	return c
}

// TagCommand is the hidden `_tag <session>`: bound to M-t in the workspace
// picker. It opens a nested fzf listing existing tags for the selected
// workspace, lets the user pick one or type a new one (empty clears), and
// writes workspace.OptWorkspaceTag on that session. It runs inline via the
// picker's execute() bind, and the picker reloads afterward so the pill appears.
func TagCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_tag <session>",
		Short:  "internal: tag the selected workspace (M-t in the picker)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			h := tmuxhost.New(socket)
			session := strings.TrimSpace(args[0])
			if session == "" {
				return nil
			}
			curOut, _ := h.Run("show-option", "-t", session, "-qv", workspace.OptWorkspaceTag)
			current := strings.TrimSpace(string(curOut))
			title := session
			if t, _ := h.Run("show-option", "-t", session, "-qv", workspace.OptWorkspaceTitle); strings.TrimSpace(string(t)) != "" {
				title = strings.TrimSpace(string(t))
			}

			// Live preview as the header: the target row as it will look once
			// the pending choice is applied. transform-header re-runs on start,
			// keystroke (change), and selection move (focus).
			previewCmd := dispatch.ToolCmd("workspaces", "_tag-preview",
				"--title="+title, "--", "{q}", "{}")
			preview := "transform-header(" + previewCmd + ")"
			opts := []fzfstyle.Opt{
				fzfstyle.WithCustomColor("prompt:111:bold,pointer:111,query:111,hl:111,hl+:111:bold,label:103,border:103,header:111,footer:103"),
				fzfstyle.WithPrintQuery(),
				fzfstyle.WithExpect("enter"),
				fzfstyle.WithBind("start", preview),
				fzfstyle.WithBind("change", preview),
				fzfstyle.WithBind("focus", preview),
				fzfstyle.WithFooter("M-t · cancel"),
				fzfstyle.WithBind("alt-t", "abort"),
			}
			pickerArgs := fzfstyle.Args("宛 ", "Tag Workspace", "111", opts...)
			res, err := fzf.PickWithExpect(tagPickerItems(current, collectTags(h)), []string{"enter"}, pickerArgs...)
			if err != nil {
				return nil // Esc / Ctrl-C: leave the tag as-is.
			}
			chosen := resolveTagChoice(res.Selection, res.Query)
			if chosen == current {
				return nil
			}
			if err := workspace.SetTag(h, session, chosen); err != nil {
				debuglog.LogErr("workspaces._tag", err)
				return err
			}
			debuglog.Logf("workspaces._tag: %s tag=%q (was %q)", session, chosen, current)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// tagBind is the workspace-picker M-t action: open the nested tag picker for the
// current row (session = {1}), then reload the list so the new pill renders.
func tagBind() string {
	return "execute(" + dispatch.ToolCmd("workspaces", "_tag", "{1}") + ")+reload(" +
		dispatch.ToolCmd("workspaces", "_session-list") + ")"
}
