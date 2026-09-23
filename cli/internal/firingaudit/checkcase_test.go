package firingaudit

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
)

func minute(m int) time.Time { return time.Date(2026, 9, 23, 3, m, 0, 0, time.UTC) }

func blockRun(repo, id string, m int) checks.OracleRun {
	return checks.OracleRun{Repo: repo, RunID: id, Source: checks.SourceStop, Harness: "codex",
		Session: "s", Outcome: checks.OutcomeFailed, Disposition: checks.DispositionBlock,
		Started: minute(m), Finished: minute(m).Add(time.Second)}
}

func record(repo, run, check, outcome string) checks.Execution {
	return checks.Execution{Repo: repo, RunID: run, CheckID: check, Outcome: outcome}
}

func kinds(failures []CheckFailure) []string {
	var out []string
	for _, f := range failures {
		name := f.Kind
		if f.Check != "" {
			name += ":" + f.Check
		}
		if f.Required {
			name += "!"
		}
		out = append(out, name)
	}
	return out
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Each failing check is a failure of its own; a check the run never reached or
// that skipped itself asserted nothing. check.sh's own failure must be judged
// only when no declared check accounts for it.
func TestCheckFailuresReadsEachFailureTheRunReported(t *testing.T) {
	run := blockRun("/r", "b", 1)
	mixed := []checks.Execution{
		record("/r", "b", "go", checks.OutcomeFailed),
		record("/r", "b", "lint", checks.OutcomeUnavailable),
		record("/r", "b", "fmt", checks.OutcomePassed),
		record("/r", "b", "slow", checks.OutcomeSkipped),
		record("/r", "b", "revert-test", checks.OutcomeNotExecuted),
		record("/r", "other", "vet", checks.OutcomeFailed),
	}
	want := []string{"check:go!", "check:lint!", "oracle"}
	if got := kinds(CheckFailures(run, mixed)); !sameList(got, want) {
		t.Errorf("mixed run failures = %v; want %v", got, want)
	}

	unmanaged := kinds(CheckFailures(run, nil))
	if !sameList(unmanaged, []string{"oracle!"}) {
		t.Errorf("unmanaged run failures = %v; want the oracle's, required", unmanaged)
	}

	integration := blockRun("/r", "i", 2)
	integration.Outcome, integration.IntegrationFailure = checks.OutcomePassed, true
	got := kinds(CheckFailures(integration, []checks.Execution{record("/r", "i", "go", checks.OutcomeNotExecuted)}))
	if !sameList(got, []string{"integration!"}) {
		t.Errorf("integration run failures = %v; want only the integration failure", got)
	}
}

// Cases come from every repository's live records, each with its own records
// and follow-up, oldest block first.
func TestCheckCasesAssembleBlocksAcrossRepositories(t *testing.T) {
	later := blockRun("/a", "a1", 5)
	earlier := blockRun("/b", "b1", 2)
	allow := blockRun("/a", "a2", 7)
	allow.Disposition, allow.Outcome = checks.DispositionAllow, checks.OutcomePassed
	ev := checks.LiveEvidence{
		Runs: []checks.OracleRun{later, allow, earlier},
		Executions: []checks.Execution{
			record("/a", "a1", "go", checks.OutcomeFailed),
			record("/b", "b1", "go", checks.OutcomeTimeout),
			record("/a", "a2", "go", checks.OutcomePassed),
		},
	}
	cases := CheckCases(ev)
	if len(cases) != 2 || cases[0].Run.RunID != "b1" || cases[1].Run.RunID != "a1" {
		t.Fatalf("cases = %+v; want the two blocks, b1 first", cases)
	}
	a := cases[1]
	if len(a.Executions) != 1 || a.Executions[0].Repo != "/a" {
		t.Errorf("a1 executions = %+v; want only its own record", a.Executions)
	}
	if a.Followup.NextRun == nil || a.Followup.NextRun.RunID != "a2" {
		t.Errorf("a1 next run = %+v; want a2", a.Followup.NextRun)
	}
	if f := a.Failures; len(f) != 2 || f[0].Execution == nil || f[0].Execution.Outcome != checks.OutcomeFailed ||
		f[1].Kind != adjudication.FailureOracle {
		t.Errorf("a1 failures = %v; want go with its record, then the optional oracle", kinds(f))
	}
	if _, ok := FindCheckCase(cases, "b1"); !ok {
		t.Error("the b1 case was not found by its run")
	}
	if _, ok := FindCheckCase(cases, "a2"); ok {
		t.Error("a run that allowed its turn was found as a case")
	}
}

// Each failure is classified against its own repository's detectors, read once
// per repository.
func TestClassifyCasesReadsEachRepositoryOnce(t *testing.T) {
	a := blockRun("/a", "a1", 1)
	b := blockRun("/b", "b1", 2)
	withDigest := func(repo, run, digest string) checks.Execution {
		e := record(repo, run, "go", checks.OutcomeFailed)
		e.DetectorDigest = digest
		return e
	}
	cases := CheckCases(checks.LiveEvidence{
		Runs:       []checks.OracleRun{a, b},
		Executions: []checks.Execution{withDigest("/a", "a1", "d1"), withDigest("/b", "b1", "d1")},
	})
	reads := map[string]int{}
	ClassifyCases(cases, func(repo string) map[string]string {
		reads[repo]++
		if repo == "/a" {
			return map[string]string{"go": "d1"}
		}
		return map[string]string{"go": "d2"}
	})
	got := map[string][]string{}
	for _, c := range cases {
		for _, f := range c.Failures {
			got[c.Run.Repo] = append(got[c.Run.Repo], f.Applies)
		}
	}
	if !sameList(got["/a"], []string{adjudication.Current, adjudication.Unmanaged}) ||
		!sameList(got["/b"], []string{adjudication.Historic, adjudication.Unmanaged}) {
		t.Errorf("applies = %v; want a current and b historic, check.sh unmanaged in both", got)
	}
	if reads["/a"] != 1 || reads["/b"] != 1 {
		t.Errorf("reads = %v; want one per repository", reads)
	}
}

// A repository on this machine with no manifest declares nothing; one that is
// not on this machine resolves to nothing, which reads as unavailable.
func TestInstalledDetectorsResolvesAMissingRepositoryToNothing(t *testing.T) {
	resolve := InstalledDetectors()
	if got := resolve(t.TempDir()); got == nil || len(got) != 0 {
		t.Errorf("a repository without checks = %v; want an empty map", got)
	}
	if got := resolve(filepath.Join(t.TempDir(), "gone")); got != nil {
		t.Errorf("a missing repository = %v; want nil", got)
	}
}

func TestCoverChecksCountsEachRepositorysBlocks(t *testing.T) {
	cases := CheckCases(checks.LiveEvidence{Runs: []checks.OracleRun{
		blockRun("/a", "a1", 1), blockRun("/b", "b1", 2), blockRun("/a", "a2", 3)}})
	receipts := []adjudication.Receipt{
		{Subject: adjudication.SubjectCheck, RunID: "a2"},
		{Subject: adjudication.SubjectGuard, FiringID: "a1"},
	}
	got := CoverChecks(cases, receipts)
	want := []CheckCoverage{{Repo: "/a", Blocks: 2, Judged: 1, Unjudged: 1}, {Repo: "/b", Blocks: 1, Unjudged: 1}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("coverage = %+v; want %+v (a guard receipt judges no block)", got, want)
	}
}
