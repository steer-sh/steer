package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// branchesOn reports the tier kinds a function branches on: a case value, an == or !=
// operand, or a call to the kind's Is predicate.
func branchesOn(body ast.Node) map[string]bool {
	kinds := map[string]bool{}
	tierSel := func(e ast.Expr) (string, bool) {
		if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
			e = call.Args[0] // string(tier.Review)
		}
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return sel.Sel.Name, ok && pkg.Name == "tier"
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CaseClause:
			for _, e := range n.List {
				if name, ok := tierSel(e); ok {
					kinds[name] = true
				}
			}
		case *ast.BinaryExpr:
			if n.Op != token.EQL && n.Op != token.NEQ {
				break
			}
			for _, e := range []ast.Expr{n.X, n.Y} {
				if name, ok := tierSel(e); ok {
					kinds[name] = true
				}
			}
		case *ast.CallExpr:
			if name, ok := tierSel(n.Fun); ok && strings.HasPrefix(name, "Is") {
				kinds[strings.TrimPrefix(name, "Is")] = true
			}
		}
		return true
	})
	return kinds
}

// A function that branches on the instruction rule or the review prompt also decides
// what a reviewed rule does there: it names Reviewed in its code, or in a comment that
// says why a reviewed rule never reaches it. A new tier that lands beside these two
// meets every such site through this test. It lives in cmd, whose test binary links
// every package that branches on a tier.
func TestEveryTierBranchDecidesTheReviewedRule(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return err
		}
		if filepath.Dir(path) == filepath.Join(root, "internal", "tier") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		check := func(name string, start token.Pos, body *ast.BlockStmt) {
			kinds := branchesOn(body)
			if !kinds["Instruction"] && !kinds["Review"] {
				return
			}
			text := string(src[fset.Position(start).Offset:fset.Position(body.End()).Offset])
			if !strings.Contains(text, "Reviewed") && !strings.Contains(text, "reviewed rule") {
				missing = append(missing, rel+":"+name)
			}
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Body == nil {
					continue
				}
				// The function's text, comments included, from its doc comment on.
				start := decl.Pos()
				if decl.Doc != nil {
					start = decl.Doc.Pos()
				}
				check(decl.Name.Name, start, decl.Body)
			case *ast.GenDecl:
				// A command's RunE is a function literal in a package-level value.
				ast.Inspect(decl, func(n ast.Node) bool {
					if lit, ok := n.(*ast.FuncLit); ok {
						check("func literal at line "+strconv.Itoa(fset.Position(lit.Pos()).Line), lit.Pos(), lit.Body)
						return false
					}
					return true
				})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("these functions branch on the instruction or review tier and do not say what a "+
			"reviewed rule does there:\n  %s", strings.Join(missing, "\n  "))
	}
}
