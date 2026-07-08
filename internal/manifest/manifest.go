// Package manifest defines the JSON contract every atelier tool implements.
//
// Tools are external binaries named `atelier-<name>`. When invoked with the
// sentinel flag `--atelier-manifest`, a tool must print its manifest JSON to
// stdout and exit 0. The core uses this to discover tools, generate the
// tmux.conf binding block, and check dependencies via `atelier doctor`.
//
// The contract is versioned via APIVersion. Tools declare which version they
// were built against; the core refuses to load tools with a mismatched major.
package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// APIVersion is the current manifest contract version. Bump only on
// breaking changes; the core refuses to load tools with a mismatched value.
const APIVersion = 1

// Sentinel is the flag a tool must recognize and respond to by emitting its
// manifest as JSON. Chosen to be obscure enough to never collide with a
// tool's own subcommands.
const Sentinel = "--atelier-manifest"

// DiscoveryTimeout is the max time to wait for a tool to emit its manifest.
// Slow tools are skipped (not errors) — this protects the core from hangs.
// 5s tolerates shell-script tools under CI load while still bounding the
// blast radius of a hung binary.
const DiscoveryTimeout = 5 * time.Second

// Manifest is the JSON contract emitted by every atelier-* binary in
// response to the Sentinel flag.
type Manifest struct {
	APIVersion  int       `json:"api_version"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Version     string    `json:"version,omitempty"`
	Binding     *Binding  `json:"binding,omitempty"`  // primary binding (convenience)
	Bindings    []Binding `json:"bindings,omitempty"` // additional bindings

	// PrimaryInvoke is the subcommand the tool selector launches when this
	// tool is picked from the master picker. Defaults to "open". Useful for
	// tools whose canonical entry isn't named "open" (e.g. pg → "pgcli").
	PrimaryInvoke string `json:"primary_invoke,omitempty"`

	// UI is the tool's visual identity used by the tool selector to render
	// it consistently and by popup-dispatch to set the right title.
	UI *UI `json:"ui,omitempty"`

	Popup       Kind     `json:"popup,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	Subcommands []string `json:"subcommands,omitempty"`

	// Tool declares that this plugin registers a launchable entry in the
	// M-; tool selector. Being a discovered plugin is NOT enough — a
	// plugin may exist purely to contribute background capabilities
	// (badges, sort signals, hooks) without being a menu tool. Only
	// plugins with Tool=true appear in the selector; providers like ghpr
	// omit it. Discovery, dispatch, and `atelier doctor` are unaffected.
	Tool bool `json:"tool,omitempty"`

	// PickerBindings declares the keys this tool binds INSIDE its own
	// popup (whatever rendering technology the popup uses — fzf, bubbletea,
	// raw curses, doesn't matter to atelier). atelier core uses this only
	// to render the `atelier help` cheatsheet; the actual bindings are the
	// tool's own responsibility.
	//
	// The convention is to use atelier's leader (Meta / M-*) so users see
	// one consistent modifier across the whole UI, but tools can deviate
	// if they have a stronger UX reason.
	PickerBindings []PickerBinding `json:"picker_bindings,omitempty"`

	// Badge, if set, declares that this tool contributes a per-workspace
	// status badge to the M-s workspace picker. The picker stays agnostic
	// to what the badge means: it reads the declared window option, splices
	// its (already-rendered) value between the workspace name and the AI
	// summary, and — if Action is set — wires the declared key to invoke a
	// subcommand on the selected row. See internal/tools/workspaces badge
	// wiring. The gh-pr tool is the first provider.
	Badge *Badge `json:"badge,omitempty"`
}

// Badge is a tool's contribution to the workspace picker: a per-window
// status symbol plus an optional keybinding that acts on the selected row.
// The core never interprets the badge's meaning — the tool writes a
// pre-rendered (ANSI-colored) glyph into the Option window-option and the
// picker splices it verbatim.
type Badge struct {
	// Option is the tmux window-option the picker reads for the badge
	// value, e.g. "@ghpr_badge". Follows the @<plugin>_<field> convention.
	Option string `json:"option"`

	// Refresh, if set, is a subcommand the picker spawns (detached, once
	// per open) so the tool can update its badges. The tool owns its own
	// staleness throttling — the picker just pokes it. e.g. "_refresh".
	Refresh string `json:"refresh,omitempty"`

	// Order sorts this badge among multiple providers (ascending). Ties
	// break on tool name.
	Order int `json:"order,omitempty"`

	// SortOption + SortOrder let a provider influence the picker's row
	// order. When set, the picker reads SortOption (a tmux window option
	// holding a semantic value) and sorts rows by the value's index in
	// SortOrder (earlier = higher). Values absent from SortOrder (and
	// windows without the option) sort last. Applied WITHIN the picker's
	// existing grouping, as a tiebreak before recency. ghpr uses this to
	// order open → draft → merged → closed.
	SortOption string   `json:"sort_option,omitempty"`
	SortOrder  []string `json:"sort_order,omitempty"`

	// Action, if set, binds a key in the picker that runs Invoke on the
	// selected row.
	Action *BadgeAction `json:"action,omitempty"`
}

// BadgeAction binds a picker key to a tool subcommand invoked with the
// selected row (the full tab-delimited picker line) as its argument.
type BadgeAction struct {
	// Key is atelier canonical form ("M-o", "C-o"); the picker translates
	// it to its rendering library's syntax and shows it in the footer.
	Key string `json:"key"`
	// Invoke is the tool subcommand to run, e.g. "open".
	Invoke string `json:"invoke"`
	// Label is the short footer text, e.g. "open PR".
	Label string `json:"label,omitempty"`
}

// PickerBinding describes one keybinding inside a tool's popup. It is
// purely informational — atelier core does NOT enforce or wire these.
// The tool itself decides how to honor them (typically by translating
// `Key` into its rendering library's binding syntax — fzf's `--bind`,
// bubbletea's key matcher, etc.).
type PickerBinding struct {
	// Picker is the logical name of the popup within the tool. Tools
	// with multiple distinct pickers (e.g. workspaces has "creator",
	// "name", "sessions", "clone") use this to group bindings. Optional;
	// omit if the tool has only one picker.
	Picker string `json:"picker,omitempty"`

	// Key is the binding as the tool implements it. Use atelier's
	// canonical form: "M-x" for Meta+x, "C-x" for Ctrl+x, "enter",
	// "esc", "tab". The help renderer treats this as opaque display text
	// — no validation, no remapping.
	Key string `json:"key"`

	// Action is a short, action-oriented label rendered to the user.
	// e.g. "Toggle repo/auto mode", "Delete workspace", "Switch to clone picker".
	Action string `json:"action"`
}

// UI declares a tool's visual identity. Optional; sensible defaults apply
// if missing. The atelier toolselector reads these to render each entry
// with the tool's icon + accent color. Popup dispatch reads PopupTitle
// for the `-T` flag.
type UI struct {
	// Icon is the symbol shown next to the tool name in the selector
	// (typically a single CJK character like "知", "栽", "製").
	Icon string `json:"icon,omitempty"`
	// AccentColor is a 256-color number ("173") or a named color ("red")
	// used for the icon, name, prompt, pointer, and hl in fzf invocations
	// originating from this tool.
	AccentColor string `json:"accent_color,omitempty"`
	// PopupTitle is the text shown in the popup's centered title (-T flag).
	PopupTitle string `json:"popup_title,omitempty"`
}

// Primary returns the tool's primary subcommand for the master selector.
// Defaults to "open" if both Binding.Invoke and PrimaryInvoke are unset.
func (m *Manifest) Primary() string {
	if m.PrimaryInvoke != "" {
		return m.PrimaryInvoke
	}
	if m.Binding != nil && m.Binding.Invoke != "" {
		return m.Binding.Invoke
	}
	return "open"
}

// AllBindings returns the merged set: the primary Binding (if any) plus any
// in Bindings, in order. Used by initgen.
func (m *Manifest) AllBindings() []Binding {
	out := make([]Binding, 0, 1+len(m.Bindings))
	if m.Binding != nil {
		out = append(out, *m.Binding)
	}
	out = append(out, m.Bindings...)
	return out
}

// Binding describes a tmux key binding the tool wants in the generated
// `atelier init` output. Bindings are optional — pure CLI tools may omit them.
type Binding struct {
	Key      string `json:"key"`                 // e.g. "p", "g", "M-n"
	KeyTable string `json:"key_table,omitempty"` // "root" (default), "popup"
	Title    string `json:"title,omitempty"`     // popup title text
	Style    Style  `json:"style,omitempty"`     // "full" or "picker"
	StartCwd bool   `json:"start_cwd,omitempty"` // pass -d "#{pane_current_path}"
	Invoke   string `json:"invoke,omitempty"`    // subcommand to call; defaults to "open"

	// AlsoInPopup also emits a `bind -T popup <key>` sibling that runs
	// `detach-client` before opening the popup. Used by selectors and
	// switchers — pressing the key from inside another popup closes that
	// popup and opens the selector on the outer client.
	AlsoInPopup bool `json:"also_in_popup,omitempty"`
}

// Kind classifies the backing-session lifecycle for a tool's popup.
type Kind string

const (
	KindWorkspace Kind = "workspace" // per-tmux-window backing session
	KindGlobal    Kind = "global"    // singleton backing session
	KindNone      Kind = "none"      // no persistent popup (e.g. pickers)
)

// Style classifies the visual popup style atelier should emit in bindings.
type Style string

const (
	StyleFull   Style = "full"   // rounded border, bottom-anchored, full-height
	StylePicker Style = "picker" // -B compact picker (70%×70%)
)

// Validate sanity-checks a manifest. Returns the first error found.
func (m *Manifest) Validate() error {
	if m.APIVersion == 0 {
		return fmt.Errorf("manifest missing api_version")
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("manifest api_version %d is incompatible with core api_version %d",
			m.APIVersion, APIVersion)
	}
	if m.Name == "" {
		return fmt.Errorf("manifest missing name")
	}
	// Binding without Key is valid — declares popup style for the
	// selector without requesting a tmux keybinding. initgen skips
	// emission for such bindings.
	switch m.Popup {
	case "", KindNone, KindWorkspace, KindGlobal:
	default:
		return fmt.Errorf("manifest popup %q is not a known kind", m.Popup)
	}
	switch m.Binding.style() {
	case "", StyleFull, StylePicker:
	default:
		return fmt.Errorf("manifest binding style %q is not known", m.Binding.style())
	}
	return nil
}

func (b *Binding) style() Style {
	if b == nil {
		return ""
	}
	return b.Style
}

// FromBinary invokes the binary at path with the Sentinel flag and parses
// the manifest JSON it prints to stdout. Returns an error if the binary
// doesn't respond within DiscoveryTimeout, returns non-zero, or emits
// invalid JSON.
func FromBinary(path string) (*Manifest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DiscoveryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, Sentinel)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s %s: timed out after %s", path, Sentinel, DiscoveryTimeout)
		}
		return nil, fmt.Errorf("%s %s: %w (%s)", path, Sentinel, err, strings.TrimSpace(errBuf.String()))
	}

	m, err := FromJSON(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// FromJSON parses raw manifest JSON bytes.
func FromJSON(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(bytes.TrimSpace(data), &m); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// AsJSON renders the manifest as pretty-printed JSON. Convenience for
// tools that want to dump their own manifest.
func (m *Manifest) AsJSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
