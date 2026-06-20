package popupshell_test

import (
	"testing"

	"github.com/vyrwu/atelier/internal/tools/popupshell"
)

func TestName_StripsTmuxIDSigils(t *testing.T) {
	got := popupshell.Name("$12", "@34")
	want := "_atelier_popupshell_12_34"
	if got != want {
		t.Fatalf("Name: got %q want %q", got, want)
	}
}

func TestName_Stable(t *testing.T) {
	a := popupshell.Name("$0", "@1")
	b := popupshell.Name("$0", "@1")
	if a != b {
		t.Fatalf("Name: not stable, got %q vs %q", a, b)
	}
}

func TestName_DistinctForDifferentParents(t *testing.T) {
	a := popupshell.Name("$0", "@1")
	b := popupshell.Name("$0", "@2")
	if a == b {
		t.Fatalf("Name: different windows should produce different names")
	}
}

func TestSpec_ToolNameMatchesPackage(t *testing.T) {
	if popupshell.Spec.Tool != "popupshell" {
		t.Fatalf("Spec.Tool drifted from package name: %q", popupshell.Spec.Tool)
	}
}
