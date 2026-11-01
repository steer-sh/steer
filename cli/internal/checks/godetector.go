package checks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DeclDigest is one tracked test-side declaration and the SHA-256 of its source.
type DeclDigest struct {
	File   string `json:"file"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// goDecl is one top-level declaration of a test file.
type goDecl struct {
	file  string
	name  string
	node  ast.Decl
	src   []byte
	names []string
	// embeds holds the patterns of the //go:embed directives bound to a var.
	embeds []string
}

// goDetector is the part of a Go package's test files that one test depends
// on, as far as static references reach.
type goDetector struct {
	pkgName    string
	decls      []goDecl
	imports    map[string]string
	blank      []string
	unresolved []string
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// analyzeGoTest collects the test-side declarations the named test reaches by
// static reference, plus TestMain and init. Production code is outside the
// boundary. References it sees but cannot track are returned as unresolved.
func analyzeGoTest(root string, d Declaration) (goDetector, error) {
	dir := filepath.Join(root, filepath.FromSlash(d.Package))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return goDetector{}, fmt.Errorf("check %s: package %s: %w", d.ID, d.Package, err)
	}
	fset := token.NewFileSet()
	type parsed struct {
		name string
		file *ast.File
		src  []byte
	}
	var files []parsed
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return goDetector{}, err
		}
		f, err := parser.ParseFile(fset, e.Name(), src, parser.ParseComments)
		if err != nil {
			return goDetector{}, fmt.Errorf("check %s: %w", d.ID, err)
		}
		files = append(files, parsed{e.Name(), f, src})
	}

	var pkgName string
	for _, f := range files {
		for _, decl := range f.file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == d.Test {
				pkgName = f.file.Name.Name
			}
		}
	}
	if pkgName == "" {
		return goDetector{}, fmt.Errorf("check %s: no test function %s in %s", d.ID, d.Test, d.Package)
	}

	byName := map[string][]int{}
	methods := map[string][]int{}
	methodsNamed := map[string][]int{}
	siblingNames := map[string]bool{}
	var decls []goDecl
	fileImports := map[string]map[string]string{}
	fileMisnamed := map[string][]string{}
	for _, f := range files {
		if f.file.Name.Name != pkgName {
			for _, decl := range f.file.Decls {
				for _, n := range declNames(decl) {
					siblingNames[n] = true
				}
			}
			continue
		}
		fileImports[f.name] = importNames(f.file)
		fileMisnamed[f.name] = misnamedImports(f.file)
		prevEnd := f.file.Name.End()
		for _, decl := range f.file.Decls {
			leadFrom := prevEnd
			prevEnd = decl.End()
			if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
				continue
			}
			start, end := fset.Position(decl.Pos()).Offset, fset.Position(decl.End()).Offset
			gd := goDecl{file: f.name, node: decl, src: f.src[start:end], names: declNames(decl)}
			if v, ok := decl.(*ast.GenDecl); ok && v.Tok == token.VAR {
				var lead []byte
				lead, gd.embeds = embedDirectives(f.file.Comments, leadFrom, decl)
				gd.src = append(lead, gd.src...)
			}
			gd.name = strings.Join(gd.names, ",")
			idx := len(decls)
			decls = append(decls, gd)
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil {
				recv := receiverType(fn)
				methods[recv] = append(methods[recv], idx)
				methodsNamed[fn.Name.Name] = append(methodsNamed[fn.Name.Name], idx)
				continue
			}
			for _, n := range gd.names {
				byName[n] = append(byName[n], idx)
			}
		}
	}

	pkgImport, modErr := packageImportPath(root, d.Package)
	det := goDetector{pkgName: pkgName, imports: map[string]string{}}
	unresolved := map[string]bool{}
	var collision error
	if modErr != nil {
		unresolved["module path: "+modErr.Error()] = true
	}
	seen := map[int]bool{}
	var queue []int
	enqueue := func(idxs ...int) {
		for _, i := range idxs {
			if !seen[i] {
				seen[i] = true
				queue = append(queue, i)
			}
		}
	}
	enqueue(byName[d.Test]...)
	enqueue(byName["TestMain"]...)
	enqueue(byName["init"]...)
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		gd := decls[i]
		imports := fileImports[gd.file]
		if spec, ok := gd.node.(*ast.GenDecl); ok && spec.Tok == token.TYPE {
			for _, n := range gd.names {
				enqueue(methods[n]...)
			}
		}
		if fn, ok := gd.node.(*ast.FuncDecl); ok && fn.Recv != nil {
			enqueue(byName[receiverType(fn)]...)
		}
		var visit func(n ast.Node) bool
		visit = func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				// The parser binds locals and same-file declarations, never an import.
				if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil {
					if p, isImport := imports[id.Name]; isImport {
						if other, taken := det.imports[id.Name]; taken && other != p && collision == nil {
							collision = fmt.Errorf("check %s: the generated test file cannot hold %s as both %s and %s; alias one import in the test files", d.ID, id.Name, other, p)
						}
						det.imports[id.Name] = p
						switch {
						case p == pkgImport && siblingNames[x.Sel.Name]:
							unresolved[fmt.Sprintf("test-side declaration %s of the package under test", x.Sel.Name)] = true
						case p != pkgImport && !stdlib(p):
							unresolved["import "+p] = true
						}
						return false
					}
					if len(byName[id.Name]) == 0 {
						for _, p := range fileMisnamed[gd.file] {
							unresolved[fmt.Sprintf("import %s (its package name differs from its path; alias it)", p)] = true
						}
					}
				}
				// A test-side method may sit on a production type, which no
				// test-side type declaration reaches, so the name reaches it.
				enqueue(methodsNamed[x.Sel.Name]...)
				ast.Inspect(x.X, visit)
				return false
			case *ast.Ident:
				enqueue(byName[x.Name]...)
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					if v, err := strconv.Unquote(x.Value); err == nil && (v == "testdata" || strings.HasPrefix(v, "testdata/")) {
						if !inputCovers(d, path.Join(d.Package, v)) {
							unresolved[fmt.Sprintf("testdata path %q (declare it with --input)", v)] = true
						}
					}
				}
			}
			return true
		}
		ast.Inspect(gd.node, visit)
		for _, p := range gd.embeds {
			if !embedCovered(dir, d, p) {
				unresolved[fmt.Sprintf("embed pattern %q (declare the files it matches with --input)", strings.TrimPrefix(p, "all:"))] = true
			}
		}
	}
	if collision != nil {
		return goDetector{}, collision
	}
	for _, f := range files {
		if f.file.Name.Name != pkgName {
			continue
		}
		for _, imp := range f.file.Imports {
			if imp.Name != nil && imp.Name.Name == "." {
				p, _ := strconv.Unquote(imp.Path.Value)
				unresolved["dot import "+p] = true
			}
		}
		for _, p := range blankImports(f.file) {
			if !containsString(det.blank, p) {
				det.blank = append(det.blank, p)
			}
			if !stdlib(p) {
				unresolved["side-effect import "+p] = true
			}
		}
	}
	sort.Strings(det.blank)

	idxs := make([]int, 0, len(seen))
	for i := range seen {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		det.decls = append(det.decls, decls[i])
	}
	for u := range unresolved {
		det.unresolved = append(det.unresolved, u)
	}
	sort.Strings(det.unresolved)
	return det, nil
}

func declNames(decl ast.Decl) []string {
	switch x := decl.(type) {
	case *ast.FuncDecl:
		if x.Recv != nil {
			return []string{receiverType(x) + "." + x.Name.Name}
		}
		return []string{x.Name.Name}
	case *ast.GenDecl:
		var names []string
		for _, spec := range x.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				for _, n := range s.Names {
					names = append(names, n.Name)
				}
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			}
		}
		return names
	}
	return nil
}

func receiverType(fn *ast.FuncDecl) string {
	t := fn.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

// importNames maps each import's local name in the file to its path.
func importNames(f *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := assumedName(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		names[name] = p
	}
	return names
}

// misnamedImports returns the unnamed imports the file never refers to by their
// assumed name. A file must use each import, so their package names differ.
func misnamedImports(f *ast.File) []string {
	used := map[string]bool{}
	for _, id := range f.Unresolved {
		used[id.Name] = true
	}
	var paths []string
	for _, imp := range f.Imports {
		if imp.Name != nil {
			continue
		}
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && !used[assumedName(p)] {
			paths = append(paths, p)
		}
	}
	return paths
}

// assumedName is the package name an unnamed import is referred to by, under
// the goimports convention: the last path element that is not a major-version
// suffix, without a "go-" prefix, cut at the first "." or "-".
func assumedName(p string) string {
	base := path.Base(p)
	if len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" && strings.Contains(p, "/") {
		base = path.Base(path.Dir(p))
	}
	base = strings.TrimPrefix(base, "go-")
	if i := strings.IndexAny(base, ".-"); i > 0 {
		base = base[:i]
	}
	return base
}

// blankImports returns the paths a file imports for side effects only.
func blankImports(f *ast.File) []string {
	var paths []string
	for _, imp := range f.Imports {
		if imp.Name != nil && imp.Name.Name == "_" {
			if p, err := strconv.Unquote(imp.Path.Value); err == nil {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// stdlib reports an import path of the standard library, whose first element
// has no dot.
func stdlib(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	return !strings.Contains(first, ".")
}

// inputCovers reports a declared input that is the path, or a file under it
// when the path names a directory. An input is always a file.
func inputCovers(d Declaration, p string) bool {
	for _, in := range d.Inputs {
		if in == p || strings.HasPrefix(in, p+"/") {
			return true
		}
	}
	return false
}

// embedDirectives reads the //go:embed comments between from and the end of a
// var declaration. It returns those above the declaration as source lines, which
// its slice lacks, and the patterns of them all.
func embedDirectives(comments []*ast.CommentGroup, from token.Pos, decl ast.Decl) (lead []byte, patterns []string) {
	for _, group := range comments {
		for _, c := range group.List {
			if c.Pos() < from || c.Pos() >= decl.End() {
				continue
			}
			args, ok := strings.CutPrefix(c.Text, "//go:embed")
			if !ok || (args != "" && args[0] != ' ' && args[0] != '\t') {
				continue
			}
			if c.Pos() < decl.Pos() {
				lead = append(append(lead, c.Text...), '\n')
			}
			patterns = append(patterns, embedPatterns(args)...)
		}
	}
	return lead, patterns
}

// embedPatterns splits a directive's arguments, which are bare or Go-quoted.
// A malformed argument ends the list.
func embedPatterns(args string) []string {
	var patterns []string
	for {
		args = strings.TrimLeft(args, " \t")
		if args == "" {
			return patterns
		}
		var p string
		if args[0] == '"' || args[0] == '`' {
			quoted, err := strconv.QuotedPrefix(args)
			if err != nil {
				return patterns
			}
			args = args[len(quoted):]
			if p, err = strconv.Unquote(quoted); err != nil {
				return patterns
			}
		} else {
			i := strings.IndexAny(args, " \t")
			if i < 0 {
				i = len(args)
			}
			p, args = args[:i], args[i:]
		}
		patterns = append(patterns, p)
	}
}

// embedCovered reports that a declared input falls under a package-relative
// embed pattern and that dir holds no matching file left undeclared.
func embedCovered(dir string, d Declaration, raw string) bool {
	pattern, all := strings.CutPrefix(raw, "all:")
	declared := map[string]bool{}
	covered := false
	for _, in := range d.Inputs {
		if rel, ok := strings.CutPrefix(in, d.Package+"/"); ok || d.Package == "." {
			if d.Package == "." {
				rel = in
			}
			declared[rel] = true
			covered = covered || embedMatches(pattern, rel, all)
		}
	}
	if !covered {
		return false
	}
	undeclared := false
	filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if embedMatches(pattern, rel, all) && !declared[rel] {
			undeclared = true
			return filepath.SkipAll
		}
		return nil
	})
	return !undeclared
}

// embedMatches reports a file go:embed would take for the pattern: one it names,
// or one under a directory it names, where only all: admits a path element
// starting with . or _.
func embedMatches(pattern, rel string, all bool) bool {
	if ok, _ := path.Match(pattern, rel); ok {
		return true
	}
	for p := path.Dir(rel); p != "."; p = path.Dir(p) {
		base := path.Base(rel)
		rel = path.Dir(rel)
		if !all && (base[0] == '.' || base[0] == '_') {
			return false
		}
		if ok, _ := path.Match(pattern, p); ok {
			return true
		}
	}
	return false
}

// packageImportPath derives the import path of a repository-relative package
// directory from the nearest go.mod at or above it inside the repository.
func packageImportPath(root, pkgDir string) (string, error) {
	rel := pkgDir
	for {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel), "go.mod"))
		if err == nil {
			mod := modulePath(data)
			if mod == "" {
				return "", fmt.Errorf("%s/go.mod names no module", rel)
			}
			sub := strings.TrimPrefix(strings.TrimPrefix(pkgDir, rel), "/")
			if rel == "." && pkgDir != "." {
				sub = pkgDir
			}
			return strings.TrimSuffix(path.Join(mod, sub), "/"), nil
		}
		if rel == "." {
			return "", fmt.Errorf("no go.mod at or above %s", pkgDir)
		}
		rel = path.Dir(rel)
	}
}

func modulePath(gomod []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(gomod))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			rest, _, _ = strings.Cut(rest, "//")
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// goDetectorOf digests a go-test binding: its tracked declarations and its
// declared inputs.
func goDetectorOf(root string, d Declaration) (Detector, goDetector, error) {
	gd, err := analyzeGoTest(root, d)
	if err != nil {
		return Detector{}, goDetector{}, err
	}
	files, err := inputDigests(root, d.ID, d.Inputs)
	if err != nil {
		return Detector{}, goDetector{}, err
	}
	det := Detector{Files: files, Unresolved: gd.unresolved}
	all := sha256.New()
	fmt.Fprintf(all, "go-test\x00%s\x00%s\n", d.Package, d.Test)
	for _, decl := range gd.decls {
		sum := sha256.Sum256(decl.src)
		dd := DeclDigest{File: decl.file, Name: decl.name, SHA256: hex.EncodeToString(sum[:])}
		det.Decls = append(det.Decls, dd)
		fmt.Fprintf(all, "decl\x00%s\x00%s\n", dd.Name, dd.SHA256)
	}
	for _, p := range gd.blank {
		fmt.Fprintf(all, "blank\x00%s\n", p)
	}
	for _, fd := range files {
		fmt.Fprintf(all, "file\x00%s\x00%s\n", fd.Path, fd.SHA256)
	}
	det.Digest = hex.EncodeToString(all.Sum(nil))
	return det, gd, nil
}

// source renders the tracked declarations as one test file that imports only
// the packages they name.
func (gd goDetector) source() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by steer check replay. DO NOT EDIT.\n\npackage %s\n\n", gd.pkgName)
	if len(gd.imports) > 0 || len(gd.blank) > 0 {
		names := make([]string, 0, len(gd.imports))
		for n := range gd.imports {
			names = append(names, n)
		}
		sort.Strings(names)
		b.WriteString("import (\n")
		for _, n := range names {
			fmt.Fprintf(&b, "\t%s %q\n", n, gd.imports[n])
		}
		for _, p := range gd.blank {
			fmt.Fprintf(&b, "\t_ %q\n", p)
		}
		b.WriteString(")\n\n")
	}
	for _, decl := range gd.decls {
		b.Write(decl.src)
		b.WriteString("\n\n")
	}
	return b.Bytes()
}
