package workspaces

import "testing"

// TestParsePickedIdentity guards the fix for the "can't delete / can't
// even enter a workspace" wedge (fix-deploy-drop-offline-mec1-target,
// feat/remove-livekit-non-eu-envs): picker binds pass the two plain
// identity fields as `{1} {2}`, and the delete/open subcommands read
// them positionally. Passing the whole styled `{}` row (ANSI + recap
// newline + free-form `+`/`(`/`)`/`;`) corrupted fzf's action re-parse
// so the delete never fired. This locks in the positional contract and
// the legacy single-arg tab-split fallback.
func TestParsePickedIdentity(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		first, second string
		ok            bool
	}{
		{
			name:   "two plain fields ({1} {2})",
			args:   []string{"wawafertility/wawa-clinic", "fix-deploy-drop-offline-mec1-target"},
			first:  "wawafertility/wawa-clinic",
			second: "fix-deploy-drop-offline-mec1-target",
			ok:     true,
		},
		{
			name:   "branch with slash",
			args:   []string{"wawafertility/wawa-helm-charts", "feat/remove-livekit-non-eu-envs"},
			first:  "wawafertility/wawa-helm-charts",
			second: "feat/remove-livekit-non-eu-envs",
			ok:     true,
		},
		{
			name:   "extra display arg is ignored",
			args:   []string{"owner/repo", "branch", "styled display junk"},
			first:  "owner/repo",
			second: "branch",
			ok:     true,
		},
		{
			name:   "legacy single tab-joined arg still splits",
			args:   []string{"owner/repo\tbranch\t\x1b[36mdisplay\x1b[0m"},
			first:  "owner/repo",
			second: "branch",
			ok:     true,
		},
		{name: "empty first field is not ok", args: []string{"", "branch"}, ok: false},
		{name: "empty second field is not ok", args: []string{"owner/repo", ""}, ok: false},
		{name: "no args", args: nil, ok: false},
		{name: "single arg without tab is not ok", args: []string{"owner/repo"}, ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			first, second, ok := parsePickedIdentity(c.args)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (args=%q)", ok, c.ok, c.args)
			}
			if !ok {
				return
			}
			if first != c.first || second != c.second {
				t.Errorf("got (%q, %q), want (%q, %q)", first, second, c.first, c.second)
			}
		})
	}
}
