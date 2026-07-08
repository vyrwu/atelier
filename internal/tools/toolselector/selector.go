// Package toolselector is the bash-equivalent of tmux_tool_selector.
//
// Renders a fzf menu of all discovered tools (with per-tool icon + accent
// color from each manifest's UI block) plus a special "Shell" entry that
// navigates back to the user's workspace window (closes popup chain,
// selects window :1). Dispatches the picked tool with the bash-exact
// popup geometry + title and the bash-exact detach-inner-clients +
// one-shot hook pattern (so the user's main client never disconnects).
package toolselector

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	cmddispatch "github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/plugin"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// shellEntryKind is the dispatch kind for the special "Shell" navigation
// entry that's not a tool but appears in the selector menu (matches bash).
const shellEntryKind = "_shell"

type entry struct {
	Icon        string
	Name        string
	Description string
	AccentColor string
	Kind        string // tool name for real tools; shellEntryKind for the Shell special
	Plugin      *plugin.Plugin
}

func SelectCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "select",
		Short: "fzf-pick a tool (with kanji icons + accent colors); dispatch on outer client",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)

			res, err := plugin.Discover()
			if err != nil {
				return err
			}

			entries := buildEntries(res.Plugins)

			// Format: <colored icon>  <colored name>\t<kind>
			// fzf with --ansi strips escape codes from the returned line, so
			// we key the lookup by kind (the trailing tab-separated field
			// which is invariant under --ansi).
			lines := make([]string, 0, len(entries))
			byKind := make(map[string]entry, len(entries))
			for _, e := range entries {
				icon := fzfstyle.ColoredText(e.AccentColor, e.Icon)
				name := fzfstyle.ColoredText(e.AccentColor, e.Name)
				line := fmt.Sprintf("%s  %s\t%s", icon, name, e.Kind)
				lines = append(lines, line)
				byKind[e.Kind] = e
			}

			// alt-n / alt-s / alt-r: swap to sibling workspace pickers
			// without leaving the popup. fzf's `become(...)` exec()s the
			// command in place of fzf, so the popup-session pty survives
			// and the new picker takes over. Same pattern as
			// dispatchExecInPlace below — picker → picker is exec-in-place.
			// Tmux's popup-table M-n binding doesn't reach inside fzf
			// because display-popup -E hands raw stdin to fzf; fzf
			// processes every key against its own --bind table first.
			args := fzfstyle.Args("⌘ ", "Select Tool", "172",
				fzfstyle.WithDelimiter("\t"),
				fzfstyle.WithNth("1"),
				fzfstyle.WithBind("alt-;", "abort"),
				fzfstyle.WithBind("alt-n", "become(atelier tools workspaces pick)"),
				fzfstyle.WithBind("alt-s", "become(atelier tools workspaces sessions)"),
				fzfstyle.WithBind("alt-r", "become(atelier tools workspaces recover)"),
			)
			picked, err := fzf.Pick(lines, args...)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return nil
				}
				return err
			}
			// Extract trailing kind field (tab-delimited).
			kind := picked
			if i := strings.LastIndexByte(picked, '\t'); i >= 0 {
				kind = picked[i+1:]
			}
			chosen, ok := byKind[kind]
			if !ok {
				return fmt.Errorf("could not resolve picked entry: %q", picked)
			}

			return dispatch(h, chosen)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// buildEntries returns the menu order matching bash's tool selector:
// Shell, Popup, K9s, AWS, Lazygit, Claude, Pgcli, Pgcenter, Workspace
// Selector, Workspace Creator. Falls back to plugin order for any
// non-canonical tools.
func buildEntries(plugins []plugin.Plugin) []entry {
	// Only plugins that explicitly register a tool (Manifest.Tool) appear
	// in the selector. Discovered-but-not-a-tool plugins (providers like
	// ghpr that only contribute a workspace badge) are filtered out here,
	// so neither the canonical ordering below nor the community loop can
	// surface them.
	tools := make([]plugin.Plugin, 0, len(plugins))
	for i := range plugins {
		if plugins[i].Name == "toolselector" {
			continue
		}
		if plugins[i].Manifest == nil || !plugins[i].Manifest.Tool {
			continue
		}
		tools = append(tools, plugins[i])
	}
	plugins = tools

	// Index plugins by name for quick lookup.
	byName := make(map[string]*plugin.Plugin, len(plugins))
	for i := range plugins {
		byName[plugins[i].Name] = &plugins[i]
	}

	entries := []entry{}

	// Special "Shell" entry — navigates back to workspace window :1. Not a tool.
	entries = append(entries, entry{
		Icon:        "主",
		Name:        "Shell",
		Description: "Back to workspace shell",
		AccentColor: "73",
		Kind:        shellEntryKind,
	})

	// Canonical order matching bash tmux_tool_selector.
	canonical := []string{
		"popupshell",
		"k8s",
		"aws",
		"lazygit",
		"claude",
		"ghdash",
		"ghenhance",
		"pgcli", // pg subcommands surfaced separately
		"pgcenter",
		"ccusage",
		"workspaces-selector", // virtual: "Select Workspace" from workspaces tool
		"workspaces-creator",  // virtual: "New Workspace" from workspaces tool
		"workspaces-recover",  // virtual: "Recover Workspace" from workspaces tool
	}

	seen := map[string]bool{"toolselector": true}
	for _, key := range canonical {
		switch key {
		case "pgcli":
			if p, ok := byName["pg"]; ok && !seen["pg-pgcli"] {
				entries = append(entries, entry{
					Icon:        "索",
					Name:        "Pgcli",
					Description: "Singleton pgcli popup",
					AccentColor: "67",
					Kind:        "pg:pgcli",
					Plugin:      p,
				})
				seen["pg-pgcli"] = true
				// `pg` is fully represented by its two virtual entries —
				// suppress the plain `pg` entry from the trailing community
				// loop.
				seen["pg"] = true
			}
		case "pgcenter":
			if p, ok := byName["pg"]; ok && !seen["pg-pgcenter"] {
				entries = append(entries, entry{
					Icon:        "監",
					Name:        "Pgcenter",
					Description: "Singleton pgcenter popup",
					AccentColor: "131",
					Kind:        "pg:pgcenter",
					Plugin:      p,
				})
				seen["pg-pgcenter"] = true
				seen["pg"] = true
			}
		case "workspaces-selector":
			if p, ok := byName["workspaces"]; ok && !seen["workspaces-selector"] {
				entries = append(entries, entry{
					Icon:        "栽",
					Name:        "Select Workspace",
					Description: "Pick an existing workspace session",
					AccentColor: "168",
					Kind:        "workspaces:sessions",
					Plugin:      p,
				})
				seen["workspaces-selector"] = true
				seen["workspaces"] = true
			}
		case "workspaces-creator":
			if p, ok := byName["workspaces"]; ok && !seen["workspaces-creator"] {
				entries = append(entries, entry{
					Icon:        "製",
					Name:        "New Workspace",
					Description: "Create a new worktree workspace",
					AccentColor: "108",
					Kind:        "workspaces:pick",
					Plugin:      p,
				})
				seen["workspaces-creator"] = true
				seen["workspaces"] = true
			}
		case "workspaces-recover":
			if p, ok := byName["workspaces"]; ok && !seen["workspaces-recover"] {
				entries = append(entries, entry{
					Icon:        "復",
					Name:        "Recover Workspace",
					Description: "Pick or delete a past/present worktree",
					AccentColor: "141",
					Kind:        "workspaces:recover",
					Plugin:      p,
				})
				seen["workspaces-recover"] = true
				seen["workspaces"] = true
			}
		default:
			if p, ok := byName[key]; ok && !seen[key] {
				e := entry{
					Name:        key,
					Description: p.Manifest.Description,
					Plugin:      p,
					Kind:        key,
				}
				if p.Manifest.UI != nil {
					e.Icon = p.Manifest.UI.Icon
					e.AccentColor = p.Manifest.UI.AccentColor
					if p.Manifest.UI.PopupTitle != "" {
						e.Name = p.Manifest.UI.PopupTitle
					}
				}
				entries = append(entries, e)
				seen[key] = true
			}
		}
	}

	// Append any non-canonical plugins (community tools) in plugin order.
	for i := range plugins {
		name := plugins[i].Name
		if seen[name] {
			continue
		}
		p := &plugins[i]
		e := entry{
			Name:        name,
			Description: p.Manifest.Description,
			Plugin:      p,
			Kind:        name,
		}
		if p.Manifest.UI != nil {
			e.Icon = p.Manifest.UI.Icon
			e.AccentColor = p.Manifest.UI.AccentColor
			if p.Manifest.UI.PopupTitle != "" {
				e.Name = p.Manifest.UI.PopupTitle
			}
		}
		entries = append(entries, e)
	}

	return entries
}

// dispatch handles the picked entry. Special cases:
//
//   - shellEntryKind: navigation, no popup — detach any popup the user was
//     in, then `select-window -t ':1'` on the outer client.
//   - all others: open the chosen tool as a NESTED popup on top of the
//     selector's own popup. The selector process blocks (synchronous
//     display-popup) until the nested popup closes, then exits — at which
//     point the selector popup closes too. Underlying tool popups (e.g.
//     a K9s popup the selector was launched from) survive untouched.
//
// We deliberately do NOT pass -c <outer-client> here. The new popup
// inherits the current popup-client context, which lets it nest cleanly.
// Workspace-affecting actions (switch-client / select-window) inside the
// dispatched tool target the outer client by its captured @atelier_outer_client
// global, so they still land correctly.
func dispatch(h *tmuxhost.Client, e entry) error {
	if e.Kind == shellEntryKind {
		return dispatchShellReturn(h)
	}

	// Resolve plugin + invoke for split kinds like "pg:pgcli".
	pluginName := e.Kind
	invoke := ""
	if idx := strings.IndexByte(e.Kind, ':'); idx > 0 {
		pluginName = e.Kind[:idx]
		invoke = e.Kind[idx+1:]
	}
	if e.Plugin == nil {
		return fmt.Errorf("plugin missing for %q", pluginName)
	}
	if invoke == "" {
		invoke = e.Plugin.Manifest.Primary()
	}

	// Build the popup style — but override the title using the menu entry's
	// display name (so "Pgcli" vs "Pgcenter" titles differ even though
	// they're one plugin).
	mb := *e.Plugin.Manifest.Binding // copy
	mb.Title = entryPopupTitle(e)

	// Geometry-aware dispatch — see dispatchMode for the routing rationale.
	switch dispatchMode(mb.Style) {
	case dispatchExecInPlace:
		debuglog.Logf("toolselector.dispatch: exec-in-place %s/%s (picker→picker)", pluginName, invoke)
		atelierPath, lerr := exec.LookPath("atelier")
		if lerr != nil {
			return fmt.Errorf("atelier not on PATH: %w", lerr)
		}
		argv := []string{"atelier", "tools", pluginName, invoke}
		return execReplace(atelierPath, argv)
	default:
		styleArgs := hostpopup.PopupStyleArgs(&mb)
		invocation := cmddispatch.ToolCmd(pluginName, invoke)
		debuglog.Logf("toolselector.dispatch: OpenOnOuter %s/%s (picker→full)", pluginName, invoke)
		return hostpopup.OpenOnOuter(h, styleArgs, invocation)
	}
}

// DispatchMode chooses how to render the next tool from the selector.
type DispatchMode int

const (
	// dispatchOpenOnOuter detaches inner popup clients then opens a new
	// popup on the outer (workspace) client with full geometry. Required
	// when the target tool's style differs from the selector's — an in-
	// place exec would render the target in the selector's smaller picker
	// frame.
	dispatchOpenOnOuter DispatchMode = iota
	// dispatchExecInPlace replaces the selector's process with the target
	// via syscall.Exec. The selector's popup pty stays, so any underlying
	// tool popup (claude / k9s / lazygit) survives untouched. Only valid
	// when the target's style matches the selector's (picker → picker).
	dispatchExecInPlace
)

// dispatchMode returns the dispatch strategy for a target binding's style.
// Empty / unknown styles fall back to StyleFull semantics (open on outer)
// to preserve the conservative pre-port behavior.
func dispatchMode(targetStyle manifest.Style) DispatchMode {
	if targetStyle == manifest.StylePicker {
		return dispatchExecInPlace
	}
	return dispatchOpenOnOuter
}

// execReplace replaces the current process with the target command via
// syscall.Exec — the popup's pty + every inner-popup state survives, the
// new tool simply takes over the same PID.
func execReplace(path string, argv []string) error {
	return syscall.Exec(path, argv, os.Environ())
}

// dispatchShellReturn returns the user to their workspace window :1,
// closing any popup chain. Matches bash's "Shell" tool action.
func dispatchShellReturn(h *tmuxhost.Client) error {
	out, _ := h.Run("list-clients", "-F", "#{client_session}|#{client_name}")
	var innerClients []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.HasPrefix(parts[0], "_") {
			innerClients = append(innerClients, parts[1])
		}
	}

	if len(innerClients) == 0 {
		_, err := h.Run("select-window", "-t", ":1")
		return err
	}

	// Set hook to select window :1 after the last popup client detaches.
	hookAction := `select-window -t ':1' ; set-hook -ug client-detached`
	if _, err := h.Run("set-hook", "-g", "client-detached", hookAction); err != nil {
		return err
	}
	for _, c := range innerClients {
		_, _ = h.Run("detach-client", "-t", c)
	}
	return nil
}

func entryPopupTitle(e entry) string {
	if e.Name != "" {
		return e.Name
	}
	if e.Plugin != nil && e.Plugin.Manifest.UI != nil {
		return e.Plugin.Manifest.UI.PopupTitle
	}
	return ""
}
