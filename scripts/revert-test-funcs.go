// Command revert-test-funcs reads Go file paths from stdin, one per line, and
// prints each file's top-level functions as JSON for scripts/revert-test.py:
// {"<path>": [{name, first, last, body, zero, sites}]}. first and last are the
// 1-based lines of the declaration, body is the byte offset just after the body's
// opening brace, and zero is a return of zero values. sites lists the statements
// a variant can suppress or alias, each with the byte edits that make the variant.
// The argument "tests" selects the mode that identities documents.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type function struct {
	Name  string `json:"name"`
	First int    `json:"first"`
	Last  int    `json:"last"`
	Body  int    `json:"body"`
	Zero  string `json:"zero"`
	Sites []site `json:"sites"`
}

// site is one candidate: kind is copy, alias, guard or suppress; start and end
// are the statement's byte offsets.
type site struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Line  int    `json:"line"`
	Text  string `json:"text"`
	Edits []edit `json:"edits"`
}

type edit struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// identity is what identifies a file's functions for a moved test. Build holds the
// file's build constraint lines and the platform its name selects; Functions maps
// each top-level function to its source from `func` to its closing brace, then the
// imports that source can reach.
type identity struct {
	Package   string            `json:"package"`
	Build     string            `json:"build"`
	Functions map[string]string `json:"functions"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "tests" {
		identities()
		return
	}
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
			finder := &sites{fset: fset, source: source, found: []site{}}
			finder.list(fn.Body.List)
			sort.SliceStable(finder.found, func(i, j int) bool {
				return finder.found[i].Start < finder.found[j].Start
			})
			funcs = append(funcs, function{
				Name:  fn.Name.Name,
				First: fset.Position(fn.Pos()).Line,
				Last:  fset.Position(fn.End()).Line,
				Body:  fset.Position(fn.Body.Lbrace).Offset + 1,
				Zero:  zeroReturn(fset, source, fn.Type.Results),
				Sites: finder.found,
			})
		}
		out[path] = funcs
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail(err)
	}
}

// identities reads {"<path>": "<source>"} from stdin and prints {"<path>": identity}.
// A file that does not parse has no entry.
func identities() {
	var sources map[string]string
	if err := json.NewDecoder(os.Stdin).Decode(&sources); err != nil {
		fail(err)
	}
	listed, err := exec.Command("go", "tool", "dist", "list").Output()
	if err != nil {
		fail(err)
	}
	oses, arches := map[string]bool{}, map[string]bool{}
	for _, pair := range strings.Fields(string(listed)) {
		goos, goarch, _ := strings.Cut(pair, "/")
		oses[goos], arches[goarch] = true, true
	}
	out := map[string]identity{}
	for path, source := range sources {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, source,
			parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			continue
		}
		functions := map[string]string{}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Body != nil {
				functions[fn.Name.Name] = text(fset, []byte(source), fn) + "\n" + reached(file, fn)
			}
		}
		build := append(constraints(file), platform(filepath.Base(path), oses, arches))
		out[path] = identity{file.Name.Name, strings.Join(build, "\n"), functions}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail(err)
	}
}

// reached lists the imports of file that fn's source can refer to: those a selector
// in it names, each dot import, and each import whose name its path does not give.
func reached(file *ast.File, fn *ast.FuncDecl) string {
	roots := map[string]bool{}
	ast.Inspect(fn, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if root, ok := selector.X.(*ast.Ident); ok {
				roots[root.Name] = true
			}
		}
		return true
	})
	imports := []string{}
	for _, spec := range file.Imports {
		name := importName(spec)
		if name == "." || name == "" || roots[name] {
			imports = append(imports, name+" "+spec.Path.Value)
		}
	}
	sort.Strings(imports)
	return strings.Join(imports, "\n")
}

// importName returns an import's alias, or the last path element that names it
// after any major version, or "" when that element is no identifier.
func importName(spec *ast.ImportSpec) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	parts := strings.Split(strings.Trim(spec.Path.Value, `"`), "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		last = parts[len(parts)-2]
	}
	if !token.IsIdentifier(last) {
		return ""
	}
	return last
}

// constraints returns the build constraint lines above the package clause.
func constraints(file *ast.File) []string {
	lines := []string{}
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text) {
				lines = append(lines, comment.Text)
			}
		}
	}
	return lines
}

// platform returns the _GOOS, _GOARCH or _GOOS_GOARCH that a file name selects,
// by the go command's rule: text before the first underscore never counts. A name
// the go command ignores returns "ignored".
func platform(name string, oses, arches map[string]bool) string {
	if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
		return "ignored"
	}
	name, _, _ = strings.Cut(name, ".")
	_, rest, found := strings.Cut(name, "_")
	if !found {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix("_"+rest, "_test"), "_")
	last := parts[len(parts)-1]
	if len(parts) >= 2 && oses[parts[len(parts)-2]] && arches[last] {
		return parts[len(parts)-2] + "_" + last
	}
	if oses[last] || arches[last] {
		return last
	}
	return ""
}

// zeroReturn writes `*new(T)` for each unnamed result; named results need a bare return.
func zeroReturn(fset *token.FileSet, source []byte, results *ast.FieldList) string {
	if results == nil || len(results.List) == 0 || len(results.List[0].Names) > 0 {
		return "return"
	}
	values := make([]string, 0, len(results.List))
	for _, field := range results.List {
		values = append(values, "*new("+text(fset, source, field.Type)+")")
	}
	return "return " + strings.Join(values, ", ")
}

type sites struct {
	fset   *token.FileSet
	source []byte
	found  []site
}

func (s *sites) offset(pos token.Pos) int { return s.fset.Position(pos).Offset }

func text(fset *token.FileSet, source []byte, node ast.Node) string {
	return string(source[fset.Position(node.Pos()).Offset:fset.Position(node.End()).Offset])
}

// list classifies each statement of a block and walks the blocks nested in it.
// Headers of if, for and switch statements are never in a list, so they are never
// candidates; function literals are expressions, so their bodies are never walked.
func (s *sites) list(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		s.classify(stmt)
		s.nested(stmt)
	}
}

func (s *sites) nested(stmt ast.Stmt) {
	switch stmt := stmt.(type) {
	case *ast.BlockStmt:
		s.list(stmt.List)
	case *ast.LabeledStmt:
		s.nested(stmt.Stmt)
	case *ast.IfStmt:
		s.list(stmt.Body.List)
		if stmt.Else != nil {
			s.nested(stmt.Else)
		}
	case *ast.ForStmt:
		s.list(stmt.Body.List)
	case *ast.RangeStmt:
		s.list(stmt.Body.List)
	case *ast.SwitchStmt:
		s.nested(stmt.Body)
	case *ast.TypeSwitchStmt:
		s.nested(stmt.Body)
	case *ast.SelectStmt:
		s.nested(stmt.Body)
	case *ast.CaseClause:
		s.list(stmt.Body)
	case *ast.CommClause:
		s.list(stmt.Body)
	}
}

func (s *sites) classify(stmt ast.Stmt) {
	switch stmt := stmt.(type) {
	case *ast.ExprStmt:
		if isCall(stmt.X, "copy") {
			s.suppress("copy", stmt)
		} else {
			s.suppress("suppress", stmt)
		}
	case *ast.AssignStmt:
		switch stmt.Tok {
		case token.ASSIGN:
			if allBlank(stmt.Lhs) && allIdents(stmt.Rhs) {
				return
			}
			kind := "suppress"
			for _, rhs := range stmt.Rhs {
				if isCall(rhs, "copy") || aliasSource(rhs) != nil {
					kind = "copy"
				}
			}
			s.suppress(kind, stmt)
		case token.DEFINE:
			if len(stmt.Lhs) == len(stmt.Rhs) {
				for _, rhs := range stmt.Rhs {
					if src := aliasSource(rhs); src != nil {
						s.alias(stmt, rhs, src)
					}
				}
			}
		}
	case *ast.IncDecStmt, *ast.GoStmt, *ast.DeferStmt:
		s.suppress("suppress", stmt)
	case *ast.IfStmt:
		if stmt.Else == nil && len(stmt.Body.List) > 0 && exits(stmt.Body.List[len(stmt.Body.List)-1]) {
			s.suppress("guard", stmt)
		}
	}
}

// exits reports whether a guard body ends by leaving its block. An if without an
// else is never a terminating statement, so wrapping one changes no build result.
func exits(stmt ast.Stmt) bool {
	switch stmt := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return stmt.Tok == token.BREAK || stmt.Tok == token.CONTINUE || stmt.Tok == token.GOTO
	}
	return false
}

// suppress wraps the statement in `if false { }`, which keeps every identifier it
// references in use.
func (s *sites) suppress(kind string, stmt ast.Stmt) {
	start, end := s.offset(stmt.Pos()), s.offset(stmt.End())
	s.add(kind, stmt, []edit{{start, start, "if false { "}, {end, end, " }"}})
}

// alias binds the copy's source in place of the copy and keeps the copy call in an
// unreachable block ahead of the statement.
func (s *sites) alias(stmt *ast.AssignStmt, rhs, src ast.Expr) {
	start := s.offset(stmt.Pos())
	s.add("alias", stmt, []edit{
		{start, start, "if false { _ = " + text(s.fset, s.source, rhs) + " }; "},
		{s.offset(rhs.Pos()), s.offset(rhs.End()), text(s.fset, s.source, src)},
	})
}

func (s *sites) add(kind string, stmt ast.Stmt, edits []edit) {
	line := strings.TrimSpace(strings.SplitN(text(s.fset, s.source, stmt), "\n", 2)[0])
	s.found = append(s.found, site{
		Kind:  kind,
		Start: s.offset(stmt.Pos()),
		End:   s.offset(stmt.End()),
		Line:  s.fset.Position(stmt.Pos()).Line,
		Text:  line,
		Edits: edits,
	})
}

func isCall(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == name
}

// aliasSource returns src for slices.Clone(src), append([]T(nil), src...) and
// append([]T{}, src...), and nil for any other expression.
func aliasSource(expr ast.Expr) ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "slices" && sel.Sel.Name == "Clone" && len(call.Args) == 1 {
			return call.Args[0]
		}
		return nil
	}
	if !isCall(expr, "append") || len(call.Args) != 2 || !call.Ellipsis.IsValid() {
		return nil
	}
	switch empty := call.Args[0].(type) {
	case *ast.CallExpr:
		nilArg, ok := singleIdent(empty.Args)
		if isSlice(empty.Fun) && ok && nilArg == "nil" && !empty.Ellipsis.IsValid() {
			return call.Args[1]
		}
	case *ast.CompositeLit:
		if isSlice(empty.Type) && len(empty.Elts) == 0 {
			return call.Args[1]
		}
	}
	return nil
}

// allBlank reports an assignment to `_` only.
func allBlank(lhs []ast.Expr) bool {
	for _, expr := range lhs {
		if ident, ok := expr.(*ast.Ident); !ok || ident.Name != "_" {
			return false
		}
	}
	return true
}

// allIdents reports right-hand sides that are bare identifiers, whose evaluation
// has no effect; a call, receive, index or selector can have one.
func allIdents(rhs []ast.Expr) bool {
	for _, expr := range rhs {
		if _, ok := expr.(*ast.Ident); !ok {
			return false
		}
	}
	return true
}

func singleIdent(args []ast.Expr) (string, bool) {
	if len(args) != 1 {
		return "", false
	}
	ident, ok := args[0].(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func isSlice(expr ast.Expr) bool {
	array, ok := expr.(*ast.ArrayType)
	return ok && array.Len == nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "revert-test-funcs:", err)
	os.Exit(2)
}
