// Command revert-test-funcs reads Go file paths from stdin, one per line, and
// prints each file's top-level functions as JSON for scripts/revert-test.py:
// {"<path>": [{name, first, last, body, zero}]}. first and last are the 1-based
// lines of the declaration, body is the byte offset just after the body's
// opening brace, and zero is a return of zero values.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"strings"
)

type function struct {
	Name  string `json:"name"`
	First int    `json:"first"`
	Last  int    `json:"last"`
	Body  int    `json:"body"`
	Zero  string `json:"zero"`
}

func main() {
	paths, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	out := map[string][]function{}
	for _, path := range strings.Fields(string(paths)) {
		source, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			fail(err)
		}
		funcs := []function{}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			funcs = append(funcs, function{
				Name:  fn.Name.Name,
				First: fset.Position(fn.Pos()).Line,
				Last:  fset.Position(fn.End()).Line,
				Body:  fset.Position(fn.Body.Lbrace).Offset + 1,
				Zero:  zeroReturn(fset, source, fn.Type.Results),
			})
		}
		out[path] = funcs
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail(err)
	}
}

// zeroReturn writes `*new(T)` for each unnamed result; named results need a bare return.
func zeroReturn(fset *token.FileSet, source []byte, results *ast.FieldList) string {
	if results == nil || len(results.List) == 0 || len(results.List[0].Names) > 0 {
		return "return"
	}
	values := make([]string, 0, len(results.List))
	for _, field := range results.List {
		start := fset.Position(field.Type.Pos()).Offset
		end := fset.Position(field.Type.End()).Offset
		values = append(values, "*new("+string(source[start:end])+")")
	}
	return "return " + strings.Join(values, ", ")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "revert-test-funcs:", err)
	os.Exit(2)
}
