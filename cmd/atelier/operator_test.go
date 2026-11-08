package main

import (
	"strings"
	"testing"
)

func TestDestroyGate(t *testing.T) {
	for _, tc := range []struct {
		maitre, yes, tty bool
		input            string
		bad              bool
	}{{true, true, false, "", false}, {false, true, false, "", false}, {false, false, false, "", true}, {false, false, true, "y\n", false}, {false, false, true, "n\n", true}} {
		called := false
		gate := destroyGate(tc.maitre, tc.yes, tc.tty, strings.NewReader(tc.input), func(string) error { called = true; return nil })
		err := gate("Delete?")
		if (err != nil) != tc.bad || called != tc.maitre {
			t.Errorf("%+v: %v called=%v", tc, err, called)
		}
	}
}
