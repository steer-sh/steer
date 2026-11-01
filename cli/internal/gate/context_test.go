package gate

import (
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
