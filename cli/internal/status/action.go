// The verdict-to-action table. A verdict token names a measurement state; this
// names what the reader does about it, and orders the sections by that action.
// Terminal, HTML and TUI phrase and sort from here; --json keeps the token.

package status

import (
	"fmt"
	"math"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

// The actions a class can ask of its reader.
const (
	ActionHarmful    = "harmful"
	ActionAdjudicate = "adjudicate"
	ActionRepair     = "repair"
	ActionWait       = "wait"
	ActionAccept     = "accept"
	ActionSettled    = "settled"
)

// ActionOrder is the order the sections render in.
var ActionOrder = []string{
	ActionHarmful, ActionAdjudicate, ActionRepair, ActionWait, ActionAccept,
	ActionSettled,
}

// ActionHeading is the section header for one action.
var ActionHeading = map[string]string{
	ActionHarmful:    "Harmful — the class grew after install",
	ActionAdjudicate: "Adjudicate — this is what grows the evidence",
	ActionRepair:     "Repair — each of these names a fix that exists",
	ActionWait:       "Wait — the window is still filling",
	ActionAccept:     "Accept — no repair settles these; read the counts and decide",
	ActionSettled:    "Settled — the measurement decided these; nothing to do",
}

// actionFor maps a verdict token to the action it asks for.
var actionFor = map[string]string{
	"harmful":             ActionHarmful,
	"evidence-incomplete": ActionAdjudicate,
	"not-triggered":       ActionRepair,
	"unattributed":        ActionRepair,
	"ineffective":         ActionRepair,
	"legacy-unscoped":     ActionRepair,
	"confounded":          ActionAccept,
	"class-grew":          ActionRepair,
	"no-baseline":         ActionRepair,
	"collecting":          ActionWait,
	"inconclusive":        ActionAccept,
	"class-inconclusive":  ActionAccept,
	"unscored":            ActionAccept,
	"effective":           ActionSettled,
	"class-faded":         ActionSettled,
	"class-steady":        ActionSettled,
}

// actionSentence translates one verdict token into what to do about it.
var actionSentence = map[string]string{
	"harmful":             "the class grew after install — remove the rule, then retire the record",
	"evidence-incomplete": "chains inside the windows still owe an adjudication",
	"not-triggered":       "the matcher never matched a unit in the after-window — rework the match",
	"unattributed":        "the class fell with no firing — the rule may not be what changed",
	"ineffective":         "the rule did not reduce the class — rework or retire it",
	"legacy-unscoped":     "no eligibility rule was frozen — run: steer intervention scope",
	"confounded":          "the windows ran under different conditions and cannot be compared — read the counts and decide",
	"class-grew":          "the refused class grew — reconsider the refusal",
	"no-baseline":         "the before-window carries no occurrence of the class — rework the match, or retire it",
	"collecting":          "the window is still filling",
	"inconclusive":        "the range cannot place the effect",
}

// claimsAnEffect reports whether a verdict already states an effect. A settled
// class is never reclassified as unmeasurable: its window did reach a decision.
func claimsAnEffect(verdict string) bool {
	switch verdict {
	case "effective", "harmful", "ineffective", "class-grew", "class-faded", "class-steady":
		return true
	}
	return false
}

// ActionFor names the action a verdict asks for. An unreachable window overrides
// one action only — wait — because waiting is the one instruction it contradicts.
// A repair stays a repair: reworking a matcher is available whatever the window
// costs, and filing it under accept would hide a fix the reader can still make.
// A token this binary does not know reads as accept, never as repair: repair
// heads a fix that exists, and an uninterpretable state names no fix.
func ActionFor(verdict string, unmeasurable bool) string {
	a, ok := actionFor[verdict]
	if !ok {
		return ActionAccept
	}
	if unmeasurable && a == ActionWait {
		return ActionAccept
	}
	return a
}

// ActionSentence is the verdict translated into an instruction. The unreachable
// window replaces only the sentence it contradicts — the one that tells the
// reader to wait. Every other verdict keeps its own instruction: a repair names
// a fix the window length does not remove, and an accept names why the windows
// do not decide. RatePhrase states the reachability beside either.
func ActionSentence(verdict string, unmeasurable bool) string {
	if unmeasurable && actionFor[verdict] == ActionWait {
		return "no reachable window can decide this — keep it as a rule, or retire it"
	}
	return actionSentence[verdict]
}

// unmeasurableYears is the wait past which a required window stops being a wait.
const unmeasurableYears = 1.0

// quietDays is the silence past which this host's chain rate is withheld;
// minSpanDays is the shortest sample a rate may be projected from.
const (
	quietDays   = 14
	minSpanDays = 14
)

// ChainRate is the recent chain accrual read off one host's shard. Known is false
// when the sample spans fewer than minSpanDays, or when the newest chain is older
// than quietDays.
type ChainRate struct {
	PerDay    float64 `json:"per_day"`
	Known     bool    `json:"known"`
	QuietDays int     `json:"quiet_days,omitempty"`
}

// RateOver reads a chain rate off a whole corpus: how many chains it holds and the
// epoch seconds of its oldest and newest. A caller must not pass a truncated tail —
// a capped list of an active host spans too little to clear minSpanDays.
func RateOver(units int, oldest, newest float64, now time.Time) ChainRate {
	if units < 2 || newest <= oldest {
		return ChainRate{}
	}
	quiet := int(now.Sub(time.Unix(int64(newest), 0)).Hours() / 24)
	if quiet >= quietDays {
		return ChainRate{QuietDays: quiet}
	}
	span := (newest - oldest) / 86400
	if span < minSpanDays {
		return ChainRate{}
	}
	return ChainRate{PerDay: float64(units-1) / span, Known: true}
}

// RateFrom reads a rate off a chain list, newest last.
func RateFrom(chains []ChainSummary, now time.Time) ChainRate {
	if len(chains) < 2 {
		return ChainRate{}
	}
	return RateOver(len(chains), chains[0].StartTS, chains[len(chains)-1].StartTS, now)
}

// Unmeasurable says a required window is more than a year away at this rate. An
// unknown rate is not unmeasurable: it is unknown. The rate counts chains, so a
// record denominated in any other unit is never graded against it, and a verdict
// that already claims an effect is never regraded at all.
func Unmeasurable(verdict, unit string, required *int, rate ChainRate) bool {
	if claimsAnEffect(verdict) || orUnit(unit) != "chain" {
		return false
	}
	if required == nil || *required <= 0 || !rate.Known {
		return false
	}
	return float64(*required)/rate.PerDay > unmeasurableYears*365
}

// RatePhrase names the required window in units and the wait in calendar time. It
// withholds the calendar half when the rate is unknown.
func RatePhrase(required *int, unit string, rate ChainRate) string {
	if required == nil || *required <= 0 {
		return ""
	}
	unit = orUnit(unit)
	window := fmt.Sprintf("a clean window of %d %s", *required, plural(unit, *required))
	if unit != "chain" {
		return window + "; this corpus reports no accrual rate here"
	}
	if !rate.Known {
		if rate.QuietDays > 0 {
			return fmt.Sprintf("%s; no %s here in %d days, so the rate is unknown",
				window, plural(unit, 2), rate.QuietDays)
		}
		return window + "; the recent sample here is too short to project a rate"
	}
	return fmt.Sprintf("%s is about %s away at your recent %s rate",
		window, humanDays(float64(*required)/rate.PerDay), unit)
}

// humanDays renders a wait in the largest unit that keeps it a small number.
func humanDays(days float64) string {
	switch {
	case days < 21:
		return fmt.Sprintf("%d days", int(math.Round(days)))
	case days < 90:
		return fmt.Sprintf("%d weeks", int(math.Round(days/7)))
	case days < 365:
		return fmt.Sprintf("%d months", int(math.Round(days/30)))
	}
	return fmt.Sprintf("%.0f years", math.Round(days/365))
}

// RecurrencePhrase says whether the class came back since the rule went in. Each
// count carries its denominator in words, so no fraction prints.
func RecurrencePhrase(before, after cadence.Incidence, unit string) string {
	unit = orUnit(unit)
	s := fmt.Sprintf("%d %s in the %d %s since install",
		after.Hits, plural("recurrence", after.Hits), after.Units, plural(unit, after.Units))
	if before.Units > 0 {
		s += fmt.Sprintf("; %d in the %d before", before.Hits, before.Units)
	}
	return s
}

// EffectClaimPhrase renders the range only under the two verdicts where the
// scorer made a claim. It is "" everywhere else.
func EffectClaimPhrase(verdict string, e cadence.Effect) string {
	if verdict != "effective" && verdict != "harmful" {
		return ""
	}
	rr := EffectPhrase(e)
	if rr == "" || e.Estimate == nil {
		return rr
	}
	return fmt.Sprintf("%s, %.0f%% confidence   %s",
		magnitude(*e.Estimate), e.Confidence*100, rr)
}

// magnitude states a relative risk in the direction the reader reads it.
func magnitude(rr float64) string {
	if rr < 1 {
		return fmt.Sprintf("%.0f%% fewer", (1-rr)*100)
	}
	return fmt.Sprintf("%.1fx more frequent", rr)
}

// RemovalPhrase says what retire does and where the rule lives. Retire closes the
// measurement and removes nothing.
func RemovalPhrase(tier, target string) string {
	const ends = "retire ends the measurement; "
	switch tier {
	// The CLI writes check|steer|prose|prompt; check|guard|instruction|review is
	// the same ladder under its later names, and both reach a reader here.
	case "check", "prose", "instruction", "prompt", "review":
		if target != "" {
			return ends + fmt.Sprintf("the rule stays in %s until you remove it", target)
		}
	case "steer", "guard":
		if target != "" {
			return ends + fmt.Sprintf("the %s guard stays installed until you remove it", target)
		}
		return ends + "the guard stays installed until you remove it"
	}
	if target != "" {
		return ends + fmt.Sprintf("the rule stays in %s until you remove it", target)
	}
	return ends + "the rule is not installed in any tier"
}

// orUnit names the denominator when a record carries no unit tag.
func orUnit(unit string) string {
	if unit == "" {
		return "unit"
	}
	return unit
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	if word == "recurrence" {
		return "recurrences"
	}
	return word + "s"
}
