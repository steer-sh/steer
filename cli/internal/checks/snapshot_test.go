package checks

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitAll(t *testing.T, root, message string) string {
	t.Helper()
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "--no-verify", "-m", message)
	return gitIn(t, root, "rev-parse", "HEAD")
}

// patchOf returns the diff that turns rel into content, leaving rel unchanged.
func patchOf(t *testing.T, root, rel, content string) string {
	t.Helper()
	old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, rel, content)
	patch := gitIn(t, root, "diff", "--", rel) + "\n"
	writeFile(t, root, rel, string(old))
	return patch
}

// snapshotRepo makes a git repository whose first commit holds the fixed
// src/run.sh and an older detector that prints "old detector", and whose
// second commit holds the current detector. It returns the root and the first
// commit.
func snapshotRepo(t *testing.T) (string, string) {
	t.Helper()
	root := checkRepo(t, strings.Replace(noCountScript, "literal test count printed", "old detector", 1))
	gitIn(t, root, "init", "-q")
	writeFile(t, root, "src/run.sh", "echo \"tests passed\"\n")
	fixed := commitAll(t, root, "fixed")
	writeFile(t, root, ".steer/checks/no-count.sh", noCountScript)
	commitAll(t, root, "current detector")
	return root, fixed
}

func snapshotCase(name string, exit int, diagnostic, snapshot, patch string) Case {
	return Case{Name: name, ExitCode: exitCode(exit), Diagnostic: diagnostic, Provenance: "planted",
		Snapshot: snapshot, Patch: patch}
}

const literalPatch = ".steer/checks/cases/no-count/literal-count.patch"

func resultNamed(t *testing.T, run ValidationRun, name string) CaseResult {
	t.Helper()
	for _, c := range run.Cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("run %+v has no case %s", run, name)
	return CaseResult{}
}

// A snapshot case runs the current detector on the recorded commit, and its
// defect case on that commit with the patch applied. The commit is pinned by a
// ref, and the repository's work tree and index are left as they were.
func TestASnapshotCaseRunsTheCurrentDetectorOnTheRecordedCommit(t *testing.T) {
	root, fixed := snapshotRepo(t)
	writeFile(t, root, literalPatch, patchOf(t, root, "src/run.sh", "echo \"42 tests passed\"\n"))
	writeCases(t, root, "no-count",
		snapshotCase("fixed", 0, "", fixed, ""),
		snapshotCase("literal-count", 1, "literal test count", fixed, literalPatch))
	commitAll(t, root, "cases")
	writeFile(t, root, "src/run.sh", "echo \"tests passed at head\"\n")

	run, err := Add(root, noCount)
	if err != nil {
		t.Fatalf("Add: %v (cases %+v)", err, run.Cases)
	}
	for _, c := range run.Cases {
		if c.Outcome != OutcomeMatched || c.Snapshot != fixed {
			t.Errorf("case %+v; want matched at snapshot %s", c, fixed)
		}
	}
	if got := gitIn(t, root, "rev-parse", SnapshotRefPrefix+fixed); got != fixed {
		t.Errorf("snapshot ref points at %q; want %s", got, fixed)
	}
	if staged := gitIn(t, root, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("replay staged %s", staged)
	}
	if changed := gitIn(t, root, "diff", "--name-only"); changed != ".steer/checks/manifest.json\nsrc/run.sh" {
		t.Errorf("work tree changes after replay:\n%s\nwant only the manifest and the edit made before it", changed)
	}
	if untracked := gitIn(t, root, "ls-files", "--others"); untracked != "" {
		t.Errorf("replay left untracked files: %s", untracked)
	}

	writeFile(t, root, literalPatch, patchOf(t, root, "src/run.sh", "echo \"43 tests passed\"\n"))
	history, _ := History(root)
	if f := FreshnessOf(root, noCount, history); f.State != FreshStale || strings.Join(f.Changed, ",") != "cases" {
		t.Fatalf("after a patch edit: %+v; want stale on the cases", f)
	}
}

// A patch that no longer applies and a commit the repository does not hold
// make the case invalid with their cause. Only a held commit is pinned.
func TestAnUnappliablePatchOrMissingCommitIsNotAnExpectedFailure(t *testing.T) {
	root, fixed := snapshotRepo(t)
	writeFile(t, root, literalPatch, "--- a/src/run.sh\n+++ b/src/run.sh\n@@ -1 +1 @@\n-echo \"no such line\"\n+echo \"42 tests passed\"\n")
	writeCases(t, root, "no-count",
		snapshotCase("fixed", 0, "", fixed, ""),
		snapshotCase("literal-count", 1, "literal test count", fixed, literalPatch))
	run, err := Add(root, noCount)
	if !errors.Is(err, ErrNotValidated) || run.Verdict != VerdictUnavailable {
		t.Fatalf("conflicting patch: verdict %s err %v; want unavailable", run.Verdict, err)
	}
	if c := resultNamed(t, run, "literal-count"); c.Outcome != OutcomeInvalid || c.Cause != CauseIncompatible {
		t.Errorf("conflicting patch gave %+v; want invalid as an incompatible case", c)
	}

	missing := strings.Repeat("0", 40)
	writeCases(t, root, "no-count",
		snapshotCase("fixed", 0, "", missing, ""),
		snapshotCase("literal-count", 1, "literal test count", missing, ""))
	run, _ = Add(root, noCount)
	for _, c := range run.Cases {
		if c.Outcome != OutcomeInvalid || c.Cause != CauseSnapshotUnavailable {
			t.Errorf("missing commit gave %+v; want invalid with the snapshot unavailable", c)
		}
	}
	if refs := gitIn(t, root, "for-each-ref", "--format=%(refname)", SnapshotRefPrefix); refs != SnapshotRefPrefix+fixed {
		t.Errorf("snapshot refs %q; want only the held commit %s", refs, fixed)
	}
}

// A patch may change production but not the detector it is testing.
func TestASnapshotPatchCannotChangeTheDetector(t *testing.T) {
	root, fixed := snapshotRepo(t)
	writeFile(t, root, literalPatch, patchOf(t, root, ".steer/checks/no-count.sh", "exit 1\n"))
	writeCases(t, root, "no-count",
		snapshotCase("fixed", 0, "", fixed, ""),
		snapshotCase("literal-count", 1, "literal test count", fixed, literalPatch))
	run, _ := Add(root, noCount)
	c := resultNamed(t, run, "literal-count")
	if c.Outcome != OutcomeInvalid || !strings.Contains(c.Reason, "changes detector file .steer/checks/no-count.sh") {
		t.Fatalf("a patch editing the detector gave %+v; want invalid naming the detector file", c)
	}
}

// A snapshot case names a full commit and changes its tree only by patch; a
// patch needs a snapshot and a path inside the repository.
func TestASnapshotCaseNamesAFullCommitAndChangesItOnlyByPatch(t *testing.T) {
	sha := strings.Repeat("a", 40)
	good := snapshotCase("fixed", 0, "", sha, "")
	valid := []Case{good, snapshotCase("defect", 1, "literal", sha, "p.patch")}
	if err := (CaseFile{Version: CasesVersion, Cases: valid}).Validate(); err != nil {
		t.Fatalf("a valid snapshot case file was refused: %v", err)
	}
	short := snapshotCase("defect", 1, "literal", sha[:12], "")
	withFiles := snapshotCase("defect", 1, "literal", sha, "")
	withFiles.Files = map[string]string{"src/run.sh": "x"}
	noSnapshot := snapshotCase("defect", 1, "literal", "", "p.patch")
	escaping := snapshotCase("defect", 1, "literal", sha, "../p.patch")
	for name, c := range map[string]Case{
		"short hash":           short,
		"files and snapshot":   withFiles,
		"patch with no commit": noSnapshot,
		"escaping patch":       escaping,
	} {
		if err := (CaseFile{Version: CasesVersion, Cases: []Case{good, c}}).Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A Go snapshot compiles only the tracked declarations against the recorded
// commit, so a current unrelated test that calls production the commit lacks
// does not break it; a patch that stops the package building is an
// incompatible case.
func TestAGoSnapshotReplaysTheTrackedDeclarationsOnTheRecordedCommit(t *testing.T) {
	root := goCheckRepo(t, readTestdata(t, "resp_test.go.txt"))
	if _, _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	gitIn(t, root, "init", "-q")
	fixed := commitAll(t, root, "fixed")
	omitsID := ".steer/checks/cases/resp-fields/omits-id.patch"
	breaks := ".steer/checks/cases/resp-fields/breaks.patch"
	writeFile(t, root, omitsID, patchOf(t, root, "svc/resp/resp.go", strings.Replace(respProduction, `"id"`, `"name"`, 1)))
	writeFile(t, root, breaks, patchOf(t, root, "svc/resp/resp.go", strings.Replace(respProduction, "return 2", `return "2"`, 1)))
	writeFile(t, root, "svc/resp/resp.go", respProduction+"\nfunc Added() int { return 3 }\n")
	writeFile(t, root, "svc/resp/added_test.go", "package resp\n\nimport \"testing\"\n\nfunc TestAdded(t *testing.T) { _ = Added() }\n")

	writeCases(t, root, respCheck.ID,
		snapshotCase("fixed", 0, "", fixed, ""),
		snapshotCase("omits-id", 1, "missing field id", fixed, omitsID))
	run, err := Add(root, respCheck)
	if err != nil {
		t.Fatalf("Add: %v (cases %+v)", err, run.Cases)
	}

	writeCases(t, root, respCheck.ID,
		snapshotCase("fixed", 0, "", fixed, ""),
		snapshotCase("breaks", 1, "missing field id", fixed, breaks))
	run, _ = Test(root, respCheck.ID)
	if c := resultNamed(t, run, "breaks"); c.Outcome != OutcomeInvalid || c.Cause != CauseIncompatible {
		t.Fatalf("a patch that stops the build gave %+v; want invalid as an incompatible case", c)
	}
}

// A Go case whose module the offline replay cannot load is an environment
// failure, not a case to author again.
func TestAGoCaseMissingAModuleIsAnEnvironmentFailure(t *testing.T) {
	root := goCheckRepo(t, readTestdata(t, "resp_test.go.txt"))
	if _, _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	hash := "h1:" + strings.Repeat("A", 43) + "="
	needsModule := respCase("fixed", 0, "", "package resp\n\nimport _ \"example.com/nope\"\n\nfunc Response() string { return `{\"id\": 1}` }\n")
	needsModule.Files["svc/go.mod"] = respGoMod + "\nrequire example.com/nope v1.0.0\n"
	needsModule.Files["svc/go.sum"] = "example.com/nope v1.0.0 " + hash + "\nexample.com/nope v1.0.0/go.mod " + hash + "\n"
	writeCases(t, root, respCheck.ID, needsModule,
		respCase("omits-id", 1, "missing field id", "package resp\n\nfunc Response() string { return `{}` }\n"))
	run, _ := Add(root, respCheck)
	if c := resultNamed(t, run, "fixed"); c.Outcome != OutcomeInvalid || c.Cause != CauseEnvironment {
		t.Fatalf("a missing module gave %+v; want invalid with the environment unavailable", c)
	}
}
