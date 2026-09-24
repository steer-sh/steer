package status

import (
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
)

// A run the repository lock turned away carries no result of its own, so it is
// reported beside the run that held the repository and never in place of it.
func TestCheckRunOfKeepsTheHolderAheadOfARefusal(t *testing.T) {
	held := checks.OracleRun{RunID: "held", Source: checks.SourceStop, Harness: "claude-code",
		Outcome: checks.OutcomePassed, Disposition: "allow", Finished: time.Unix(1700, 0)}
	refused := checks.OracleRun{RunID: "refused", Source: checks.SourceRun,
		Outcome: checks.OutcomeNotExecuted, ConcurrentWith: "held", Finished: time.Unix(1800, 0)}

	run := CheckRunOf(&held, &refused)
	if run.RunID != "held" || run.Outcome != checks.OutcomePassed {
		t.Fatalf("run %+v; want the holder's own result", run)
	}
	if run.Finished != 1700 || run.RefusedAfter != 1800 {
		t.Fatalf("run %+v; want the refusal recorded beside the holder", run)
	}
	if phrase := CheckRunPhrase(run); !strings.Contains(phrase, checks.OutcomePassed) ||
		!strings.Contains(phrase, "turned away") {
		t.Fatalf("phrase %q; want the holder's result and the refusal after it", phrase)
	}

	only := CheckRunOf(nil, &refused)
	if only.RunID != "refused" || only.RefusedAfter != 0 {
		t.Fatalf("run %+v; want the refusal itself when no run on record executed", only)
	}
	if CheckRunOf(nil, nil) != nil {
		t.Fatal("an empty history produced a run")
	}
}

// Registration, this machine's authorization and an observed execution are three
// readings. A repository that declares checks nothing validated reports them as
// never replayed with no execution, which is not a pass.
func TestCollectChecksReadsRegistrationAdoptionAndExecutionApart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, _, err := checks.Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, err := checks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Checks = append(m.Checks, checks.Declaration{ID: "no-count", Kind: checks.KindScript,
		Script: ".steer/checks/no-count.sh"})
	if err := checks.Save(root, m); err != nil {
		t.Fatal(err)
	}

	c := collectChecks(root)
	if !c.Manifest || c.Adopted || c.Armed() {
		t.Fatalf("tier %+v; want a manifest with no authorization here", c)
	}
	if c.LatestRun != nil {
		t.Fatalf("latest run %+v; want none recorded", c.LatestRun)
	}
	if len(c.Runner) != 4 {
		t.Fatalf("runner rows %+v; want both harnesses in both scopes", c.Runner)
	}
	for _, a := range c.Runner {
		if a.Registered || a.Resolvable != ResolveNotRegistered || a.ObservedCorpus != checkCorpus {
			t.Errorf("runner row %+v; want an unregistered row naming its own stream", a)
		}
		if a.Harness == "codex" && a.Trust != TrustUnknown {
			t.Errorf("codex trust %q; Steer reads no Stop trust record", a.Trust)
		}
	}
	if len(c.Checks) != 1 {
		t.Fatalf("checks %+v; want the one declaration", c.Checks)
	}
	row := c.Checks[0]
	if row.Validation != ValidationNone || row.Execution != ExecutionUnobserved || row.Passed() {
		t.Fatalf("row %+v; want a check never tested and never observed", row)
	}
	if phrase := CheckPhrase(row); !strings.Contains(phrase, "never tested") ||
		!strings.Contains(phrase, "no execution recorded") {
		t.Fatalf("phrase %q; want both readings named", phrase)
	}
	if phrase := CheckAdoptionPhrase(c); !strings.Contains(phrase, "steer check adopt") {
		t.Fatalf("phrase %q; want the command that authorizes this repository", phrase)
	}
}

// Outside a repository there is nothing to adopt and nothing declared, and a
// registered runner is still machine state worth reading back.
func TestCollectChecksOutsideARepositoryStillReadsTheRunner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := collectChecks("")
	if c.Repo != "" || c.Manifest || len(c.Checks) != 0 {
		t.Fatalf("tier %+v; want nothing declared", c)
	}
	if len(c.Runner) != 4 {
		t.Fatalf("runner rows %+v; want the registration read anyway", c.Runner)
	}
	if phrase := CheckAdoptionPhrase(c); !strings.Contains(phrase, "not inside a repository") {
		t.Fatalf("phrase %q; want the reader told there is no repository here", phrase)
	}
}
