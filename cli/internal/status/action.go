// The observation-to-action table. A field observation names a measurement state;
// this names what the reader does about it, and orders the sections by that action.
// Terminal and TUI phrase and sort from here; --json keeps the observation.

package status

import (
	"fmt"

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
	case mine.UnitsExcluded(obs):
		return "No recurrence in the counted work"
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

// missingCoverage is mine.MissingCoverage, which every reader shares. A unit the
// window could not count is disclosed, not repaired: a ledger the resolver owes is
// the resolver's own status entry, and the rest have no repair.
func missingCoverage(obs mine.Observation) bool {
	return mine.MissingCoverage(obs)
}

// RemovalPhrase says what stopping the tracking does to the fix a record binds:
// nothing. It names the command that removes the fix and never claims the fix is
// still there; ArtifactPhrase says where it stands. It reads BOTH ladder spellings
// through internal/tier.
func RemovalPhrase(t string, a *cadence.Artifact) string {
	const ends = "stopping the tracking ends the measurement"
	kind, _ := tier.Of(t)
	noun, _ := artifactNoun(kind)
	if a == nil || noun == "" {
		return ends + " and removes nothing"
	}
	out := ends + " and removes no " + noun
	if a.ID == "" {
		return out
	}
	return out + "; to remove it, run: " + RemoveCommand(t, a.ID, a.Scope)
}

// RemoveCommand is the CLI command that removes the fix with this ID from a tier,
// "" for a tier that binds no fix. A non-empty scope is passed as --scope.
func RemoveCommand(t, id, scope string) string {
	kind, _ := tier.Of(t)
	_, remove := artifactNoun(kind)
	if remove == "" {
		return ""
	}
	cmd := remove + id
	if scope != "" {
		cmd += " --scope " + scope
	}
	return cmd
}

// ArtifactNoun names the fix a tier binds, in the reader's words: guard, rule,
// addendum entry or check. It is "" for a tier that binds no fix.
func ArtifactNoun(t string) string {
	kind, _ := tier.Of(t)
	noun, _ := artifactNoun(kind)
	return noun
}

// artifactNoun names the fix a tier binds, in the reader's words, and the command
// prefix that removes one.
func artifactNoun(k tier.Kind) (noun, remove string) {
	switch k {
	case tier.Guard:
		return "guard", "steer remove "
	case tier.Instruction:
		return "rule", "steer rules remove "
	case tier.Review:
		return "addendum entry", "steer commit-gate addendum remove "
	case tier.Check:
		return "check", "steer check remove "
	}
	return "", ""
}

// ArtifactPhrase says where the fix a record binds stands now, "" for a record that
// binds none by design.
func ArtifactPhrase(t string, a *cadence.Artifact) string {
	kind, _ := tier.Of(t)
	noun, _ := artifactNoun(kind)
	if a == nil || noun == "" {
		return ""
	}
	name := noun + " " + a.ID
	phrase := artifactStatePhrase(kind, noun, name, a)
	if a.RemovalRecorded() {
		phrase += "; the archive records a removal of it at " + a.Removed +
			", after this record was registered, so check whether the " + noun +
			" was out of place during this record's window"
	}
	return phrase
}

func artifactStatePhrase(kind tier.Kind, noun, name string, a *cadence.Artifact) string {
	switch a.State {
	case cadence.ArtifactPresent:
		return "the " + name + " is in place as recorded"
	case cadence.ArtifactChanged:
		if kind == tier.Check {
			return "the " + name + " is declared, but its detector differs from the one this record registered"
		}
		return "the " + name + " is in place, but its text differs from the text this record measured"
	case cadence.ArtifactAbsent:
		switch {
		case kind == tier.Check:
			return "the " + name + " is no longer declared in " + a.Repo
		case kind == tier.Review && a.Scope == "project":
			return "the " + name + " is not in the project store at HEAD, so the commit gate does not read it"
		}
		return fmt.Sprintf("the %s is no longer in the %s store", name, a.Scope)
	case cadence.ArtifactUnreadable:
		return "where the " + name + " stands is not known: " + a.Detail
	case cadence.ArtifactUnbound:
		return "this record names no " + noun + ", so where it stands is not known"
	case cadence.ArtifactElsewhere:
		return "the " + name + " belongs to " + a.Repo + ", which this reading does not inspect"
	case cadence.ArtifactUnplaced:
		return "this record does not name the repository whose store holds the " + name +
			", so where it stands is not known"
	}
	return "the " + name + " is in the state " + a.State
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

// CheckActivityPhrase says how often a repository check ran in the observation
// window on this machine. It is empty for a fix that is not a repository check.
func CheckActivityPhrase(obs mine.Observation) string {
	c := obs.Checks
	switch {
	case !c.Applicable:
		return ""
	case c.Attribution != mine.AttributionCheckID:
		return "check runs cannot be read because this fix names no check"
	case c.Elsewhere():
		return fmt.Sprintf("check runs are recorded on %s, where this fix was registered, and not on this machine", c.RegisteredHost)
	case !c.StreamPresent:
		return "check runs are unknown because this machine holds no check evidence for the repository"
	}
	s := fmt.Sprintf("check runs on this machine: %d of %s in the observation window (%s)", c.Executions, c.CheckID, c.Scope)
	if c.Executions > 0 {
		s += fmt.Sprintf("; %s; %d stopped a turn", mine.OutcomeCounts(c.Outcomes), c.Blocked)
	}
	if c.ChangedDetector > 0 {
		s += fmt.Sprintf("; %d ran an edited check", c.ChangedDetector)
	}
	return s
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
