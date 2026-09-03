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
	if Unmeasurable(&n, rate) {
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
	if Unmeasurable(&near, rate) {
		t.Error("a short window must stay a wait")
	}
	if !Unmeasurable(&far, rate) {
		t.Error("a window years away must read as unmeasurable")
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

// Harmful leads the sections and accept closes them.
func TestHarmfulSortsFirst(t *testing.T) {
	if ActionOrder[0] != ActionHarmful {
		t.Errorf("harmful must lead the sections, got %q", ActionOrder[0])
	}
	if ActionOrder[len(ActionOrder)-1] != ActionAccept {
		t.Errorf("accept must close the sections, got %q", ActionOrder[len(ActionOrder)-1])
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
