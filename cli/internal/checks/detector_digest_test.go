package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestOf(t *testing.T, root string, d Declaration) string {
	t.Helper()
	det, err := DetectorOf(root, d)
	if err != nil {
		t.Fatalf("DetectorOf: %v", err)
	}
	return det.Digest
}

func chmodFile(t *testing.T, root, rel string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(filepath.Join(root, filepath.FromSlash(rel)), mode); err != nil {
		t.Fatal(err)
	}
}

func freshnessNow(t *testing.T, root string, d Declaration) Freshness {
	t.Helper()
	history, err := History(root)
	if err != nil {
		t.Fatal(err)
	}
	return FreshnessOf(root, d, history)
}

// A chmod with no byte change selects another interpreter, so it stales the
// receipt, and one replay under the new mode makes the check current again.
func TestAChmodStalesTheReceiptUntilOneReplay(t *testing.T) {
	root := checkRepo(t, noCountScript)
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	if run, err := Validate(root, noCount); err != nil || run.Verdict != VerdictValidated {
		t.Fatalf("Validate: verdict %s err %v (%s)", run.Verdict, err, run.Reason)
	}
	if f := freshnessNow(t, root, noCount); f.State != FreshCurrent {
		t.Fatalf("freshness %s with nothing changed; want current", f.State)
	}
	chmodFile(t, root, noCount.Script, 0o755)
	f := freshnessNow(t, root, noCount)
	if f.State != FreshStale || strings.Join(f.Changed, ",") != "detector" {
		t.Fatalf("after chmod: %s %v; want stale on the detector", f.State, f.Changed)
	}
	if run, err := Validate(root, noCount); err != nil || run.Verdict != VerdictValidated {
		t.Fatalf("replay: verdict %s err %v (%s)", run.Verdict, err, run.Reason)
	}
	if f := freshnessNow(t, root, noCount); f.State != FreshCurrent {
		t.Fatalf("freshness %s %v after one replay; want current", f.State, f.Changed)
	}
}

// A declared input of a Go test carries its executable bit into the digest.
func TestAGoInputsExecutableBitMovesTheDigest(t *testing.T) {
	root := goCheckRepo(t, readTestdata(t, "rand_test.go.txt"))
	writeFile(t, root, "svc/resp/tool.sh", "exit 0\n")
	d := Declaration{ID: "draw", Kind: KindGoTest, Package: "svc/resp", Test: "TestDraw", Inputs: []string{"svc/resp/tool.sh"}}
	before := digestOf(t, root, d)
	chmodFile(t, root, "svc/resp/tool.sh", 0o755)
	if digestOf(t, root, d) == before {
		t.Error("chmod +x on a declared input left the detector unchanged")
	}
}

// The digest holds what selects the interpreter and no other permission, so the
// umask a checkout ran under and a mode git does not record move nothing.
func TestTheDigestReadsOnlyTheExecutableBit(t *testing.T) {
	root := checkRepo(t, noCountScript)
	digestAt := func(mode os.FileMode) string {
		t.Helper()
		chmodFile(t, root, noCount.Script, mode)
		return digestOf(t, root, noCount)
	}
	plain, runnable := digestAt(0o644), digestAt(0o755)
	if plain == runnable {
		t.Fatal("the executable bit left the digest unchanged")
	}
	for _, mode := range []os.FileMode{0o600, 0o640, 0o664, 0o666} {
		if digestAt(mode) != plain {
			t.Errorf("mode %o moved the digest of a non-executable detector", mode)
		}
	}
	for _, mode := range []os.FileMode{0o700, 0o750, 0o775, 0o777} {
		if digestAt(mode) != runnable {
			t.Errorf("mode %o moved the digest of an executable detector", mode)
		}
	}
	// runsItself takes any execute bit, so the digest does too.
	if digestAt(0o654) != runnable {
		t.Error("a group execute bit reads as non-executable; runsItself runs that script under its shebang")
	}
}

// A new file a tracked pattern matches moves the digest, and removing it moves
// it back. A file only an untracked declaration's pattern matches moves nothing.
func TestAFileMatchingAnEmbedPatternMovesTheDigest(t *testing.T) {
	src := strings.Replace(readTestdata(t, "embed_test.go.txt"), "testdata/golden.json", "testdata/*.golden", 1)
	root := goCheckRepo(t, src)
	writeFile(t, root, "svc/resp/testdata/a.golden", "a")
	writeFile(t, root, "svc/resp/testdata/nested/n.golden", "n")
	d := Declaration{ID: "embeds", Kind: KindGoTest, Package: "svc/resp", Test: "TestGolden",
		Inputs: []string{"svc/resp/testdata/a.golden"}}
	before := digestOf(t, root, d)

	writeFile(t, root, "svc/resp/testdata/grouped.json", "{}")
	if digestOf(t, root, d) != before {
		t.Error("a file only an untracked declaration embeds moved the detector")
	}
	writeFile(t, root, "svc/resp/testdata/b.golden", "b")
	moved := digestOf(t, root, d)
	if moved == before {
		t.Fatal("a new file the pattern matches left the detector unchanged")
	}
	if err := os.Remove(filepath.Join(root, "svc/resp/testdata/b.golden")); err != nil {
		t.Fatal(err)
	}
	if digestOf(t, root, d) != before {
		t.Error("removing the new file did not restore the digest")
	}
}

// The import a tracked selector resolves to is detector input: the same source
// over another package is another test. An import only an untracked test uses
// moves nothing.
func TestAChangedImportPathMovesTheDigest(t *testing.T) {
	src := readTestdata(t, "rand_test.go.txt")
	root := goCheckRepo(t, src)
	d := Declaration{ID: "draw", Kind: KindGoTest, Package: "svc/resp", Test: "TestDraw"}
	digest := func(content string) string {
		t.Helper()
		writeFile(t, root, "svc/resp/resp_test.go", content)
		return digestOf(t, root, d)
	}
	base := digest(src)
	if digest(strings.Replace(src, `"math/rand"`, `"crypto/rand"`, 1)) == base {
		t.Error("math/rand to crypto/rand left the detector unchanged")
	}
	if digest(strings.Replace(src, `"strconv"`, `"example.com/strconv"`, 1)) != base {
		t.Error("an import only an untracked test uses moved the detector")
	}
}

// The same committed content reads one digest in a main checkout and a linked
// worktree: paths are repository-relative, git writes its own modes, .git is a
// directory in one and a file in the other, and commits move nothing under it.
func TestOneContentReadsOneDigestAcrossCheckouts(t *testing.T) {
	root := goCheckRepo(t, readTestdata(t, "rand_test.go.txt"))
	writeFile(t, root, "go.mod", respGoMod)
	writeFile(t, root, "root_test.go", "package svc\n\nimport (\n\t\"embed\"\n\t\"net/url\"\n\t\"os\"\n\t\"strconv\"\n\t\"testing\"\n)\n\n//go:embed *\nvar all embed.FS\n\nfunc TestAll(t *testing.T) {\n\t_, _, _, _ = all, url.Parse, os.Getenv, strconv.Itoa\n\t_ = t\n}\n")
	writeFile(t, root, "tool.sh", "exit 0\n")
	chmodFile(t, root, "tool.sh", 0o775)
	if err := os.RemoveAll(filepath.Join(root, "svc")); err != nil {
		t.Fatal(err)
	}
	d := Declaration{ID: "all", Kind: KindGoTest, Package: ".", Test: "TestAll",
		Inputs: []string{"tool.sh", "root_test.go", "go.mod"}}
	gitIn(t, root, "init", "-q")
	commitAll(t, root, "content")

	main, err := DetectorOf(root, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(main.Unresolved) != 0 {
		t.Errorf("unresolved %q; want .git outside what the pattern matches", main.Unresolved)
	}
	tree := filepath.Join(t.TempDir(), "linked")
	gitIn(t, root, "worktree", "add", "-q", tree)
	if got := digestOf(t, tree, d); got != main.Digest {
		t.Errorf("linked worktree digest %s; main checkout %s", got, main.Digest)
	}
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "moves .git only")
	if got := digestOf(t, root, d); got != main.Digest {
		t.Errorf("a commit moved the digest through .git: %s; was %s", got, main.Digest)
	}

	shuffled := d
	shuffled.Inputs = []string{"go.mod", "tool.sh", "root_test.go"}
	for i := 0; i < 32; i++ {
		if got := digestOf(t, root, shuffled); got != main.Digest {
			t.Fatalf("read %d digest %s; want %s", i, got, main.Digest)
		}
	}
}
