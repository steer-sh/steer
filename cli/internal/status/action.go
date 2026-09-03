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
)

// ActionOrder is the order the sections render in.
var ActionOrder = []string{
	ActionHarmful, ActionAdjudicate, ActionRepair, ActionWait, ActionAccept,
}

// ActionHeading is the section header for one action.
var ActionHeading = map[string]string{
	ActionHarmful:    "Harmful — the class grew after install",
	ActionAdjudicate: "Adjudicate — this is what grows the evidence",
	ActionRepair:     "Repair — each of these names a fix that exists",
	ActionWait:       "Wait — the window is still filling",
	ActionAccept:     "Accept — measurement will not resolve these at your current rate",
}

// actionFor maps a verdict token to the action it asks for.
var actionFor = map[string]string{
	"harmful":             ActionHarmful,
	"evidence-incomplete": ActionAdjudicate,
	"not-triggered":       ActionRepair,
	"unattributed":        ActionRepair,
	"ineffective":         ActionRepair,
	"legacy-unscoped":     ActionRepair,
	"confounded":          ActionRepair,
	"class-grew":          ActionRepair,
	"collecting":          ActionWait,
	"inconclusive":        ActionAccept,
}

// actionSentence translates one verdict token into what to do about it.
var actionSentence = map[string]string{
	"harmful":             "the class grew after install — remove the rule, then retire the record",
	"evidence-incomplete": "chains inside the windows still owe an adjudication",
	"not-triggered":       "the matcher never matched a unit in the after-window — rework the match",
	"unattributed":        "the class fell with no firing — the rule may not be what changed",
	"ineffective":         "the rule did not reduce the class — rework or retire it",
	"legacy-unscoped":     "no eligibility rule was frozen — run: steer intervention scope",
	"confounded":          "the measurement conditions changed mid-window — re-register to open a clean window",
	"class-grew":          "the refused class grew — reconsider the refusal",
	"collecting":          "the window is still filling",
	"inconclusive":        "the range cannot place the effect",
}

// ActionFor names the action a verdict asks for. An unmeasurable record moves to
// accept whatever its token says; harmful is exempt.
func ActionFor(verdict string, unmeasurable bool) string {
	if unmeasurable && verdict != "harmful" {
		return ActionAccept
	}
	if a, ok := actionFor[verdict]; ok {
		return a
	}
	return ActionRepair
}

// ActionSentence is the verdict translated into an instruction.
func ActionSentence(verdict string, unmeasurable bool) string {
	if unmeasurable && verdict != "harmful" {
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
	PerDay    float64
	Known     bool
	QuietDays int
}

// RateFrom reads a chain rate off the snapshot's recent chains, newest last.
func RateFrom(chains []ChainSummary, now time.Time) ChainRate {
	if len(chains) < 2 {
		return ChainRate{}
	}
	oldest := chains[0].StartTS
	newest := chains[len(chains)-1].StartTS
	quiet := int(now.Sub(time.Unix(int64(newest), 0)).Hours() / 24)
	if quiet >= quietDays {
		return ChainRate{QuietDays: quiet}
	}
	span := (newest - oldest) / 86400
	if span < minSpanDays {
		return ChainRate{}
	}
	return ChainRate{PerDay: float64(len(chains)-1) / span, Known: true}
}

// Unmeasurable says a required window is more than a year away at this rate. An
// unknown rate is not unmeasurable: it is unknown.
func Unmeasurable(required *int, rate ChainRate) bool {
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
	switch tier {
	case "check", "instruction":
		if target != "" {
			return fmt.Sprintf("retire ends the measurement; the rule stays in %s until you remove it", target)
		}
	case "guard":
		if target != "" {
			return fmt.Sprintf("retire ends the measurement; the %s guard stays installed until you remove it", target)
		}
		return "retire ends the measurement; the guard stays installed until you remove it"
	}
	return "retire ends the measurement; the rule is not installed in any tier"
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
