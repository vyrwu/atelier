package state

import "testing"

func TestClassifySession(t *testing.T) {
	cases := map[string]SessionKind{
		"vyrwu/atelier":       KindWorkspace,
		"wawafertility/infra": KindWorkspace,
		"default":             KindLauncher,
		"_atelier_claude_2_2": KindPopup,
		"_atelier_k8s":        KindPopup, // session-global popup: still a popup
		"_claudepop_3_4":      KindPopup,
		"_popup_1_2":          KindPopup,
	}
	for name, want := range cases {
		if got := ClassifySession(name); got != want {
			t.Errorf("ClassifySession(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestIsPopupSession preserves the rollup-filter contract from the former
// cli/status.go:isPopupSession (the double-attention bug guard): anything on
// an atelier or bash popup session is excluded from the attention rollup.
func TestIsPopupSession(t *testing.T) {
	cases := map[string]bool{
		"_atelier_claude_2_2":          true,
		"_atelier_k8s":                 true,
		"_claudepop_3_4":               true,
		"_popup_1_2":                   true,
		"_k8spop_1_2":                  true,
		"_awspop_1_2":                  true,
		"_lazygitpop_1_2":              true,
		"vyrwu/atelier":                false,
		"wawafertility/infrastructure": false,
		"default":                      false,
		"0":                            false,
		"":                             false,
	}
	for name, want := range cases {
		if got := IsPopupSession(name); got != want {
			t.Errorf("IsPopupSession(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParsePopup_AtelierWorkspaceScoped(t *testing.T) {
	info, ok := ParsePopup("_atelier_claude_5_3")
	if !ok || info.Form != FormAtelier || info.Tool != "claude" || info.SidDigit != "5" || info.WidDigit != "3" {
		t.Fatalf("got %+v ok=%v; want claude/5/3 atelier", info, ok)
	}
}

func TestParsePopup_AtelierSessionGlobalHasNoParent(t *testing.T) {
	if _, ok := ParsePopup("_atelier_k8s"); ok {
		t.Fatal("session-global popups must not yield a parent")
	}
}

func TestParsePopup_Bash(t *testing.T) {
	info, ok := ParsePopup("_claudepop_3_4")
	if !ok || info.Form != FormBash || info.SidDigit != "3" || info.WidDigit != "4" {
		t.Fatalf("got %+v ok=%v; want 3/4 bash", info, ok)
	}
	// Bash prefix with trailing extra token still parses the first two.
	if info, ok := ParsePopup("_popup_1_2_stale"); !ok || info.SidDigit != "1" || info.WidDigit != "2" {
		t.Fatalf("got %+v ok=%v; want 1/2 bash", info, ok)
	}
}

func TestParsePopup_BashRejectsNonDigits(t *testing.T) {
	if _, ok := ParsePopup("_popup_foo_bar"); ok {
		t.Fatal("bash form must reject non-digit sid/wid")
	}
}

func TestParsePopup_NonPopup(t *testing.T) {
	for _, n := range []string{"vyrwu/atelier", "default", ""} {
		if _, ok := ParsePopup(n); ok {
			t.Errorf("ParsePopup(%q) should not parse", n)
		}
	}
}

func TestListable(t *testing.T) {
	if !Listable("/repo", "") || !Listable("", "multi-repo") || !Listable("/repo", "auto") {
		t.Error("a window with @repo_path OR @ai_workspace_kind is listable")
	}
	if Listable("", "") {
		t.Error("a window with neither is not listable")
	}
}

func TestDigits(t *testing.T) {
	cases := map[string]string{
		"$12": "12",
		"@7":  "7",
		"%3":  "3",
		"5":   "5",
		"abc": "",
	}
	for in, want := range cases {
		if got := Digits(in); got != want {
			t.Errorf("Digits(%q) = %q, want %q", in, got, want)
		}
	}
}
