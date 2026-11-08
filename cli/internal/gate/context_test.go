package gate

import (
	"fmt"
	"strings"
	"testing"
)

// TestCallerContextReadsTheIndex: a file outside the diff lands with its index
// content, so an unstaged caller update must not reach the reviewer as the caller the
// commit carries.
func TestCallerContextReadsTheIndex(t *testing.T) {
	cfg := Defaults()
	root := buildRepo(t, map[string]string{
		"api.go":    "func Fetch(id int) {}\n",
		"client.go": "func run() { Fetch(1) }\n",
		"other.go":  "func other() { Fetch(2) }\n",
	})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "api.go", "func Fetch(id int, ctx Context) {}\n")
	gitCmd(t, root, "add", "api.go")
	writeFileAt(t, root, "client.go", "func run() { Fetch(1, ctx) }\n")
	writeFileAt(t, root, "stray.go", "func stray() { Fetch(3) }\n")

	block := BuildContext(cfg, root, map[string]bool{"Fetch": true}, []string{"api.go"})
	if !strings.Contains(block, "Fetch(1)") {
		t.Error("the caller must reach the reviewer as the commit carries it")
	}
	if strings.Contains(block, "Fetch(1, ctx)") {
		t.Error("an unstaged caller update does not land, so it must not read as the caller")
	}
	if !strings.Contains(block, "Fetch(2)") {
		t.Error("a clean caller must still reach the reviewer")
	}
	if strings.Contains(block, "stray.go") {
		t.Error("an untracked file is not a caller the commit carries")
	}
}

// TestCallerContextFromASubdirectory: a commit made from a subdirectory reviews the
// whole commit, so its callers come from the whole tree and a changed file is never
// attached as one.
func TestCallerContextFromASubdirectory(t *testing.T) {
	cfg := Defaults()
	root := buildRepo(t, map[string]string{
		"sub/api.go": "func Fetch(id int) {}\n",
		"client.go":  "func run() { Fetch(1) }\n",
	})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "sub/api.go", "func Fetch(id int, ctx Context) {}\n")
	gitCmd(t, root, "add", "sub/api.go")

	block := BuildContext(cfg, root+"/sub", map[string]bool{"Fetch": true}, []string{"sub/api.go"})
	if !strings.Contains(block, "Fetch(1)") {
		t.Error("a caller outside the commit's directory must reach the reviewer")
	}
	if strings.Contains(block, "ctx Context") {
		t.Error("a file in the diff must never be attached as an unchanged caller")
	}
}

// TestCallerContextSkipsOnlyTheFileThatOverruns: callers rank by mentions, not size,
// so a higher-ranked file that overruns the budget must not shut out a later one that fits.
func TestCallerContextSkipsOnlyTheFileThatOverruns(t *testing.T) {
	cfg := Defaults()
	cfg.MaxContextChars = 400
	root := buildRepo(t, map[string]string{
		"api.go":   "func Fetch() {}\nfunc Store() {}\n",
		"large.go": "func both() { Fetch(); Store() }\n" + strings.Repeat("// pad\n", 200),
		"small.go": "func one() { Fetch() }\n",
	})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "api.go", "func Fetch(id int) {}\nfunc Store(id int) {}\n")
	gitCmd(t, root, "add", "api.go")

	block := BuildContext(cfg, root, map[string]bool{"Fetch": true, "Store": true}, []string{"api.go"})
	if strings.Contains(block, "large.go") {
		t.Error("a caller larger than the whole budget must not be attached")
	}
	if !strings.Contains(block, "small.go") {
		t.Error("a later caller that fits must still reach the reviewer")
	}
}

// stagedContext runs the engine's context path over the staged change.
func stagedContext(t *testing.T, cfg Config, root string) string {
	t.Helper()
	spec := DiffSpec{StagedOnly: true}
	paths, ok := ChangedFiles(cfg, root, spec)
	if !ok {
		t.Fatal("ChangedFiles failed")
	}
	diff, ok := GetDiff(cfg, root, spec)
	if !ok {
		t.Fatal("GetDiff failed")
	}
	return BuildContext(cfg, root, ChangedTerms(diff, paths), paths)
}

// TestCallerContextIsLanguageNeutral: a Go receiver method and struct type, a Java
// method and a Python function each reach their unchanged consumers, and files that
// share only names common across the repository do not.
func TestCallerContextIsLanguageNeutral(t *testing.T) {
	cfg := Defaults()
	cfg.MaxContextFiles = 4
	cfg.MaxContextChars = 280
	common := "func return error int public account invoice def class type struct Request\n"
	files := map[string]string{
		"store/engine.go": "package store\n\ntype Engine struct {\n\tlimit int\n}\n\n" +
			"func (e *Engine) Handle(req Request) error {\n\treturn nil\n}\n",
		"java/Ledger.java": "public class Ledger {\n    public int computeBalance(int account) {\n" +
			"        return account;\n    }\n}\n",
		"py/billing.py":    "def settle_invoice(invoice):\n    return invoice\n",
		"cmd/serve.go":     "package cmd\n\nfunc serve(r Request) error { return eng.Handle(r) }\n",
		"cmd/build.go":     "package cmd\n\nfunc build() *store.Engine { return nil }\n",
		"java/Report.java": "class Report { int total(Ledger l) { return l.computeBalance(7); } }\n",
		"py/jobs.py":       "from billing import settle_invoice\n\ndef run(x):\n    return settle_invoice(x)\n",
	}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("misc/common%02d.txt", i)] = common
	}
	for i := 0; i < 3; i++ {
		files[fmt.Sprintf("misc/weak%d.txt", i)] = "deadline currency\n"
	}
	root := buildRepo(t, files)
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "store/engine.go", "package store\n\ntype Engine struct {\n\tlimit int\n"+
		"\tbudget int\n}\n\nfunc (e *Engine) Handle(req Request, deadline int) error {\n\treturn nil\n}\n")
	writeFileAt(t, root, "java/Ledger.java", "public class Ledger {\n"+
		"    public int computeBalance(int account, int day) {\n        return account;\n    }\n}\n")
	writeFileAt(t, root, "py/billing.py", "def settle_invoice(invoice, currency):\n    return invoice\n")
	gitCmd(t, root, "add", "-A")

	block := stagedContext(t, cfg, root)
	for _, consumer := range []string{"cmd/serve.go", "cmd/build.go", "java/Report.java", "py/jobs.py"} {
		if !strings.Contains(block, "--- "+consumer+" ") {
			t.Errorf("unchanged consumer %s must reach the reviewer:\n%s", consumer, block)
		}
	}
	if strings.Contains(block, "misc/") {
		t.Errorf("a file sharing only common or weaker names must not displace a consumer:\n%s", block)
	}
	if !strings.Contains(block, "--- cmd/serve.go (unchanged; shares: Handle) ---") {
		t.Errorf("a file's header must name the rare term it shares, not the common ones:\n%s", block)
	}
}

// TestCallerContextFindsABodyOnlyChange: a change inside a function body whose name is
// on no changed line still reaches the function's callers through the enclosing line.
func TestCallerContextFindsABodyOnlyChange(t *testing.T) {
	cfg := Defaults()
	root := buildRepo(t, map[string]string{
		"cache.go": "package cache\n\nfunc (c *Cache) Evict(key string) {\n\tc.count--\n" +
			"\tdelete(c.items, key)\n}\n",
		"user.go": "package cache\n\nfunc use(c *Cache) { c.Evict(\"k\") }\n",
	})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "cache.go", "package cache\n\nfunc (c *Cache) Evict(key string) {\n"+
		"\tc.count -= 2\n\tdelete(c.items, key)\n}\n")
	gitCmd(t, root, "add", "-A")

	if block := stagedContext(t, cfg, root); !strings.Contains(block, "--- user.go ") {
		t.Errorf("the caller of a function whose body alone changed must reach the reviewer:\n%s", block)
	}
}

// TestCallerContextFindsAnIndentedMethodBodyChange: a method declared inside a class
// encloses its body by indentation, so a change to that body alone reaches the callers.
func TestCallerContextFindsAnIndentedMethodBodyChange(t *testing.T) {
	cfg := Defaults()
	before := "class Invoice:\n    def __init__(self):\n        self.total = 0\n\n" +
		"    def recalculate_totals(self):\n        self.total = 1\n        return self.total\n"
	root := buildRepo(t, map[string]string{
		"invoice.py": before,
		"report.py":  "def render(doc):\n    return doc.recalculate_totals()\n",
	})
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "invoice.py", strings.Replace(before, "self.total = 1", "self.total = 2", 1))
	gitCmd(t, root, "add", "-A")

	if block := stagedContext(t, cfg, root); !strings.Contains(block, "--- report.py ") {
		t.Errorf("the caller of an indented method whose body alone changed must reach the reviewer:\n%s", block)
	}
}

// TestCallerContextKeepsAWidelyCalledName: a changed function that most files do not
// call still selects its callers, however many more there are than the block holds.
func TestCallerContextKeepsAWidelyCalledName(t *testing.T) {
	cfg := Defaults()
	files := map[string]string{"api.go": "package api\n\nfunc Fetch(id int) {}\n"}
	for i := 0; i < 9; i++ {
		files[fmt.Sprintf("caller%d.go", i)] = "package api\n\nfunc use() { Fetch(1) }\n"
	}
	for i := 0; i < 11; i++ {
		files[fmt.Sprintf("other%02d.go", i)] = "package api\n"
	}
	root := buildRepo(t, files)
	gitCmd(t, root, verb, "-qm", "init")
	writeFileAt(t, root, "api.go", "package api\n\nfunc Fetch(id int, ctx Context) {}\n")
	gitCmd(t, root, "add", "-A")

	if block := stagedContext(t, cfg, root); !strings.Contains(block, "Fetch(1)") {
		t.Errorf("a name 9 of 20 unchanged files call must still select its callers:\n%s", block)
	}
}

// TestChangedTermsReadTheEnclosingLine: the enclosing line is the nearest earlier hunk
// line that opens a scope, or the hunk header's funcname text before any.
func TestChangedTermsReadTheEnclosingLine(t *testing.T) {
	header := "diff --git a/c.go b/c.go\n--- a/c.go\n+++ b/c.go\n" +
		"@@ -4,1 +4,1 @@ func (c *Cache) Evict(key string) {\n-\tc.count--\n+\tc.count -= 2\n"
	terms := ChangedTerms(header, nil)
	for _, want := range []string{"count", "Cache", "Evict", "key", "string"} {
		if !terms[want] {
			t.Errorf("terms %v lack %q from the hunk header's funcname", sortedKeys(terms), want)
		}
	}

	inHunk := "diff --git a/c.go b/c.go\n--- a/c.go\n+++ b/c.go\n" +
		"@@ -1,4 +1,4 @@ func Previous() {\n func Evict() {\n-\tcount--\n+\tcount -= 2\n }\n"
	terms = ChangedTerms(inHunk, nil)
	if !terms["Evict"] || terms["Previous"] {
		t.Errorf("terms %v: the scope line inside the hunk encloses the change, not the header's",
			sortedKeys(terms))
	}
	if !terms["count"] {
		t.Errorf("terms %v lack the changed line's own words", sortedKeys(terms))
	}
}

// TestSearchTermsAreCapped: a large diff sends at most maxSearchTerms terms to git
// grep, keeping the longest.
func TestSearchTermsAreCapped(t *testing.T) {
	terms := map[string]bool{"a_much_longer_identifier_name": true}
	for i := 0; i < maxSearchTerms+50; i++ {
		terms[fmt.Sprintf("t%04d", i)] = true
	}
	got := searchTerms(terms)
	if len(got) != maxSearchTerms {
		t.Fatalf("searchTerms kept %d terms, want %d", len(got), maxSearchTerms)
	}
	if got[0] != "a_much_longer_identifier_name" {
		t.Errorf("searchTerms()[0] = %q, want the longest term first", got[0])
	}
}
