package checks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

// crashFixtures are test files, keyed by package, whose TestCrash takes the test
// binary down without a test-level result. Each file declares TestNever after it.
// Their functions are indented for scripts/revert-test.py, as in suiteTests.
var crashFixtures = map[string]string{
	"osexit": `package p

import (
	"fmt"
	"os"
	"testing"
)

	func TestCrash(t *testing.T) { fmt.Println("crash-marker"); os.Exit(1) }
`,
	"logfatal": `package p

import (
	"log"
	"testing"
)

	func TestCrash(t *testing.T) { log.Fatal("crash-marker") }
`,
	"goroutine": `package p

import (
	"testing"
	"time"
)

	func TestCrash(t *testing.T) {
		go func() { panic("crash-marker") }()
		time.Sleep(time.Minute)
	}
`,
	"subtest": `package p

import (
	"fmt"
	"os"
	"testing"
)

	func TestCrash(t *testing.T) {
		t.Run("inner", func(t *testing.T) { fmt.Println("crash-marker"); os.Exit(1) })
	}
`,
}

// A bound test that crashes the test binary reads as failed with its own output,
// in each crash form the toolchain reports with no test-level result. A bound
// test the crash kept from starting is not known.
func TestGoSuiteReadsATestThatCrashedTheBinaryAsFailed(t *testing.T) {
	root := suiteRepo(t)
	var checks []Declaration
	for pkg, source := range crashFixtures {
		writeFile(t, root, "crash/"+pkg+"/p_test.go", source+"\n\tfunc TestNever(t *testing.T) {}\n")
		checks = append(checks,
			Declaration{ID: pkg + "-crash", Kind: KindGoTest, Package: "crash/" + pkg, Test: "TestCrash"},
			Declaration{ID: pkg + "-never", Kind: KindGoTest, Package: "crash/" + pkg, Test: "TestNever"})
	}
	if err := Save(root, Manifest{Version: ManifestVersion, Checks: checks}); err != nil {
		t.Fatal(err)
	}
	code, records, err := RunGoSuite(root, GoSuiteOptions{Args: []string{"./crash/..."}})
	if err != nil || code != 1 {
		t.Fatalf("RunGoSuite: exit %d err %v", code, err)
	}
	got := byCheck(records)
	for pkg := range crashFixtures {
		crash, never := got[pkg+"-crash"], got[pkg+"-never"]
		if crash.Outcome != OutcomeFailed || !strings.Contains(crash.Reason, "before the test reported") {
			t.Errorf("%s: crash reads %s (%s); want failed, told apart from an assertion", pkg, crash.Outcome, crash.Reason)
		}
		if !strings.Contains(crash.Excerpt, "crash-marker") || crash.OutputDigest == "" {
			t.Errorf("%s: crash output %q digest %q; want the test's own output", pkg, crash.Excerpt, crash.OutputDigest)
		}
		if never.Outcome != OutcomeUnavailable || !strings.Contains(never.Reason, "without a result") {
			t.Errorf("%s: never-started reads %s (%s); want unavailable", pkg, never.Outcome, never.Reason)
		}
		if never.OutputDigest != "" || never.Excerpt != "" {
			t.Errorf("%s: never-started has output %q", pkg, never.Excerpt)
		}
	}
}

// A test with no result of its own reads from what the stream shows about it and
// its package. The streams follow the shape go test -json emits for each case.
func TestGoSuiteReadsAnUnfinishedTestFromItsPackage(t *testing.T) {
	const pkg, other = "example.com/p", "example.com/other"
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	failedAt, end := t0.Add(time.Minute), t0.Add(time.Hour)
	run := goSuiteEvent{Time: t0, Action: "run", Package: pkg, Test: "TestBound"}
	output := goSuiteEvent{Time: t0, Action: "output", Package: pkg, Test: "TestBound", Output: "last-words\n"}
	pause := goSuiteEvent{Time: t0, Action: "pause", Package: pkg, Test: "TestBound"}
	testPass := goSuiteEvent{Time: t0, Action: "pass", Package: pkg, Test: "TestBound"}
	testFail := goSuiteEvent{Time: t0, Action: "fail", Package: pkg, Test: "TestBound"}
	subRun := goSuiteEvent{Time: t0, Action: "run", Package: pkg, Test: "TestBound/inner"}
	start := goSuiteEvent{Time: t0, Action: "start", Package: pkg}
	fail := goSuiteEvent{Time: failedAt, Action: "fail", Package: pkg}
	pass := goSuiteEvent{Time: failedAt, Action: "pass", Package: pkg}
	buildFail := goSuiteEvent{Time: failedAt, Action: "fail", Package: pkg, FailedBuild: pkg + " [" + pkg + ".test]"}
	otherRun := goSuiteEvent{Time: t0, Action: "run", Package: other, Test: "TestHang"}

	cases := []struct {
		name     string
		events   []goSuiteEvent
		timedOut bool
		outcome  string
		reason   string
		finished time.Time
		output   bool
	}{
		{name: "crash", events: []goSuiteEvent{start, run, output, fail},
			outcome: OutcomeFailed, reason: "before the test reported", finished: failedAt, output: true},
		{name: "crash in a subtest", events: []goSuiteEvent{start, run, subRun, output, fail},
			outcome: OutcomeFailed, reason: "before the test reported", finished: failedAt, output: true},
		{name: "crash in a second iteration", events: []goSuiteEvent{start, run, testPass, run, output, fail},
			outcome: OutcomeFailed, reason: "before the test reported", finished: failedAt, output: true},
		{name: "deadline after a failed iteration", events: []goSuiteEvent{start, run, output, testFail, run}, timedOut: true,
			outcome: OutcomeFailed, finished: t0, output: true},
		{name: "exit while paused at t.Parallel", events: []goSuiteEvent{start, run, pause, output, fail},
			outcome: OutcomeFailed, reason: "before the test reported", finished: failedAt, output: true},
		{name: "crash before a later deadline", events: []goSuiteEvent{start, run, output, fail, otherRun}, timedOut: true,
			outcome: OutcomeFailed, reason: "before the test reported", finished: failedAt, output: true},
		{name: "never started", events: []goSuiteEvent{start, fail},
			outcome: OutcomeUnavailable, reason: "without a result", finished: end},
		{name: "never started before a deadline", events: []goSuiteEvent{start, fail}, timedOut: true,
			outcome: OutcomeNotExecuted, reason: "deadline", finished: end},
		{name: "started in a passing package", events: []goSuiteEvent{start, run, output, pass},
			outcome: OutcomeUnavailable, reason: "without a result", finished: end},
		{name: "build failure", events: []goSuiteEvent{start, buildFail},
			outcome: OutcomeUnavailable, reason: "failed to build", finished: end},
		{name: "deadline stopped a started test", events: []goSuiteEvent{start, run, output}, timedOut: true,
			outcome: OutcomeNotExecuted, reason: "deadline", finished: end},
		{name: "deadline stopped a paused test", events: []goSuiteEvent{start, run, pause}, timedOut: true,
			outcome: OutcomeNotExecuted, reason: "deadline", finished: end},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newGoSuiteReader(&bytes.Buffer{})
			r.bind(pkg, "TestBound", Execution{CheckID: "bound"})
			for _, ev := range c.events {
				line, err := json.Marshal(ev)
				if err != nil {
					t.Fatal(err)
				}
				r.Write(append(line, '\n'))
			}
			records := r.records(end, c.timedOut)
			if len(records) != 1 {
				t.Fatalf("records %+v; want one", records)
			}
			rec := records[0]
			if rec.Outcome != c.outcome || !strings.Contains(rec.Reason, c.reason) {
				t.Errorf("%s (%s); want %s (%s)", rec.Outcome, rec.Reason, c.outcome, c.reason)
			}
			if !rec.Finished.Equal(c.finished) {
				t.Errorf("finished %v; want %v", rec.Finished, c.finished)
			}
			if c.output != (rec.OutputDigest != "") || c.output != strings.Contains(rec.Excerpt, "last-words") {
				t.Errorf("digest %q excerpt %q; want output recorded: %v", rec.OutputDigest, rec.Excerpt, c.output)
			}
		})
	}
}
