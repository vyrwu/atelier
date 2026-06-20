package fzf

import "testing"

func TestParseOutput_EnterOnly(t *testing.T) {
	// fzf called with --expect=ctrl-a --print-query, user typed "foo" and pressed Enter.
	// fzf output order (per `man fzf`) with both flags is: query, expect_key, selection.
	// Enter is the default accept (not in --expect list) so expect_key is empty.
	//   foo
	//   (blank line for empty expect key)
	//   foo
	r := parseOutput("foo\n\nfoo", true, true)
	if r.Key != "" {
		t.Fatalf("Key: got %q want empty", r.Key)
	}
	if r.Query != "foo" || r.Selection != "foo" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseOutput_ExpectKey(t *testing.T) {
	// User pressed Ctrl-A while typed "feat":
	//   feat
	//   ctrl-a
	//   (no selection if no match)
	r := parseOutput("feat\nctrl-a\n", true, true)
	if r.Key != "ctrl-a" {
		t.Fatalf("Key: got %q want ctrl-a", r.Key)
	}
	if r.Query != "feat" {
		t.Fatalf("Query: got %q want feat", r.Query)
	}
}

func TestParseOutput_EnterEmptyQueryWithExpect(t *testing.T) {
	// The exact bug case: --expect=enter --print-query, user hits Enter on empty.
	// fzf output:
	//   (empty query line)
	//   enter
	r := parseOutput("\nenter\n", true, true)
	if r.Key != "enter" {
		t.Fatalf("Key: got %q want enter", r.Key)
	}
	if r.Query != "" {
		t.Fatalf("Query: got %q want empty", r.Query)
	}
}

func TestParseOutput_QueryOnly(t *testing.T) {
	// --print-query only, no --expect. Two lines: query, selection.
	r := parseOutput("typed\nmatch", false, true)
	if r.Key != "" {
		t.Fatalf("Key: got %q want empty", r.Key)
	}
	if r.Query != "typed" || r.Selection != "match" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseOutput_PlainSelection(t *testing.T) {
	// No --expect, no --print-query. One line: selection.
	r := parseOutput("just-this", false, false)
	if r.Key != "" || r.Query != "" {
		t.Fatalf("expected key/query empty, got %+v", r)
	}
	if r.Selection != "just-this" {
		t.Fatalf("Selection: got %q", r.Selection)
	}
}

func TestContainsPrintQuery(t *testing.T) {
	if !containsPrintQuery([]string{"--print-query", "--height=100%"}) {
		t.Fatalf("expected detection of bare --print-query")
	}
	if !containsPrintQuery([]string{"--print-query=foo"}) {
		t.Fatalf("expected detection of --print-query=foo")
	}
	if containsPrintQuery([]string{"--height=100%"}) {
		t.Fatalf("expected no detection without --print-query")
	}
}
