package workspaces

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestAltN_AbortBoundInEveryCreatorPicker locks in the toggle-dismiss
// fix: every fzf picker that's part of the creator chain must bind
// `alt-n` to `abort`, so the M-n invocation key always dismisses the
// popup it opened — at any depth (repo picker → name picker → prompt
// picker → auto-session).
//
// This is a source-inspection regression test rather than a runtime
// behavior test because the fzf args are built inline at each picker
// call-site (refactoring all four into a shared helper risks breaking
// the prompt/transform logic that's specific to each picker). If a
// future refactor introduces a real helper, replace this with a direct
// assertion on that helper's output.
func TestAltN_AbortBoundInEveryCreatorPicker(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "workspaces.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse workspaces.go: %v", err)
	}

	// Functions that host creator-chain pickers. Each function body MUST
	// contain a WithBind("alt-n", "abort") call.
	want := map[string]bool{
		"PickCommand":        false, // top-level creator (repo picker)
		"runWorkspaceName":   false, // manual branch-name picker
		"runWorkspacePrompt": false, // auto-mode prompt picker
		"runAutoSession":     false, // multi-repo auto-session prompt
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		// Walk the function body; flag if we find a WithBind call whose
		// first arg is "alt-n" and second arg is "abort".
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WithBind" {
				return true
			}
			if len(call.Args) < 2 {
				return true
			}
			if litEquals(call.Args[0], "alt-n") && litEquals(call.Args[1], "abort") {
				want[fn.Name.Name] = true
			}
			return true
		})
	}

	for fn, found := range want {
		if !found {
			t.Errorf("function %s is missing WithBind(\"alt-n\", \"abort\") — M-n toggle dismiss won't work at this depth of the creator chain", fn)
		}
	}
}

func litEquals(e ast.Expr, want string) bool {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return false
	}
	return strings.Trim(bl.Value, `"`) == want
}
