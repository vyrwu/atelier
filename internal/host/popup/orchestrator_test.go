package popup

import "testing"

func TestParseWorkspaceScopedName(t *testing.T) {
	sid, wid, ok := parseWorkspaceScopedName("_atelier_lazygit_12_34")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if sid != "12" || wid != "34" {
		t.Fatalf("got (%q,%q), want (12,34)", sid, wid)
	}
}

func TestParseWorkspaceScopedName_SessionGlobal(t *testing.T) {
	if _, _, ok := parseWorkspaceScopedName("_atelier_k8s"); ok {
		t.Fatalf("session-global names should not parse as workspace-scoped")
	}
}

func TestParseWorkspaceScopedName_NonAtelier(t *testing.T) {
	if _, _, ok := parseWorkspaceScopedName("my-workspace"); ok {
		t.Fatalf("non-atelier names should not parse")
	}
}

func TestIsAtelierPopup(t *testing.T) {
	if !isAtelierPopup("_atelier_lazygit_1_2") {
		t.Fatalf("workspace-scoped should be atelier popup")
	}
	if !isAtelierPopup("_atelier_k8s") {
		t.Fatalf("session-global should be atelier popup")
	}
	if isAtelierPopup("my-workspace") {
		t.Fatalf("non-atelier session should not be atelier popup")
	}
}
