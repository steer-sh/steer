package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// The Posture block prints one label per kind of row. Two rows under one label
// name two different things in the same column, and a reader has no way to tell
// which is which — the instrument and the stored corpora both read "Evidence"
// after one rename, and only a reviewer caught it.
func TestEveryPostureRowCarriesItsOwnLabel(t *testing.T) {
	labels := postureLabels(t)
	if len(labels) < 5 {
		t.Fatalf("read %d labels out of renderPosture, expected the whole block", len(labels))
	}
	seen := map[string]bool{}
	for _, label := range labels {
		if seen[label] {
			t.Errorf("two Posture rows print under the label %q", label)
		}
		seen[label] = true
	}
}

// postureLabels reads the label of every row renderPosture prints: the text of
// each format string before its first verb.
func postureLabels(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "status.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "renderPosture" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !isPrintf(call.Fun) {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			format, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if label := strings.TrimSpace(strings.SplitN(format, "%", 2)[0]); label != "" {
				labels = append(labels, label)
			}
			return true
		})
	}
	return labels
}

// isPrintf reports whether a call is fmt.Printf.
func isPrintf(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Printf" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "fmt"
}
