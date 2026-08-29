package textprompt

import "testing"

func typeString(e *editor, s string) {
	for _, r := range s {
		e.insert(r)
	}
}

func TestEditorInsertAndString(t *testing.T) {
	e := &editor{}
	typeString(e, "hello")
	if e.String() != "hello" || e.cur != 5 {
		t.Fatalf("got %q cur=%d", e.String(), e.cur)
	}
	// Insert in the middle.
	e.home()
	e.right() // after 'h'
	e.insert('X')
	if e.String() != "hXello" || e.cur != 2 {
		t.Fatalf("mid-insert got %q cur=%d", e.String(), e.cur)
	}
}

func TestEditorBackspaceAndDelete(t *testing.T) {
	e := &editor{}
	typeString(e, "abc")
	e.backspace()
	if e.String() != "ab" {
		t.Errorf("backspace got %q", e.String())
	}
	e.home()
	e.deleteFwd()
	if e.String() != "b" || e.cur != 0 {
		t.Errorf("deleteFwd got %q cur=%d", e.String(), e.cur)
	}
	// Backspace at start is a no-op.
	e.backspace()
	if e.String() != "b" {
		t.Errorf("backspace-at-start mutated: %q", e.String())
	}
}

func TestEditorDeleteWord(t *testing.T) {
	e := &editor{}
	typeString(e, "fix the billing bug")
	e.deleteWord() // removes "bug"
	if e.String() != "fix the billing " {
		t.Errorf("deleteWord got %q", e.String())
	}
	e.deleteWord() // removes "billing " (trailing space then word)
	if e.String() != "fix the " {
		t.Errorf("deleteWord#2 got %q", e.String())
	}
}

func TestEditorClearAndCursorBounds(t *testing.T) {
	e := &editor{}
	typeString(e, "stuff")
	e.clear()
	if e.String() != "" || e.cur != 0 {
		t.Errorf("clear got %q cur=%d", e.String(), e.cur)
	}
	// left/right clamp at bounds.
	e.left()
	if e.cur != 0 {
		t.Errorf("left below 0: %d", e.cur)
	}
	typeString(e, "ab")
	e.right()
	if e.cur != 2 {
		t.Errorf("right past end: %d", e.cur)
	}
	e.home()
	if e.cur != 0 {
		t.Errorf("home: %d", e.cur)
	}
	e.end()
	if e.cur != 2 {
		t.Errorf("end: %d", e.cur)
	}
}

func TestEditorTrimmed(t *testing.T) {
	e := &editor{}
	typeString(e, "  spaced out  ")
	if e.trimmed() != "spaced out" {
		t.Errorf("trimmed got %q", e.trimmed())
	}
}

func TestDecodeEsc(t *testing.T) {
	cases := []struct {
		in   []byte
		act  escAction
		done bool
	}{
		{[]byte{0x1b}, actNone, false},
		{[]byte{0x1b, 'x'}, actCancel, true},     // lone Esc → cancel
		{[]byte{0x1b, '['}, actNone, false},      // incomplete CSI
		{[]byte{0x1b, '[', 'D'}, actLeft, true},  // left arrow
		{[]byte{0x1b, '[', 'C'}, actRight, true}, // right arrow
		{[]byte{0x1b, '[', 'H'}, actHome, true},
		{[]byte{0x1b, '[', 'F'}, actEnd, true},
		{[]byte{0x1b, '[', '3'}, actNone, false},       // Delete, incomplete
		{[]byte{0x1b, '[', '3', '~'}, actDelete, true}, // Delete complete
		{[]byte{0x1b, '[', 'A'}, actNoop, true},        // up → noop
		{[]byte{0x1b, 'O', 'D'}, actLeft, true},        // SS3 left
	}
	for _, c := range cases {
		act, done := decodeEsc(c.in)
		if act != c.act || done != c.done {
			t.Errorf("decodeEsc(%v) = (%v,%v), want (%v,%v)", c.in, act, done, c.act, c.done)
		}
	}
}

func TestWrapRunes(t *testing.T) {
	if got := wrapRunes([]rune{}, 5); len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("empty wrap should be one empty line, got %v", got)
	}
	got := wrapRunes([]rune("abcdefg"), 3)
	if len(got) != 3 || string(got[0]) != "abc" || string(got[2]) != "g" {
		t.Errorf("wrap got %v", got)
	}
}

func TestCursorRowCol(t *testing.T) {
	e := &editor{}
	typeString(e, "abcdefg")
	e.home()
	e.right()
	e.right()
	e.right() // cur=3, width 3 → row 1 col 0
	row, col := cursorRowCol(e, 3)
	if row != 1 || col != 0 {
		t.Errorf("cursorRowCol = (%d,%d), want (1,0)", row, col)
	}
}
