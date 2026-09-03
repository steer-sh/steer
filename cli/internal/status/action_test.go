package status

import (
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

// Every verdict the cadence queues must name an action and a sentence, or a
// queued class renders with no instruction under it.
func TestEveryAttentionVerdictNamesAnAction(t *testing.T) {
	for _, v := range cadence.AttentionVerdicts {
		if _, ok := actionFor[v]; !ok {
			t.Errorf("verdict %q maps to no action", v)
		}
		if ActionSentence(v, false) == "" {
			t.Errorf("verdict %q renders no action sentence", v)
		}
	}
	for _, a := range ActionOrder {
		if ActionHeading[a] == "" {
			t.Errorf("action %q renders no heading", a)
		}
	}
}

// The range is the scorer's claim, so it prints only where a claim was made.
func TestRangePrintsOnlyUnderAClaim(t *testing.T) {
	est := 0.39
	up := 0.83
	e := cadence.Effect{Estimate: &est, Lower: 0.11, Upper: &up,
		Confidence: 0.9, Method: "clopper-pearson / mixture-cs"}
	for _, v := range []string{"confounded", "collecting", "evidence-incomplete",
		"not-triggered", "unattributed", "ineffective", "legacy-unscoped", "class-grew"} {
		if got := EffectClaimPhrase(v, e); got != "" {
			t.Errorf("verdict %q printed a range: %q", v, got)
		}
	}
	if got := EffectClaimPhrase("effective", e); !strings.Contains(got, "61% fewer") {
		t.Errorf("effective lost its claim: %q", got)
	}
	up2 := 5.18
	est2 := 2.41
	harm := cadence.Effect{Estimate: &est2, Lower: 1.12, Upper: &up2,
		Confidence: 0.9, Method: "clopper-pearson / mixture-cs"}
	if got := EffectClaimPhrase("harmful", harm); !strings.Contains(got, "2.4x more frequent") {
		t.Errorf("harmful lost its claim: %q", got)
	}
}

// Counts carry their denominator in words, so no fraction or rate reaches the
// class line.
func TestRecurrenceCarriesNoFraction(t *testing.T) {
	got := RecurrencePhrase(
		cadence.Incidence{Hits: 4, Units: 61, Rate: 0.066},
		cadence.Incidence{Hits: 1, Units: 39, Rate: 0.026}, "chain")
	want := "1 recurrence in the 39 chains since install; 4 in the 61 before"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "/") || strings.Contains(got, "%") {
		t.Errorf("a fraction or a rate reached the class line: %q", got)
	}
}

// A host with no recent chains reports no rate, and no rate is not unmeasurable.
func TestAQuietHostWithholdsTheRate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	old := float64(now.Add(-30 * 24 * time.Hour).Unix())
	chains := []ChainSummary{
		{StartTS: old - 86400*10},
		{StartTS: old},
	}
	rate := RateFrom(chains, now)
	if rate.Known {
		t.Fatal("a 30-day-old newest chain must not yield a known rate")
	}
	n := 755
	if Unmeasurable("confounded", "chain", &n, rate) {
		t.Error("an unknown rate must not read as unmeasurable")
	}
	got := RatePhrase(&n, "chain", rate)
	if !strings.Contains(got, "the rate is unknown") {
		t.Errorf("the phrase must withhold the calendar half: %q", got)
	}
	if strings.Contains(got, "unreachable") {
		t.Errorf("the phrase must not claim unreachable: %q", got)
	}
}

// A reachable window stays a wait; an unreachable one becomes a decision.
func TestUnmeasurableSeparatesAWaitFromANever(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	chains := []ChainSummary{}
	for i := 60; i >= 0; i-- {
		chains = append(chains, ChainSummary{StartTS: float64(now.Add(-time.Duration(i) * 24 * time.Hour).Unix())})
	}
	rate := RateFrom(chains, now)
	if !rate.Known {
		t.Fatal("a 60-day sample must yield a rate")
	}
	near, far := 5, 3000
	if Unmeasurable("collecting", "chain", &near, rate) {
		t.Error("a short window must stay a wait")
	}
	if !Unmeasurable("collecting", "chain", &far, rate) {
		t.Error("a window years away must read as unmeasurable")
	}
	// A settled verdict already decided; it is never regraded as unmeasurable.
	for _, v := range []string{"effective", "harmful", "ineffective"} {
		if Unmeasurable(v, "chain", &far, rate) {
			t.Errorf("verdict %q must not be regraded as unmeasurable", v)
		}
	}
	// The rate counts chains, so a session-unit record is never graded on it.
	if Unmeasurable("collecting", "session", &far, rate) {
		t.Error("a session-unit record must not be graded against a chain rate")
	}
	if got := RatePhrase(&far, "session", rate); !strings.Contains(got, "no accrual rate here") {
		t.Errorf("a session window must not borrow the chain rate: %q", got)
	}
	if got := ActionFor("confounded", true); got != ActionAccept {
		t.Errorf("an unmeasurable confound must move to accept, got %q", got)
	}
	if got := ActionFor("harmful", true); got != ActionHarmful {
		t.Errorf("harmful is exempt from the unmeasurable move, got %q", got)
	}
}

// Retire closes the measurement and removes nothing, so every removal sentence
// names where the rule still lives.
func TestRemovalNamesTheTierThatKeepsRunning(t *testing.T) {
	for _, tc := range []struct{ tier, target, want string }{
		{"check", "check.sh", "the rule stays in check.sh"},
		{"instruction", "CLAUDE.md", "the rule stays in CLAUDE.md"},
		{"guard", "no-bare-python", "guard stays installed"},
		// The CLI's own flag vocabulary, which the later names alias.
		{"steer", "no-bare-python", "guard stays installed"},
		{"prose", "CLAUDE.md", "the rule stays in CLAUDE.md"},
		{"prompt", "review.md", "the rule stays in review.md"},
		{"", "", "not installed in any tier"},
	} {
		got := RemovalPhrase(tc.tier, tc.target)
		if !strings.Contains(got, tc.want) {
			t.Errorf("tier %q: got %q, want it to contain %q", tc.tier, got, tc.want)
		}
		if !strings.Contains(got, "retire ends the measurement") {
			t.Errorf("tier %q: the sentence must say what retire does: %q", tc.tier, got)
		}
	}
}

// Harmful leads the sections and settled closes them: the first asks the most of
// the reader and the last asks nothing.
func TestHarmfulSortsFirst(t *testing.T) {
	if ActionOrder[0] != ActionHarmful {
		t.Errorf("harmful must lead the sections, got %q", ActionOrder[0])
	}
	if ActionOrder[len(ActionOrder)-1] != ActionSettled {
		t.Errorf("settled must close the sections, got %q", ActionOrder[len(ActionOrder)-1])
	}
}

// A burst is not a rate: a sample shorter than minSpanDays projects nothing.
func TestAShortSampleProjectsNoRate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	chains := []ChainSummary{}
	for i := 0; i < 20; i++ {
		chains = append(chains, ChainSummary{StartTS: float64(now.Add(-time.Duration(20-i) * time.Hour).Unix())})
	}
	rate := RateFrom(chains, now)
	if rate.Known {
		t.Fatal("20 chains inside one day must not yield a rate")
	}
	n := 755
	got := RatePhrase(&n, "chain", rate)
	if !strings.Contains(got, "too short to project a rate") {
		t.Errorf("the phrase must name the short sample: %q", got)
	}
}

// A capped chain list cannot carry the corpus rate: an active host's newest 20
// chains span days, so the rate has to be read over every chain the corpus holds.
func TestTheRateIsReadOverTheWholeCorpus(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	oldest := float64(now.Add(-200 * 24 * time.Hour).Unix())
	newest := float64(now.Add(-1 * time.Hour).Unix())
	if r := RateOver(400, oldest, newest, now); !r.Known {
		t.Fatal("a 200-day corpus must yield a rate")
	}
	// The same corpus truncated to its newest 20 chains spans under two days.
	tail := []ChainSummary{}
	for i := 20; i > 0; i-- {
		tail = append(tail, ChainSummary{StartTS: float64(now.Add(-time.Duration(i) * time.Hour).Unix())})
	}
	if RateFrom(tail, now).Known {
		t.Error("a truncated tail must not be mistaken for a corpus rate")
	}
}

// A settled verdict never reads as a repair. The default arm of ActionFor sent
// every unmapped token to repair, which headed a proven rule as a broken one.
func TestSettledVerdictsAreNotRepairs(t *testing.T) {
	for _, v := range []string{"effective", "class-faded", "class-steady"} {
		if got := ActionFor(v, false); got != ActionSettled {
			t.Errorf("ActionFor(%q) = %q, want %q", v, got, ActionSettled)
		}
	}
	for _, v := range []string{"class-inconclusive", "unscored", "a-token-from-a-newer-engine"} {
		if got := ActionFor(v, false); got == ActionRepair {
			t.Errorf("ActionFor(%q) = repair; an uninterpretable state names no fix", v)
		}
	}
	// A settled verdict stays settled whatever the window costs. Unmeasurable
	// never returns true for one, so this pins the arm rather than the caller.
	for _, v := range []string{"effective", "class-faded", "class-steady"} {
		if got := ActionFor(v, true); got != ActionSettled {
			t.Errorf("ActionFor(%q, unmeasurable) = %q, want %q", v, got, ActionSettled)
		}
	}
}

// An unreachable window overrides one action only: wait. A repair names a fix the
// window length does not remove, so it keeps both its section and its sentence.
func TestUnmeasurableOverridesOnlyAWait(t *testing.T) {
	for _, v := range []string{"not-triggered", "unattributed", "ineffective", "legacy-unscoped", "no-baseline"} {
		if got := ActionFor(v, true); got != ActionRepair {
			t.Errorf("ActionFor(%q, unmeasurable) = %q, want %q", v, got, ActionRepair)
		}
		if got := ActionSentence(v, true); got != actionSentence[v] {
			t.Errorf("ActionSentence(%q, unmeasurable) = %q, want its own repair", v, got)
		}
	}
	if got := ActionFor("evidence-incomplete", true); got != ActionAdjudicate {
		t.Errorf("ActionFor(evidence-incomplete, unmeasurable) = %q, want %q", got, ActionAdjudicate)
	}
	if got := ActionFor("collecting", true); got != ActionAccept {
		t.Errorf("ActionFor(collecting, unmeasurable) = %q, want %q", got, ActionAccept)
	}
}

// Every verdict in a repair section names the fix it asks for.
func TestEveryRepairNamesItsFix(t *testing.T) {
	for v, act := range actionFor {
		if act == ActionRepair && actionSentence[v] == "" {
			t.Errorf("verdict %q heads a repair but names no fix", v)
		}
	}
}

// Every action a verdict can ask for has a heading and a place in the order.
func TestEveryActionIsOrderedAndHeaded(t *testing.T) {
	seen := map[string]bool{}
	for _, act := range ActionOrder {
		if ActionHeading[act] == "" {
			t.Errorf("action %q has no heading", act)
		}
		seen[act] = true
	}
	for v, act := range actionFor {
		if !seen[act] {
			t.Errorf("verdict %q maps to %q, which ActionOrder does not render", v, act)
		}
	}
	if !seen[ActionFor("a-token-from-a-newer-engine", false)] {
		t.Error("the default action is not in ActionOrder")
	}
}

// An unreachable window replaces only a sentence telling the reader to wait. A
// confounded class keeps its own reason: the windows are not comparable, which
// is why no window decides it.
func TestUnmeasurableKeepsAnAcceptVerdictsOwnReason(t *testing.T) {
	if got := ActionSentence("confounded", true); got != actionSentence["confounded"] {
		t.Errorf("ActionSentence(confounded, unmeasurable) = %q, want its own reason", got)
	}
	if got := ActionSentence("inconclusive", true); got != actionSentence["inconclusive"] {
		t.Errorf("ActionSentence(inconclusive, unmeasurable) = %q, want its own reason", got)
	}
	if got := ActionSentence("collecting", true); got == actionSentence["collecting"] {
		t.Error("ActionSentence(collecting, unmeasurable) still says the window is filling")
	}
}
