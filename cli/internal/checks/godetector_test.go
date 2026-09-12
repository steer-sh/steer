package checks

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const respGoMod = "module example.com/svc\n\ngo 1.21\n"

const respProduction = "package resp\n\nfunc Response() string { return `{\"id\": 1}` }\n\nfunc Unrelated() int { return 2 }\n"

// readTestdata returns a fixture source file kept outside Go string literals.
func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var respCheck = Declaration{ID: "resp-fields", Kind: KindGoTest, Package: "svc/resp", Test: "TestResponse"}

// goCheckRepo makes a repository holding a Go module under svc/, keeping the
// caller's build cache so replays do not rebuild the standard library.
func goCheckRepo(t *testing.T, testSource string) string {
	t.Helper()
	cache, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	modcache, _ := exec.Command("go", "env", "GOMODCACHE").Output()
	t.Setenv("GOCACHE", strings.TrimSpace(string(cache)))
	t.Setenv("GOMODCACHE", strings.TrimSpace(string(modcache)))
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "svc/go.mod", respGoMod)
	writeFile(t, root, "svc/resp/resp.go", respProduction)
	writeFile(t, root, "svc/resp/resp_test.go", testSource)
	return root
}

func declNamesOf(det Detector) []string {
	var names []string
	for _, d := range det.Decls {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

// The detector of a Go test is the test and the test-side declarations it
// reaches, variables included; production code and unrelated tests are not.
func TestAGoDetectorTracksTheTestSideDeclarationsItReaches(t *testing.T) {
	respTests := readTestdata(t, "resp_test.go.txt")
	root := goCheckRepo(t, respTests)
	det, err := DetectorOf(root, respCheck)
	if err != nil {
		t.Fatalf("DetectorOf: %v", err)
	}
	if got := strings.Join(declNamesOf(det), ","); got != "TestResponse,assertFields,requiredFields" {
		t.Fatalf("tracked %s; want TestResponse,assertFields,requiredFields", got)
	}

	digestAfter := func(rel, content string) string {
		t.Helper()
		writeFile(t, root, rel, content)
		d, err := DetectorOf(root, respCheck)
		if err != nil {
			t.Fatalf("DetectorOf: %v", err)
		}
		return d.Digest
	}
	if digestAfter("svc/resp/resp.go", strings.Replace(respProduction, "return 2", "return 3", 1)) != det.Digest {
		t.Errorf("a production edit changed the detector")
	}
	if digestAfter("svc/resp/resp_test.go", strings.Replace(respTests, "otherHelper() != 2", "otherHelper() != 3", 1)) != det.Digest {
		t.Errorf("an edit to an unrelated test changed the detector")
	}
	if digestAfter("svc/resp/resp_test.go", strings.Replace(respTests, `[]string{"id"}`, `[]string{"id", "name"}`, 1)) == det.Digest {
		t.Errorf("an edit to requiredFields left the detector unchanged")
	}
	if digestAfter("svc/resp/resp_test.go", strings.Replace(respTests, "\"testing\"\n", "\"testing\"\n\n\t_ \"embed\"\n", 1)) == det.Digest {
		t.Errorf("an added side-effect import left the detector unchanged")
	}
}

// What the analysis sees but cannot track is named: an imported helper
// package and a testdata path no declared input covers. Methods of a reached
// type are tracked.
func TestAGoDetectorNamesWhatItCannotTrack(t *testing.T) {
	src := readTestdata(t, "resp_test.go.txt") + "\n" + readTestdata(t, "loads_test.go.txt")
	src = strings.Replace(src, "\"testing\"\n", "\"testing\"\n\n\t\"example.com/svc/testutil\"\n", 1)
	root := goCheckRepo(t, src)
	loads := Declaration{ID: "loads", Kind: KindGoTest, Package: "svc/resp", Test: "TestLoads"}
	det, err := DetectorOf(root, loads)
	if err != nil {
		t.Fatalf("DetectorOf: %v", err)
	}
	if got := strings.Join(declNamesOf(det), ","); got != "TestLoads,fixture,fixture.body" {
		t.Fatalf("tracked %s; want TestLoads,fixture,fixture.body", got)
	}
	got := strings.Join(det.Unresolved, "|")
	if !strings.Contains(got, "import example.com/svc/testutil") || !strings.Contains(got, `testdata path "testdata/golden.json"`) {
		t.Fatalf("unresolved %q; want the helper import and the testdata path", got)
	}

	writeFile(t, root, "svc/resp/testdata/golden.json", "{}")
	loads.Inputs = []string{"svc/resp/testdata/golden.json"}
	det, err = DetectorOf(root, loads)
	if err != nil {
		t.Fatalf("DetectorOf with input: %v", err)
	}
	if strings.Contains(strings.Join(det.Unresolved, "|"), "testdata") || len(det.Files) != 1 {
		t.Fatalf("with the input declared: unresolved %q, files %+v; want the testdata path covered", det.Unresolved, det.Files)
	}
}

// A module line may carry a comment, an unnamed import is referred to by its
// assumed package name, and a side-effect import reaches the generated file.
func TestGoImportsAndModulesInTheirUnusualForms(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "svc/go.mod", "module example.com/svc // main module\n\ngo 1.21\n")
	if got, err := packageImportPath(root, "svc/resp"); err != nil || got != "example.com/svc/resp" {
		t.Fatalf("packageImportPath = %q, %v; want example.com/svc/resp", got, err)
	}

	for p, want := range map[string]string{
		"gopkg.in/yaml.v2":           "yaml",
		"github.com/mattn/go-isatty": "isatty",
		"example.com/m/v2":           "m",
		"net/http":                   "http",
	} {
		if got := assumedName(p); got != want {
			t.Errorf("assumedName(%q) = %q; want %q", p, got, want)
		}
	}

	writeFile(t, root, "svc/resp/resp_test.go", readTestdata(t, "decode_test.go.txt"))
	gd, err := analyzeGoTest(root, Declaration{ID: "decode", Kind: KindGoTest, Package: "svc/resp", Test: "TestDecode"})
	if err != nil {
		t.Fatalf("analyzeGoTest: %v", err)
	}
	src := string(gd.source())
	for _, want := range []string{`yaml "gopkg.in/yaml.v2"`, `_ "embed"`, `_ "example.com/driver"`} {
		if !strings.Contains(src, want) {
			t.Errorf("generated file lacks %s:\n%s", want, src)
		}
	}
	if !strings.Contains(strings.Join(gd.unresolved, "|"), "side-effect import example.com/driver") {
		t.Errorf("unresolved %q; want the side-effect import named", gd.unresolved)
	}
}

// A test-side method on a production type is tracked when the test calls it,
// and a declared testdata file covers only its own literal.
func TestAGoDetectorReachesMethodsOnProductionTypesAndOneTestdataFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "svc/go.mod", respGoMod)
	writeFile(t, root, "svc/resp/resp_test.go", readTestdata(t, "client_test.go.txt"))
	d := Declaration{ID: "client", Kind: KindGoTest, Package: "svc/resp", Test: "TestClient",
		Inputs: []string{"svc/resp/testdata/a.json"}}
	gd, err := analyzeGoTest(root, d)
	if err != nil {
		t.Fatalf("analyzeGoTest: %v", err)
	}
	var names []string
	for _, decl := range gd.decls {
		names = append(names, decl.name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "Client.mustGet,TestClient" {
		t.Fatalf("tracked %s; want Client.mustGet,TestClient", got)
	}
	got := strings.Join(gd.unresolved, "|")
	if !strings.Contains(got, `"testdata/b.json"`) || strings.Contains(got, `"testdata/a.json"`) {
		t.Fatalf("unresolved %q; want b.json named and a.json covered", got)
	}
}

func respCase(name string, exit int, diagnostic, production string) Case {
	return Case{Name: name, ExitCode: exitCode(exit), Diagnostic: diagnostic, Provenance: "planted",
		Files: map[string]string{"svc/go.mod": respGoMod, "svc/resp/resp.go": production}}
}

// A Go test validates when the named test itself passes the known-good case
// and fails the defect case with the diagnostic. The package's other tests are
// not in the replay, so production they need is not required.
func TestAGoTestValidatesWhenTheNamedTestRunsBothWays(t *testing.T) {
	root := goCheckRepo(t, readTestdata(t, "resp_test.go.txt"))
	if _, _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	fixed := "package resp\n\nfunc Response() string { return `{\"id\": 1}` }\n"
	broken := "package resp\n\nfunc Response() string { return `{\"name\": 1}` }\n"
	writeCases(t, root, respCheck.ID,
		respCase("fixed", 0, "", fixed),
		respCase("omits-id", 1, "missing field id", broken))
	run, err := Add(root, respCheck)
	if err != nil {
		t.Fatalf("Add: %v (cases %+v)", err, run.Cases)
	}
	for _, c := range run.Cases {
		if c.Outcome != OutcomeMatched {
			t.Errorf("case %+v; want matched", c)
		}
	}
	if len(run.Detector.Decls) != 3 {
		t.Fatalf("receipt tracks %d declarations; want 3", len(run.Detector.Decls))
	}
}

// A defect case whose production does not build never runs the named test, so
// it cannot satisfy the expected failure; nor may a case add its own test file.
func TestABuildFailureDoesNotSatisfyAGoDefectCase(t *testing.T) {
	root := goCheckRepo(t, readTestdata(t, "resp_test.go.txt"))
	if _, _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	fixed := "package resp\n\nfunc Response() string { return `{\"id\": 1}` }\n"
	writeCases(t, root, respCheck.ID,
		respCase("fixed", 0, "", fixed),
		respCase("does-not-build", 1, "missing field id", "package resp\n\nfunc Response() string { return 1 }\n"))
	run, err := Add(root, respCheck)
	if !errors.Is(err, ErrNotValidated) || run.Verdict != VerdictUnavailable {
		t.Fatalf("verdict %s err %v; want unavailable", run.Verdict, err)
	}
	for _, c := range run.Cases {
		if c.Name == "does-not-build" && (c.Outcome != OutcomeInvalid || !strings.Contains(c.Reason, "did not run")) {
			t.Errorf("case %+v; want invalid because the test did not run", c)
		}
	}

	extra := respCase("fixed", 0, "", fixed)
	extra.Files["svc/resp/extra_test.go"] = "package resp\n"
	writeCases(t, root, respCheck.ID, extra, respCase("omits-id", 1, "missing field id", fixed))
	run, _ = Add(root, respCheck)
	if run.Cases[0].Outcome != OutcomeInvalid || !strings.Contains(run.Cases[0].Reason, "test file") {
		t.Fatalf("a case adding a test file gave %+v; want invalid", run.Cases[0])
	}
}
