// The observation-to-action table. A field observation names a measurement state;
// this names what the reader does about it, and orders the sections by that action.
// Terminal and TUI phrase and sort from here; --json keeps the observation.

package status

import (
	"fmt"
	"math"
	"time"

	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// The actions an observation can ask of its reader. No action asks for removal:
// silence justifies no retirement.
const (
	ActionInspect = "inspect"
	ActionRepair  = "repair"
	ActionObserve = "observe"
)

// ActionOrder is the order the sections render in.
var ActionOrder = []string{ActionInspect, ActionRepair, ActionObserve}

// ActionHeading is the section header for one action. Each heading names the
// object the reader acts on.
var ActionHeading = map[string]string{
	ActionInspect: "Observed problems — inspect the example",
	ActionRepair:  "Coverage evidence — repair the accounting",
	ActionObserve: "Quiet windows — nothing is asked of you",
}

// ActionFor is total over observations: an observation matching no branch lands
// in a section rather than vanishing from the inventory.
//
// Recurrence outranks coverage. A recurrence is an observed fact and stays true
// whatever the coverage counts say, while coverage explains a measurement that is
// missing; ordering coverage first would file an actively recurring class under a
// repair to its bookkeeping and hide the escape.
//
// ZeroEligibleAfter is deliberately not a repair. Every freshly installed record
// has zero eligible after-units, and prevention and absent opportunity both
// produce silence — neither is a fault.
func ActionFor(obs mine.Observation, scope *cadence.Scope) string {
	switch {
	case obs.Recurrence.Occurred:
		return ActionInspect
	case scope == nil || missingCoverage(obs):
		return ActionRepair
	}
	return ActionObserve
}

// ObservationLabel is the compact label a badge shows. It names a STATE and
// states no direction: the comparison behind these counts is uncontrolled, so no
// label may read as a claim about the fix.
func ObservationLabel(obs mine.Observation) string {
	switch {
	case obs.Recurrence.Occurred:
		return "Recurrence observed"
	case missingCoverage(obs):
		return "Coverage incomplete"
	case obs.Coverage.ZeroEligibleAfter:
		return "No eligible work yet"
	}
	return "No recurrence observed"
}

// ActionSentence is the observation translated into an instruction. A declined
// record installed no fix, so its recurrence is phrased with no rework implied.
func ActionSentence(obs mine.Observation, scope *cadence.Scope) string {
	switch {
	case obs.Recurrence.Occurred && obs.Firing.Known() && obs.Firing.Rows == 0:
		return "the problem returned while the guard recorded no block — inspect the rule, its coverage and its registration"
	case obs.Recurrence.Occurred && obs.Declined:
		return "the problem recurred since this fix was declined, and no fix is installed — inspect the newest example"
	case obs.Recurrence.Occurred:
		return "inspect the newest example and decide whether the fix needs reworking"
	case scope == nil || missingCoverage(obs):
		return "read the coverage before deciding what to change"
	case obs.Coverage.ZeroEligibleAfter:
		return "no eligible work has arrived since this went in"
	}
	return "eligible work is present and the problem did not come back"
}

// missingCoverage reports units a window could not account for, plus the audit
// stream a guard's activity cannot be read without.
func missingCoverage(obs mine.Observation) bool {
	if obs.Firing.Applicable && !obs.Firing.LogPresent {
		return true
	}
	return coverageCause(obs.Coverage.Before) || coverageCause(obs.Coverage.After)
}

// coverageCause reads both corpora's unknown keys: the chain corpus counts a cause
// per unknown and the session corpus counts one bucket.
func coverageCause(c map[string]any) bool {
	return numInt(c["unknown_scope"]) > 0 || numInt(c["unknown_evidence"]) > 0 ||
		numInt(c["pending"]) > 0 || numInt(c["unknown"]) > 0
}

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

// RatePhrase names a required window in units and the wait in calendar time. It
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

// RecurrencePhrase says whether the class came back since the rule went in. Each
// count carries its denominator in words, so no fraction prints.
func RecurrencePhrase(before, after cadence.Incidence, unit string) string {
	unit = orUnit(unit)
	s := fmt.Sprintf("%d %s in the %d %s since install", after.Hits, plural("recurrence", after.Hits), after.Units, UnitName(unit, after.Units))
	if before.Units > 0 {
		s += fmt.Sprintf("; %d in the %d before", before.Hits, before.Units)
	}
	return s
}
