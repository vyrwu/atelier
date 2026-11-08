package tmux

import (
	"reflect"
	"testing"
)

func TestSendLiteralArgsAreLiteral(t *testing.T) {
	for _, text := range []string{"-h", "Enter", "C-c", "hello\nworld"} {
		want := []string{"send-keys", "-t", "s:claude", "-l", "--", text}
		if got := sendLiteralArgs("s:claude", text); !reflect.DeepEqual(got, want) {
			t.Fatal(got)
		}
	}
}
