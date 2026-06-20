package statestore

import "strings"

// MetadataKeyToOptionName maps a Window.Metadata key (`<plugin>.<field>`)
// to the corresponding tmux window option name (`@<plugin>_<field>`).
//
// Restore uses this to stamp every cached metadata key back onto the
// recreated window as a tmux option, so plugins can read their state
// via `tmux show-options` after a server restart without consulting
// the cache directly. Plugins use the inverse (OptionNameToMetadataKey)
// when they observe a tmux option-set event and need to persist it.
//
// Why two formats: dots in metadata keys are user-friendly +
// hierarchical; tmux option names require alphanumeric/underscore +
// a leading @. The convention is to translate one to the other on
// the boundary.
//
// Examples:
//
//	"ai.prompt"            -> "@ai_prompt"
//	"ai.workspace_kind"    -> "@ai_workspace_kind"
//	"ai.active_session_id" -> "@ai_active_session_id"
//
// Note: this is intentionally one-directional safe — a metadata key
// containing underscores within the field portion (e.g. `ai.workspace_kind`)
// translates cleanly because we only convert the LAST dot. Field
// names with dots themselves would break this; we forbid those by
// convention.
func MetadataKeyToOptionName(key string) string {
	return "@" + strings.ReplaceAll(key, ".", "_")
}

// OptionNameToMetadataKey is the inverse — converts a tmux window
// option name (`@<plugin>_<field>`) to the metadata key
// (`<plugin>.<field>`). The first underscore after `@` is treated
// as the plugin/field separator; remaining underscores stay in the
// field name. Returns empty string if the option name isn't in
// the `@<plugin>_<field>` shape.
//
// Used by plugin code that observes tmux option changes (e.g. via
// a tmux hook on session-window-changed) and wants to mirror them
// into Metadata.
func OptionNameToMetadataKey(option string) string {
	if !strings.HasPrefix(option, "@") {
		return ""
	}
	rest := option[1:]
	idx := strings.Index(rest, "_")
	if idx < 0 {
		return ""
	}
	return rest[:idx] + "." + rest[idx+1:]
}
