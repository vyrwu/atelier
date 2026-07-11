// Package manifest defines the in-process descriptor every atelier tool
// carries.
//
// A tool is registered in the static in-process registry (built-in
// tools under internal/tools/*) or synthesized from a user config
// `[tools.<name>]` launcher block. In both cases the Manifest is a Go
// value, not a JSON document marshalled across a subprocess boundary —
// the core knows its tools at compile time (built-ins) or reads them
// from config (launchers). The core uses the manifest to build the
// `atelier tools` dispatcher, generate the tmux.conf binding block via
// `atelier init`, render the tool selector, and check dependencies via
// `atelier doctor`.
package manifest

import "fmt"

// Manifest is a tool's descriptor: how it binds, how its popup looks, and
// what it requires. (Presentation CAPABILITIES — AI summary/attention, forge
// badge — are NOT declared here; they are kernel ports filled by swappable
// integration adapters. See internal/integration.)
type Manifest struct {
	Name        string    `json:"name" toml:"name"`
	Description string    `json:"description,omitempty" toml:"description"`
	Binding     *Binding  `json:"binding,omitempty" toml:"-"`  // primary binding (convenience)
	Bindings    []Binding `json:"bindings,omitempty" toml:"-"` // additional bindings

	// PrimaryInvoke is the subcommand the tool selector launches when this
	// tool is picked from the master picker. Defaults to "open". Useful for
	// tools whose canonical entry isn't named "open" (e.g. pg → "pgcli").
	PrimaryInvoke string `json:"primary_invoke,omitempty"`

	// UI is the tool's visual identity used by the tool selector to render
	// it consistently and by popup-dispatch to set the right title.
	UI *UI `json:"ui,omitempty"`

	Popup    Kind     `json:"popup,omitempty"`
	Requires []string `json:"requires,omitempty"`

	// Tool declares that this tool registers a launchable entry in the M-;
	// tool selector. A registered tool with Tool=false exists but does not
	// appear in the menu. Only tools with Tool=true appear in the selector;
	// discovery, dispatch, and `atelier doctor` are unaffected.
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
// Used to guard synthesized launcher manifests from user config; built-in
// manifests are compile-time literals and are covered by tests.
func (m *Manifest) Validate() error {
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
