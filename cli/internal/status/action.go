// The verdict-to-action table. A verdict token names a measurement state; this
// names what the reader does about it, and orders the sections by that action.
// Terminal, HTML and TUI phrase and sort from here; --json keeps the token.

package status

import (
	"fmt"
	"math"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/tier"
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

// ActionHeading is the section header for one action. Each heading names the
// object the reader acts on: a bare verb ("Accept") names nothing to hold, and
// these six headings organize the whole screen.
var ActionHeading = map[string]string{
	ActionHarmful:    "Problems that grew after a fix went in — review the fix",
	ActionAdjudicate: "Missing evidence — these results stay on hold until the decisions are recorded",
	ActionRepair:     "Fixes to rework — each one names a change you can make",
	ActionWait:       "Fixes still collecting evidence — no result yet",
	ActionAccept:     "Results with no repair available — read the counts and judge these yourself",
	ActionSettled:    "Results that are decided — nothing to do",
}

// meaning is one verdict token's whole presentation: the compact label a badge
// shows, the action the token asks of its reader, and the sentence that names
// that action. One entry per token, so a token cannot carry a label with no
// sentence, or an action with no label.
//
// A label names a STATE, never an action — the action sentence names what the
// reader does. A label states a direction only under the four tokens where the
// scorer crossed a bound and claimed one: effective, harmful, class-grew and
// class-faded. Anywhere else a directional word states the point claim the
// scorer withheld.
type meaning struct {
	Label    string
	Action   string
	Sentence string
}

// meanings is the one table Terminal, HTML and TUI read. Nothing else names a
// label, and no surface prints the raw token.
var meanings = map[string]meaning{
	"harmful": {"More with fix", ActionHarmful,
		"the problem grew after this fix went in — remove the rule, then stop tracking it"},
	"evidence-incomplete": {"Missing evidence", ActionAdjudicate,
		"review sequences inside the compared periods still owe a decision"},
	"not-triggered": {"No blocks recorded", ActionRepair,
		"nothing matched this problem in the work since install — rework how it is identified"},
	"unattributed": {"Benefit unconfirmed", ActionRepair,
		"the problem became less frequent, but this guard blocked nothing — the fix may not be what changed"},
	"ineffective": {"Target not met", ActionRepair,
		"this fix did not reduce the problem to the target — rework it, or stop tracking it"},
	"legacy-unscoped": {"Comparison undefined", ActionRepair,
		"no rule was saved for which work counts — run: steer fix scope"},
	"no-baseline": {"No earlier examples", ActionRepair,
		"the earlier period holds no example of this problem — rework how it is identified, or stop tracking it"},
	"class-grew": {"Grew, fix declined", ActionRepair,
		"the problem grew after the suggestion was declined — reconsider that decision"},
	"collecting": {"Too early to tell", ActionWait,
		"more comparable work is needed before this can be decided"},
	"confounded": {"Not comparable", ActionAccept,
		"the two periods ran under different conditions and cannot be compared — read the counts and decide"},
	"inconclusive": {"No clear result", ActionAccept,
		"the range cannot place the effect"},
	"class-inconclusive": {"No clear trend, fix declined", ActionAccept,
		"the evidence does not show how the problem changed"},
	"unscored": {"Not assessed", ActionAccept,
		"no result is available for this fix on this machine"},
	"effective":    {"Fewer with fix", ActionSettled, ""},
	"class-faded":  {"Fewer, fix declined", ActionSettled, ""},
	"class-steady": {"Steady, fix declined", ActionSettled, ""},
}

// LabelUnknown is what a token this binary cannot interpret reads as. The token
// itself stays in --json, where a reader can chase it.
const LabelUnknown = "Unknown result"

// LabelUnmeasurable is the badge for a window no reachable amount of work closes.
// It is a reading of required_after, never a state the scorer wrote.
const LabelUnmeasurable = "Window unreachable"

// VerdictLabel is the compact label a badge shows for one verdict. Every surface
// reads it; none prints the token.
func VerdictLabel(verdict string, unmeasurable bool) string {
	if unmeasurable && meanings[verdict].Action == ActionWait {
		return LabelUnmeasurable
	}
	m, ok := meanings[verdict]
	if !ok || m.Label == "" {
		return LabelUnknown
	}
	return m.Label
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
	m, ok := meanings[verdict]
	if !ok {
		return ActionAccept
	}
	if unmeasurable && m.Action == ActionWait {
		return ActionAccept
	}
	return m.Action
}

// ActionSentence is the verdict translated into an instruction. The unreachable
// window replaces only the sentence it contradicts — the one that tells the
// reader to wait. Every other verdict keeps its own instruction: a repair names
// a fix the window length does not remove, and an accept names why the periods
// do not decide. RatePhrase states the reachability beside either.
func ActionSentence(verdict string, unmeasurable bool) string {
	m, ok := meanings[verdict]
	if unmeasurable && m.Action == ActionWait {
		return "no reachable window can decide this — keep it as a rule, or stop tracking it"
	}
	if !ok {
		return "this version of Steer cannot interpret this result — read the token in the JSON snapshot"
	}
	return m.Sentence
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
	window := fmt.Sprintf("a clean window of %d %s", *required, UnitName(unit, *required))
	if unit != "chain" {
		return window + "; how fast those accrue here is not measured"
	}
	if !rate.Known {
		if rate.QuietDays > 0 {
			return fmt.Sprintf("%s; none here in %d days, so the rate is unknown",
				window, rate.QuietDays)
		}
		return window + "; the recent sample here is too short to project a rate"
	}
	return fmt.Sprintf("%s is about %s away at your recent rate",
		window, humanDays(float64(*required)/rate.PerDay))
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
		after.Hits, plural("recurrence", after.Hits), after.Units, UnitName(unit, after.Units))
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

// RemovalPhrase says what stopping the tracking does and where the rule lives. It
// closes the measurement and removes nothing, so it names the tier the rule keeps
// running in. It reads BOTH ladder spellings through internal/tier: a guard
// reported as installed in no tier would keep firing while the reader believed it
// gone.
func RemovalPhrase(t, target string) string {
	const ends = "stopping the tracking ends the measurement; "
	kind, ok := tier.Of(t)
	if !ok {
		if target != "" {
			return ends + fmt.Sprintf("the rule stays in %s until you remove it", target)
		}
		return ends + "no fix is installed in any tier"
	}
	if kind == tier.Guard {
		if target != "" {
			return ends + fmt.Sprintf("the %s guard stays installed until you remove it", target)
		}
		return ends + "the guard stays installed until you remove it"
	}
	if target != "" {
		return ends + fmt.Sprintf("the rule stays in %s until you remove it", target)
	}
	return ends + "no fix is installed in any tier"
}

// UnitName is the counted item in the reader's words. The stored unit is "chain"
// or "session", and a reader has neither word. The vocabulary lint cannot catch
// this one: here the machine word arrives as data rather than as a literal, so
// every caller that puts a unit in a sentence has to come through here. It is
// exported for exactly that reason — cmd and the TUI render units too.
func UnitName(unit string, n int) string {
	switch orUnit(unit) {
	case "chain":
		return plural("review sequence", n)
	case "session":
		return plural("agent session", n)
	}
	return plural("item", n)
}

// orUnit names the counted item when a record carries no unit tag.
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
