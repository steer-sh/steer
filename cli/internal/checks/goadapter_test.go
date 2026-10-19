package checks

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

// suiteTests indents its functions so that no fixture line reads as a test of
// this package to scripts/revert-test.py.
const suiteTests = `package p

import "testing"

	func TestGood(t *testing.T) { t.Log("good-log") }

	func TestSkipped(t *testing.T) { t.Skip("not here") }

	func TestBad(t *testing.T) {
		t.Run("sub", func(t *testing.T) { t.Error("boom-diagnostic") })
	}
`

// suiteRepo makes a Go module with a package of passing, skipped and failing
// tests, a package that does not build, and bindings on all of them plus one
// test that does not exist and one package the run never reaches.
func suiteRepo(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the go shim needs bash")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs the go toolchain")
	}
	cache, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", strings.TrimSpace(string(cache)))
	testhome.Set(t, t.TempDir())
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/suite\n\ngo 1.22\n")
	writeFile(t, root, "p/p.go", "package p\n")
	writeFile(t, root, "p/p_test.go", suiteTests)
	writeFile(t, root, "q/q.go", "package q\n\nfunc F() int { return undefinedName }\n")
	writeFile(t, root, "q/q_test.go", "package q\n\nimport \"testing\"\n\nfunc TestQ(t *testing.T) {}\n")
	m := Manifest{Version: ManifestVersion, Checks: []Declaration{
		{ID: "good", Kind: KindGoTest, Package: "p", Test: "TestGood"},
		{ID: "bad", Kind: KindGoTest, Package: "p", Test: "TestBad"},
		{ID: "skipped", Kind: KindGoTest, Package: "p", Test: "TestSkipped"},
		{ID: "absent", Kind: KindGoTest, Package: "p", Test: "TestAbsent"},
		{ID: "broken", Kind: KindGoTest, Package: "q", Test: "TestQ"},
		{ID: "elsewhere", Kind: KindGoTest, Package: "r", Test: "TestR"},
	}}
	if err := Save(root, m); err != nil {
		t.Fatal(err)
	}
	return root
}

// goShim puts a go on PATH that counts its calls and runs the real toolchain.
func goShim(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	writeFile(t, dir, "go", "#!/usr/bin/env bash\necho x >> "+calls+"\nexec "+real+" \"$@\"\n")
	os.Chmod(filepath.Join(dir, "go"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// One go test run yields a record per bound test in each package it reported:
// the named test's own result, an unbuilt package or an unreported test as
// unavailable, and nothing for a package the run never reached. The suite's
// exit status and failure output survive; verbose framing and passing logs do
// not appear.
func TestGoSuiteAttributesNamedTestsFromOneRun(t *testing.T) {
	root := suiteRepo(t)
	calls := goShim(t)
	var stdout, stderr bytes.Buffer
	code, records, err := RunGoSuite(root, GoSuiteOptions{Args: []string{"./..."}, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatalf("RunGoSuite: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit %d; want go test's 1\n%s%s", code, stdout.String(), stderr.String())
	}
	got := byCheck(records)
	want := map[string]string{
		"good": OutcomePassed, "bad": OutcomeFailed, "skipped": OutcomeSkipped,
		"absent": OutcomeUnavailable, "broken": OutcomeUnavailable,
	}
	if len(records) != len(want) {
		t.Fatalf("records %+v; want %d, none for the unreached package", records, len(want))
	}
	for id, outcome := range want {
		if got[id].Outcome != outcome {
			t.Errorf("%s: %s (%s); want %s", id, got[id].Outcome, got[id].Reason, outcome)
		}
	}
	if !strings.Contains(got["broken"].Reason, "build") || !strings.Contains(got["absent"].Reason, "without a result") {
		t.Errorf("unavailable reasons %q / %q; want the build failure and the missing result told apart", got["broken"].Reason, got["absent"].Reason)
	}
	if !strings.Contains(got["bad"].Excerpt, "boom-diagnostic") || got["bad"].DetectorDigest == "" {
		t.Errorf("bad record %+v; want its own output and detector digest", got["bad"])
	}
	rendered := stdout.String()
	for _, need := range []string{"boom-diagnostic", "undefinedName", "FAIL\texample.com/suite/p"} {
		if !strings.Contains(rendered, need) {
			t.Errorf("output lacks %q:\n%s", need, rendered)
		}
	}
	for _, hidden := range []string{"=== RUN", "good-log", `"Action"`} {
		if strings.Contains(rendered, hidden) {
			t.Errorf("output shows %q:\n%s", hidden, rendered)
		}
	}
	if logged, _ := os.ReadFile(calls); strings.Count(string(logged), "x") != 1 {
		t.Fatalf("go ran %d times; want once", strings.Count(string(logged), "x"))
	}
	if stored, _ := Executions(root); len(stored) != len(want) {
		t.Fatalf("stream holds %d records; want %d", len(stored), len(want))
	}
}

// A pass the toolchain replays from its cache is recorded as cached, apart
// from a fresh pass.
func TestGoSuiteRecordsACachedPassApartFromAFreshOne(t *testing.T) {
	root := suiteRepo(t)
	args := []string{"./p", "-run", "^TestGood$"}
	var outcomes []string
	for range 2 {
		code, records, err := RunGoSuite(root, GoSuiteOptions{Args: args})
		if err != nil || code != 0 {
			t.Fatalf("RunGoSuite: exit %d err %v", code, err)
		}
		outcomes = append(outcomes, byCheck(records)["good"].Outcome)
	}
	if outcomes[0] != OutcomePassed || outcomes[1] != OutcomePassedCached {
		t.Fatalf("outcomes %v; want a fresh pass then a cached one", outcomes)
	}
}
