package status

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/mods"
)

func armed(h string) ScopeArming {
	return ScopeArming{
		Harness: h, Scope: "global", Registered: true, Effective: true,
		Trust: TrustNotApplicable, Resolvable: ResolveResolvable, Observed: 1,
	}
}

func available() reviewers.Probe {
	return reviewers.Probe{Provider: "agy", Executable: "agy", State: reviewers.ProbeAvailable}
}

func snapshot(rows ...ScopeArming) *SystemStatus {
	return &SystemStatus{Posture: Posture{CommitGate: rows, Resolver: []ScopeArming{armed(string(harness.ClaudeCode))}}}
}

// An empty machine gets the answer, not an inventory it has to read a verdict out of.
func TestNoRegistrationSaysCommitsAreNotReviewed(t *testing.T) {
	r := ReadinessOf(snapshot(), available())
	if r.Reviewing {
		t.Fatal("Reviewing is true with no registration")
	}
	if len(r.Steps) != 1 || !strings.Contains(r.Steps[0].Fix, "commit-gate install") {
		t.Fatalf("steps = %+v; want one naming the install", r.Steps)
	}
}

func TestAnArmedResolvableGateWithAnAvailableReviewerIsReviewing(t *testing.T) {
	r := ReadinessOf(snapshot(armed(string(harness.ClaudeCode))), available())
	if !r.Reviewing || len(r.Steps) != 0 {
		t.Fatalf("Readiness = %+v; want reviewing with no steps", r)
	}
	if r.Note != "" {
		t.Errorf("Note = %q; an observed channel needs none", r.Note)
	}
}

// The four states are distinct, and registration is the weakest of them. A machine
// that registered a gate it cannot run must not read as reviewing.
func TestEachOpenStateWithholdsTheReviewingAnswer(t *testing.T) {
	missingReviewer := reviewers.Probe{Provider: "agy", Executable: "agy", State: reviewers.ProbeMissing}
	inert := armed(string(harness.ClaudeCode))
	inert.Effective = false
	unresolvable := armed(string(harness.ClaudeCode))
	unresolvable.Resolvable = ResolveMissing
	untrusted := armed(string(harness.Codex))
	untrusted.Trust = TrustUnknown

	cases := map[string]Readiness{
		"reviewer missing": ReadinessOf(snapshot(armed(string(harness.ClaudeCode))), missingReviewer),
		"inert matcher":    ReadinessOf(snapshot(inert), available()),
		"command missing":  ReadinessOf(snapshot(unresolvable), available()),
		"trust pending":    ReadinessOf(snapshot(untrusted), available()),
	}
	for name, r := range cases {
		if r.Reviewing {
			t.Errorf("%s: Reviewing is true", name)
		}
		if len(r.Steps) == 0 && len(r.Unverified) == 0 {
			t.Errorf("%s: no step names what remains", name)
		}
	}
}

// A step with no command leaves the reader where the old success box did.
func TestEveryStepNamesItsRepair(t *testing.T) {
	untrusted := armed(string(harness.Codex))
	untrusted.Trust = TrustRecordPresent
	untrusted.Resolvable = ResolveMissing
	r := ReadinessOf(snapshot(untrusted), reviewers.Probe{Provider: "nope", State: reviewers.ProbeMissing})
	if len(r.Steps) != 2 {
		t.Fatalf("steps = %d; want reviewer and command", len(r.Steps))
	}
	for _, step := range r.Steps {
		if step.Title == "" || step.Fix == "" {
			t.Errorf("step %+v is missing a title or a repair", step)
		}
	}
}

// Never observed is not proof of breakage, so it rides as a note beside the answer
// rather than as a step the reader is asked to clear.
func TestNeverObservedIsANoteAndNotAStep(t *testing.T) {
	fresh := armed(string(harness.ClaudeCode))
	fresh.Observed = 0
	r := ReadinessOf(snapshot(fresh), available())
	if !r.Reviewing || len(r.Steps) != 0 {
		t.Fatalf("Readiness = %+v; want reviewing", r)
	}
	if !strings.Contains(r.Note, "no review observed") {
		t.Errorf("Note = %q", r.Note)
	}
}

// An unknown provider checked nothing, so it must not be reported as absent.
func TestAnUnknownProbeRaisesNoStep(t *testing.T) {
	if _, ok := ReviewerStep(reviewers.Probe{State: reviewers.ProbeUnknown}); ok {
		t.Error("an unknown probe raised a step")
	}
}

// One enforcing channel answers the question. A second harness that registered and
// does not enforce is a step the reader may want; it is not a reason to tell them
// their commits go unreviewed.
func TestASecondHarnessNotEnforcingDoesNotFlipTheAnswer(t *testing.T) {
	untrusted := armed(string(harness.Codex))
	untrusted.Trust = TrustRecordPresent
	r := ReadinessOf(snapshot(armed(string(harness.ClaudeCode)), untrusted), available())
	if !r.Reviewing {
		t.Fatalf("Readiness = %+v; want reviewing on the enforcing channel", r)
	}
	if len(r.Steps) != 0 || len(r.Unverified) != 1 || !strings.Contains(r.Unverified[0], "Codex global scope") {
		t.Fatalf("Readiness = %+v; want a scoped Codex trust diagnostic", r)
	}
	if !strings.Contains(r.Headline, "Claude Code global") {
		t.Errorf("Headline = %q; want the enforcing channel named", r.Headline)
	}
}

func TestUnknownTrustDoesNotClaimNonEnforcement(t *testing.T) {
	for _, trust := range []string{TrustMissing, TrustUnknown, TrustRecordPresent} {
		for _, observed := range []int{0, 1} {
			a := armed(string(harness.Codex))
			a.Trust = trust
			a.Observed = float64(observed)
			a.ObservedCorpus = "commit-review"
			r := ReadinessOf(snapshot(a), available())
			if r.Reviewing || len(r.Steps) != 0 || len(r.Unverified) != 1 {
				t.Fatalf("trust %s observed %d: %+v", trust, observed, r)
			}
			if !strings.Contains(r.Headline, "cannot be verified") || strings.Contains(r.Headline, "NOT reviewed") {
				t.Fatalf("unknown trust asserted non-enforcement: %s", r.Headline)
			}
			if phrase := ObservedPhrase(a); phrase != "" && !strings.Contains(r.Unverified[0], phrase) {
				t.Fatalf("observation missing: %+v", r)
			}
		}
	}
}

func TestMixedTrustAndSetupFailureNamesBoth(t *testing.T) {
	unknown := armed(string(harness.Codex))
	unknown.Trust = TrustRecordPresent
	for _, missingCommand := range []bool{false, true} {
		broken := armed(string(harness.ClaudeCode))
		if missingCommand {
			broken.Resolvable = ResolveMissing
		} else {
			broken.Effective = false
		}
		r := ReadinessOf(snapshot(broken, unknown), available())
		if !strings.Contains(r.Headline, "setup problems") || len(r.Steps) != 1 || len(r.Unverified) != 1 {
			t.Fatalf("mixed setup failure and trust uncertainty: %+v", r)
		}
	}
}

// The reviewer is not per harness: every channel runs it, so a reviewer that cannot
// run leaves no channel enforcing however many registrations exist.
func TestAMissingReviewerWithholdsTheAnswerOnEveryChannel(t *testing.T) {
	missing := reviewers.Probe{Provider: "agy", Executable: "agy", State: reviewers.ProbeMissing}
	r := ReadinessOf(snapshot(armed(string(harness.ClaudeCode)), armed(string(harness.Codex))), missing)
	if r.Reviewing {
		t.Fatalf("Readiness = %+v; want the answer withheld", r)
	}
}

// A resolver that cannot run leaves every review unread, but the commit is still
// reviewed, so the step never withholds the answer.
func TestAnUnresolvableResolverIsAStepBesideTheAnswer(t *testing.T) {
	s := snapshot(armed(string(harness.ClaudeCode)))
	s.Posture.Resolver[0].Resolvable = ResolveMissing
	r := ReadinessOf(s, available())
	if !r.Reviewing {
		t.Fatalf("Readiness = %+v; a broken resolver withheld the reviewing answer", r)
	}
	if len(r.Steps) != 1 || r.Steps[0].Fix != "steer bin install" || !strings.Contains(r.Steps[0].Title, "Claude Code global scope") {
		t.Fatalf("steps = %+v; want one scoped step naming the executable repair", r.Steps)
	}
}

// Any working resolver reads every harness's ledger, so a harness without its own
// registration is no step while another one works.
func TestOneWorkingResolverCoversEveryHarness(t *testing.T) {
	s := snapshot(armed(string(harness.ClaudeCode)), armed(string(harness.Codex)))
	s.Posture.Resolver = append(s.Posture.Resolver, ScopeArming{Harness: string(harness.Codex), Scope: "global"})
	if r := ReadinessOf(s, available()); len(r.Steps) != 0 {
		t.Fatalf("steps = %+v; one working resolver serves both harnesses", r.Steps)
	}
	s.Posture.Resolver[0].Registered = false
	if r := ReadinessOf(s, available()); len(r.Steps) != 1 {
		t.Fatalf("steps = %+v; with that one gone, no resolver reads the outcomes", r.Steps)
	}
}

func TestAGateWithNoResolverNamesTheInstall(t *testing.T) {
	s := snapshot(armed(string(harness.Codex)))
	s.Posture.Resolver = nil
	r := ReadinessOf(s, available())
	if !r.Reviewing || len(r.Steps) != 1 || r.Steps[0].Fix != "steer commit-gate install --harness codex --scope global" {
		t.Fatalf("Readiness = %+v; want reviewing with one install step", r)
	}
	if r := ReadinessOf(&SystemStatus{}, available()); len(r.Steps) != 1 {
		t.Fatalf("steps = %+v; with no gate, only the gate install is asked for", r.Steps)
	}
}

// A resolver with no trust record may still be skipped by its harness, so it is named
// as unverified; it is no setup failure and no claim that nothing reads outcomes.
func TestAnUnconfirmedResolverIsUnverifiedAndNotAStep(t *testing.T) {
	s := snapshot(armed(string(harness.ClaudeCode)))
	resolver := armed(string(harness.Codex))
	resolver.Trust = TrustMissing
	s.Posture.Resolver = []ScopeArming{resolver}
	r := ReadinessOf(s, available())
	if !r.Reviewing || len(r.Steps) != 0 {
		t.Fatalf("Readiness = %+v; want reviewing with no step", r)
	}
	if len(r.Unverified) != 1 || !strings.Contains(r.Unverified[0], "Codex global scope: review-outcome reader: no trust record") {
		t.Fatalf("unverified = %q; want the resolver's own trust named", r.Unverified)
	}
}

// A Claude Code mod runs before the gate's PreToolUse hook, so a mod that handles a
// tool event, or one no inspection covers, qualifies a Claude Code registration. It
// does not qualify a Codex one, and a mod read as handling no tool event adds nothing.
func TestModsQualifyOnlyAClaudeCodeRegistration(t *testing.T) {
	exposures := []mods.Exposure{
		{ID: "guard@m", State: mods.StateToolHooks, Handlers: []string{"tool.check"}},
		{ID: "pane@m", State: mods.StateNoToolHooks},
		{ID: "new@m", State: mods.StateStale},
	}
	s := snapshot(armed(string(harness.ClaudeCode)))
	s.Posture.Mods = exposures
	r := ReadinessOf(s, available())
	if !r.Reviewing {
		t.Fatal("a mod withheld reviewing; it is a diagnostic, not a broken registration")
	}
	joined := strings.Join(r.Unverified, "\n")
	if len(r.Unverified) != 2 || !strings.Contains(joined, "guard@m handles tool.check") ||
		!strings.Contains(joined, "new@m is changed since its inspection") || strings.Contains(joined, "pane@m") {
		t.Fatalf("unverified = %q; want guard@m and new@m and not pane@m", r.Unverified)
	}

	codex := snapshot(armed(string(harness.Codex)))
	codex.Posture.Mods = exposures
	if got := ReadinessOf(codex, available()).Unverified; len(got) != 0 {
		t.Fatalf("a Codex-only gate reports Claude Code mods: %q", got)
	}
}
