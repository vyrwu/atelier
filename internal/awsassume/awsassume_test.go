package awsassume

import "testing"

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"prod":                      `'prod'`,
		"core-auto/DeveloperAccess": `'core-auto/DeveloperAccess'`,
		"a'b":                       `'a'\''b'`,
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickerCmd(t *testing.T) {
	got := PickerCmd("core-auto/DeveloperAccess", "/bin/zsh")
	want := `/bin/zsh -i -c 'assume '\''core-auto/DeveloperAccess'\''; exec /bin/zsh'`
	if got != want {
		t.Errorf("PickerCmd:\n got %q\nwant %q", got, want)
	}
}

func TestPickerCmd_EscapesQuoteInProfile(t *testing.T) {
	got := PickerCmd("a'b", "/bin/zsh")
	// The profile's single quote must survive both levels of quoting.
	want := `/bin/zsh -i -c 'assume '\''a'\''\'\'''\''b'\''; exec /bin/zsh'`
	if got != want {
		t.Errorf("PickerCmd:\n got %q\nwant %q", got, want)
	}
}

func TestWrapAuth_Empty(t *testing.T) {
	launch := "atelier tools k8s _launch"
	if got := WrapAuth("", launch, "/bin/zsh"); got != launch {
		t.Errorf("WrapAuth empty authCmd = %q, want unwrapped %q", got, launch)
	}
	if got := WrapAuth("   ", launch, "/bin/zsh"); got != launch {
		t.Errorf("WrapAuth blank authCmd = %q, want unwrapped %q", got, launch)
	}
}

func TestWrapAuth_ExecPrefix(t *testing.T) {
	got := WrapAuth("assume prod --exec", "atelier tools k8s _launch", "/bin/zsh")
	want := `/bin/zsh -i -c 'assume prod --exec '\''atelier tools k8s _launch'\'''`
	if got != want {
		t.Errorf("WrapAuth:\n got %q\nwant %q", got, want)
	}
}
