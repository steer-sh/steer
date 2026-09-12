package checks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const noCountScript = `#!/usr/bin/env bash
set -euo pipefail
if grep -rnE 'echo .*[0-9]+ tests' src; then
  echo "literal test count printed" >&2
  exit 1
fi
`

func exitCode(n int) *int { return &n }

// checkRepo makes a repository holding an initialized store and one script,
// and points the evidence root at a temporary home.
func checkRepo(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("replay needs bash")
	}
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeFile(t, root, ".steer/checks/no-count.sh", script)
	return root
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCases(t *testing.T, root, id string, cases ...Case) {
	t.Helper()
	data, err := json.Marshal(CaseFile{Version: CasesVersion, Cases: cases})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".steer/checks/cases/"+id+".json", string(data))
}

func goodCase() Case {
	return Case{Name: "clean", ExitCode: exitCode(0), Provenance: "planted",
		Files: map[string]string{"src/run.sh": "echo \"tests passed\"\n"}}
}

func defectCase(diagnostic string) Case {
	return Case{Name: "literal-count", ExitCode: exitCode(1), Diagnostic: diagnostic, Provenance: "planted",
		Files: map[string]string{"src/run.sh": "echo \"42 tests passed\"\n"}}
}

var noCount = Declaration{ID: "no-count", Kind: KindScript, Script: ".steer/checks/no-count.sh"}

// A check is declared only after it passes its known-good case and fails its
// defect case with the named diagnostic; either way the run is recorded.
func TestAddDeclaresACheckOnlyAfterItsCasesValidate(t *testing.T) {
	root := checkRepo(t, noCountScript)
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	run, err := Add(root, noCount)
	if err != nil {
		t.Fatalf("Add: %v (run %+v)", err, run)
	}
	if run.Verdict != VerdictValidated || len(run.Cases) != 2 {
		t.Fatalf("verdict %s with %d cases; want validated with 2", run.Verdict, len(run.Cases))
	}
	m, _ := Load(root)
	if len(m.Checks) != 1 || m.Checks[0].ID != "no-count" {
		t.Fatalf("manifest %+v; want no-count declared", m.Checks)
	}
	history, err := History(root)
	if err != nil || len(history) != 1 || history[0].Verdict != VerdictValidated {
		t.Fatalf("history %+v err %v; want one validated run", history, err)
	}
}

// A failure that prints something other than the named diagnostic does not
// validate the check, and the check stays undeclared.
func TestAFailureWithTheWrongDiagnosticDoesNotValidate(t *testing.T) {
	root := checkRepo(t, noCountScript)
	writeCases(t, root, "no-count", goodCase(), defectCase("stale number"))
	run, err := Add(root, noCount)
	if !errors.Is(err, ErrNotValidated) {
		t.Fatalf("Add err = %v; want ErrNotValidated", err)
	}
	if run.Verdict != VerdictFailed {
		t.Fatalf("verdict %s; want failed", run.Verdict)
	}
	var defect CaseResult
	for _, c := range run.Cases {
		if c.Name == "literal-count" {
			defect = c
		}
	}
	if defect.Outcome != OutcomeMismatched || !strings.Contains(defect.Reason, "stale number") {
		t.Fatalf("defect case %+v; want a mismatch naming the missing diagnostic", defect)
	}
	if m, _ := Load(root); len(m.Checks) != 0 {
		t.Fatalf("a failed check was declared: %+v", m.Checks)
	}
	if history, _ := History(root); len(history) != 1 || history[0].Verdict != VerdictFailed {
		t.Fatalf("history %+v; want the failed run recorded", history)
	}
}

// A missing command or a failed setup cannot stand in for the defect the check
// exists to find: the case is invalid and the run is unavailable.
func TestAnUnrunnableCommandOrSetupIsUnavailableNotAFailure(t *testing.T) {
	root := checkRepo(t, "#!/usr/bin/env bash\nsteer-no-such-tool src\n")
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	run, err := Add(root, noCount)
	if !errors.Is(err, ErrNotValidated) || run.Verdict != VerdictUnavailable {
		t.Fatalf("missing command: verdict %s err %v; want unavailable", run.Verdict, err)
	}
	for _, c := range run.Cases {
		if c.Outcome != OutcomeInvalid || !strings.Contains(c.Reason, "127") {
			t.Errorf("case %+v; want invalid with exit 127", c)
		}
	}

	root = checkRepo(t, noCountScript)
	bad := defectCase("literal test count")
	bad.Setup = "false"
	writeCases(t, root, "no-count", goodCase(), bad)
	run, _ = Add(root, noCount)
	if run.Verdict != VerdictUnavailable {
		t.Fatalf("failed setup: verdict %s; want unavailable", run.Verdict)
	}
	for _, c := range run.Cases {
		if c.Name == "literal-count" && (c.Outcome != OutcomeInvalid || !strings.Contains(c.Reason, "setup exited 1")) {
			t.Errorf("setup case %+v; want invalid naming the setup exit", c)
		}
	}
}

// A case that outlives the timeout is stopped with its children and reported
// invalid.
func TestACaseThatHangsIsStoppedAtTheTimeout(t *testing.T) {
	root := checkRepo(t, "#!/usr/bin/env bash\nsleep 30 &\nsleep 30\n")
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	old := CaseTimeout
	CaseTimeout = 500 * time.Millisecond
	t.Cleanup(func() { CaseTimeout = old })

	start := time.Now()
	run, _ := Add(root, noCount)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("replay took %s; the timeout did not stop the case", elapsed)
	}
	if run.Verdict != VerdictUnavailable {
		t.Fatalf("verdict %s; want unavailable", run.Verdict)
	}
	for _, c := range run.Cases {
		if c.Outcome != OutcomeInvalid || !strings.Contains(c.Reason, "exceeded") {
			t.Errorf("case %+v; want invalid for the timeout", c)
		}
	}
}

// A case runs in a copy: what the check writes never reaches the repository,
// and a case cannot replace the detector it is testing.
func TestReplayRunsInACopyThatCannotReplaceTheDetector(t *testing.T) {
	root := checkRepo(t, "#!/usr/bin/env bash\ntouch touched-by-check\n"+strings.TrimPrefix(noCountScript, "#!/usr/bin/env bash\n"))
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	if _, err := Add(root, noCount); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "touched-by-check")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the check wrote into the repository")
	}

	hijack := goodCase()
	hijack.Files[".steer/checks/no-count.sh"] = "exit 0\n"
	writeCases(t, root, "no-count", hijack, defectCase("literal test count"))
	run, _ := Test(root, "no-count")
	if run.Verdict != VerdictUnavailable || run.Cases[0].Outcome != OutcomeInvalid {
		t.Fatalf("a case overwriting the detector gave %s / %+v; want invalid", run.Verdict, run.Cases[0])
	}
}

// A receipt goes stale when the detector or the cases change, and a later
// failed run does not erase the receipt it follows.
func TestAReceiptGoesStaleWhenTheDetectorOrCasesChange(t *testing.T) {
	root := checkRepo(t, noCountScript)
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	if _, err := Add(root, noCount); err != nil {
		t.Fatalf("Add: %v", err)
	}
	fresh := func() Freshness {
		history, err := History(root)
		if err != nil {
			t.Fatal(err)
		}
		return FreshnessOf(root, noCount, history)
	}
	if f := fresh(); f.State != FreshCurrent || len(f.Changed) != 0 {
		t.Fatalf("after Add: %+v; want current", f)
	}

	writeFile(t, root, ".steer/checks/no-count.sh", noCountScript+"# edited\n")
	if f := fresh(); f.State != FreshStale || strings.Join(f.Changed, ",") != "detector" {
		t.Fatalf("after a detector edit: %+v; want stale on the detector", f)
	}
	writeFile(t, root, ".steer/checks/no-count.sh", noCountScript)

	writeCases(t, root, "no-count", goodCase(), defectCase("literal test"))
	if f := fresh(); f.State != FreshStale || strings.Join(f.Changed, ",") != "cases" {
		t.Fatalf("after a case edit: %+v; want stale on the cases", f)
	}

	writeCases(t, root, "no-count", goodCase(), defectCase("not printed"))
	if run, _ := Test(root, "no-count"); run.Verdict != VerdictFailed {
		t.Fatalf("replay verdict %s; want failed", run.Verdict)
	}
	f := fresh()
	if f.Receipt == nil || f.Receipt.Verdict != VerdictValidated || f.Latest == nil || f.Latest.Verdict != VerdictFailed {
		t.Fatalf("after a failed replay: %+v; want the old receipt kept and the failure as latest", f)
	}
}

// Removing a check drops its declaration and keeps its cases and history.
func TestRemoveKeepsCasesAndHistory(t *testing.T) {
	root := checkRepo(t, noCountScript)
	writeCases(t, root, "no-count", goodCase(), defectCase("literal test count"))
	if _, err := Add(root, noCount); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := Remove(root, "no-count"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if m, _ := Load(root); len(m.Checks) != 0 {
		t.Fatalf("manifest still declares %+v", m.Checks)
	}
	if _, err := os.Stat(CasesPath(root, "no-count")); err != nil {
		t.Fatalf("cases removed: %v", err)
	}
	if history, _ := History(root); len(history) != 1 {
		t.Fatalf("history %+v; want the run kept", history)
	}
	if _, err := Remove(root, "no-count"); err == nil {
		t.Fatalf("removing an undeclared check succeeded")
	}
}

// A case file needs a known-good and a defect case, a diagnostic on each
// defect case, provenance, and exit codes a check can mean.
func TestACaseFileNeedsAKnownGoodAndADefectCase(t *testing.T) {
	good, defect := goodCase(), defectCase("literal")
	if err := (CaseFile{Version: CasesVersion, Cases: []Case{good, defect}}).Validate(); err != nil {
		t.Fatalf("a valid case file was refused: %v", err)
	}
	noDiag := defectCase("")
	noProv := goodCase()
	noProv.Provenance = ""
	unrunnable := defectCase("literal")
	unrunnable.ExitCode = exitCode(127)
	noExit := goodCase()
	noExit.ExitCode = nil
	escaping := goodCase()
	escaping.Files = map[string]string{"../outside": "x"}
	for name, cases := range map[string][]Case{
		"no defect case":      {good},
		"no known-good case":  {defect},
		"no diagnostic":       {good, noDiag},
		"no provenance":       {noProv, defect},
		"exit 127 expected":   {good, unrunnable},
		"exit code missing":   {noExit, defect},
		"escaping file":       {escaping, defect},
		"duplicate case name": {good, good, defect},
	} {
		if err := (CaseFile{Version: CasesVersion, Cases: cases}).Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := (CaseFile{Version: 2, Cases: []Case{good, defect}}).Validate(); err == nil {
		t.Errorf("version 2: accepted")
	}
}
