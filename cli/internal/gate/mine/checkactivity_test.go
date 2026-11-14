package mine

import "testing"

// boundCheckRecord is a scored chain record carrying boundFacts' check binding.
func boundCheckRecord(ts int) Record {
	r := obsRecord("check", "", ts)
	for k, v := range boundFacts() {
		r[k] = v
	}
	return r
}

// A chain record counts its check's executions inside the window's bounds, by
// outcome, and names the ones that blocked a turn or ran an edited detector.
func TestChainCheckActivityCountsTheBoundChecksWindowExecutions(t *testing.T) {
	chains := []Chain{obsChain(t, 1000, "agy/1/aaa/v1", "widget"), obsChain(t, 3000, "agy/1/aaa/v1", "widget")}
	runs := []CheckRun{
		{Repo: boundRoot, CheckID: "no-count", Outcome: "passed", DetectorDigest: "deadbeef", TS: 2500},
		{Repo: boundRoot, CheckID: "no-count", Outcome: "failed", DetectorDigest: "deadbeef", TS: 2600, Blocked: true},
		{Repo: boundRoot, CheckID: "no-count", Outcome: "passed", DetectorDigest: "cafef00d", TS: 3100},
		{Repo: boundRoot, CheckID: "no-count", Outcome: "not-executed", TS: 3200},
		// Before the install, another check, and another repository.
		{Repo: boundRoot, CheckID: "no-count", Outcome: "failed", TS: 1500},
		{Repo: boundRoot, CheckID: "other", Outcome: "failed", TS: 3000},
		{Repo: "/w/elsewhere", CheckID: "no-count", Outcome: "failed", TS: 3000},
	}
	var obs Observation
	withClock(9000, func() {
		scored, err := ScoreRecord(boundCheckRecord(2000), EvidenceSet{
			Chains: chains, CheckRuns: runs, CheckRepos: map[string]bool{boundRoot: true}})
		if err != nil {
			t.Fatal(err)
		}
		obs = observationOf(t, scored)
	})
	c := obs.Checks
	check(t, "a check-tier record has check activity", c.Applicable, true)
	check(t, "joined by its check id", c.Attribution, AttributionCheckID)
	check(t, "counted over the window period", c.Scope, FiringScopeWindowPeriod)
	check(t, "never claimed as class-scoped", c.ClassScoped, false)
	check(t, "four executions of this check in this repository after the install", c.Executions, 4)
	check(t, "by outcome", c.Outcomes, map[string]int{"passed": 2, "failed": 1, "not-executed": 1})
	check(t, "one blocked a turn", c.Blocked, 1)
	check(t, "one ran a detector other than the registered one", c.ChangedDetector, 1)
	check(t, "the newest execution", c.NewestTS, float64(3200))
	check(t, "and a zero would be a fact", c.Known(), true)
	check(t, "so no coverage is missing", MissingCoverage(obs), false)
}

// A session record counts only executions whose Stop run carried an eligible
// session on the same harness.
func TestSessionCheckActivityJoinsStopRunsToEligibleSessions(t *testing.T) {
	units := append(window(1000, 1, "claude-code", "proj", "bare python here"),
		window(3000, 2, "claude-code", "proj", "bare python here")...)
	r := sessionRecord(t, "bare python", "check", "", 2000)
	for k, v := range boundFacts() {
		r[k] = v
	}
	runs := []CheckRun{
		{Repo: boundRoot, CheckID: "no-count", Harness: "claude-code", SessionID: "s3000", Outcome: "failed", TS: 3000, Blocked: true},
		{Repo: boundRoot, CheckID: "no-count", Harness: "claude-code", SessionID: "s3000", Outcome: "passed", TS: 3002},
		// A manual run carries no session; a codex session id that names a claude
		// session; a session before the install.
		{Repo: boundRoot, CheckID: "no-count", Outcome: "passed", TS: 3003},
		{Repo: boundRoot, CheckID: "no-count", Harness: "codex", SessionID: "s3001", Outcome: "passed", TS: 3004},
		{Repo: boundRoot, CheckID: "no-count", Harness: "claude-code", SessionID: "s1000", Outcome: "failed", TS: 1000},
	}
	var obs Observation
	withClock(9000, func() {
		obs = observationOf(t, scoreSession(t, r, EvidenceSet{
			Sessions: units, CheckRuns: runs, CheckRepos: map[string]bool{boundRoot: true}}))
	})
	c := obs.Checks
	check(t, "the join is class-scoped", c.ClassScoped, true)
	check(t, "over the eligible units", c.Scope, FiringScopeEligibleUnits)
	check(t, "two executions landed in eligible sessions", c.Executions, 2)
	check(t, "in one session", c.Units, 1)
	check(t, "one of them blocked a turn", c.Blocked, 1)
}

// A bound check whose repository has no check evidence on this machine is unknown
// activity and missing coverage; an unbound record is neither.
func TestAMissingCheckStreamIsUnknownAndAnUnboundRecordIsUnavailable(t *testing.T) {
	t.Setenv("COMMIT_REVIEW_HOST", "host-a")
	chains := []Chain{obsChain(t, 3000, "agy/1/aaa/v1", "widget")}
	withClock(9000, func() {
		scored, err := ScoreRecord(boundCheckRecord(2000), EvidenceSet{Chains: chains})
		if err != nil {
			t.Fatal(err)
		}
		obs := observationOf(t, scored)
		check(t, "no evidence for the repository here", obs.Checks.StreamPresent, false)
		check(t, "so the zero says nothing about the check", obs.Checks.Known(), false)
		check(t, "and coverage is missing", MissingCoverage(obs), true)

		scored, err = ScoreRecord(obsRecord("check", "", 2000), EvidenceSet{Chains: chains})
		if err != nil {
			t.Fatal(err)
		}
		obs = observationOf(t, scored)
		check(t, "a legacy record is still a check-tier record", obs.Checks.Applicable, true)
		check(t, "whose attribution is unavailable", obs.Checks.Attribution, AttributionUnavailable)
		check(t, "which no repair can supply", MissingCoverage(obs), false)
	})
}

// A bound check registered on another host, with no check evidence here, is
// unknown activity this machine cannot repair. A binding with no host keeps the
// missing-coverage reading.
func TestACheckRegisteredOnAnotherHostIsNotMissingCoverageHere(t *testing.T) {
	t.Setenv("COMMIT_REVIEW_HOST", "host-b")
	chains := []Chain{obsChain(t, 3000, "agy/1/aaa/v1", "widget")}
	withClock(9000, func() {
		scored, err := ScoreRecord(boundCheckRecord(2000), EvidenceSet{Chains: chains})
		if err != nil {
			t.Fatal(err)
		}
		obs := observationOf(t, scored)
		check(t, "the binding names the registering host", obs.Checks.RegisteredHost, "host-a")
		check(t, "which is another host", obs.Checks.Elsewhere(), true)
		check(t, "so the zero is still unknown", obs.Checks.Known(), false)
		check(t, "but no coverage is missing here", MissingCoverage(obs), false)

		scored, err = ScoreRecord(boundCheckRecord(2000), EvidenceSet{
			Chains: chains, CheckRepos: map[string]bool{boundRoot: true}})
		if err != nil {
			t.Fatal(err)
		}
		obs = observationOf(t, scored)
		check(t, "evidence here makes the count known", obs.Checks.Known(), true)
		check(t, "and not elsewhere", obs.Checks.Elsewhere(), false)

		legacy := boundCheckRecord(2000)
		delete(legacy, CheckHostKey)
		scored, err = ScoreRecord(legacy, EvidenceSet{Chains: chains})
		if err != nil {
			t.Fatal(err)
		}
		obs = observationOf(t, scored)
		check(t, "a binding with no host is not elsewhere", obs.Checks.Elsewhere(), false)
		check(t, "and its coverage is missing", MissingCoverage(obs), true)
	})
}
