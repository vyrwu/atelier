package statestore

import "testing"

// TestMetadataKeyToOptionName locks the metadata-key →
// tmux-option-name convention. Plugins rely on this round-trip to
// move state between Metadata (storage) and tmux options (runtime).
// Breaking it would mean restored windows don't have their tmux
// options re-stamped after a server restart — plugins reading via
// `tmux show-options` would see empty values, breaking flows like
// claude's resume-from-last-session-id.
func TestMetadataKeyToOptionName(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"ai.prompt", "@ai_prompt"},
		{"ai.workspace_kind", "@ai_workspace_kind"},
		{"ai.active_session_id", "@ai_active_session_id"},
		{"k8s.context", "@k8s_context"},
	}
	for _, c := range cases {
		if got := MetadataKeyToOptionName(c.key); got != c.want {
			t.Errorf("MetadataKeyToOptionName(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

// TestOptionNameToMetadataKey locks the inverse direction. Plugin
// code that hooks into tmux option-set events uses this to mirror
// the option value into Metadata for persistence.
func TestOptionNameToMetadataKey(t *testing.T) {
	cases := []struct {
		option string
		want   string
	}{
		{"@ai_prompt", "ai.prompt"},
		{"@ai_workspace_kind", "ai.workspace_kind"},
		{"@ai_active_session_id", "ai.active_session_id"},
		{"@k8s_context", "k8s.context"},
		// Pathological case that callers must handle:
		{"no_at_prefix", ""},
	}
	for _, c := range cases {
		if got := OptionNameToMetadataKey(c.option); got != c.want {
			t.Errorf("OptionNameToMetadataKey(%q) = %q, want %q", c.option, got, c.want)
		}
	}
}

// TestOptionNameToMetadataKey_NoUnderscore: an option like `@foo`
// (no underscore at all) doesn't fit the `@<plugin>_<field>`
// convention — return empty rather than guessing a field split.
func TestOptionNameToMetadataKey_NoUnderscore(t *testing.T) {
	if got := OptionNameToMetadataKey("@foo"); got != "" {
		t.Errorf("malformed option name should yield empty key, got %q", got)
	}
}
