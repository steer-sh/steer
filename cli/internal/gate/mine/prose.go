package mine

import (
	"fmt"
	"io"
	"strings"
)

// The reader-facing keys of an intervention record. "problem" says what goes wrong,
// "intervention" says what this record does about it. "note" is the single free-text
// field that preceded them: it holds implementation detail on the records that carry
// one, so it is read back under its own label and is never promoted to either.
const (
	ProblemKey      = "problem"
	InterventionKey = "intervention"
	LegacyNoteKey   = "note"
)

// NotMechanizableKey is the argument an instruction record carries: why the class
// cannot be detected by a repository check or a pre-tool guard. Those are the two
// rungs above the instruction tier, and the argument has to rule out both. It is
// made in a session today and lost there, so no later reader can check it was
// made at all.
const NotMechanizableKey = "not_mechanizable"

// NotMechanizableFlag is the same field's flag spelling, exported so the CLI and
// the error text that names it cannot drift.
const NotMechanizableFlag = "not-mechanizable"

// NoInterventionFound is the one legitimate empty answer for a rejection's
// intervention. A required field that a blank satisfies is not required, so the case
// of "we looked and found nothing to install" has to be sayable in words.
const NoInterventionFound = "no suitable fix was identified"

// Prose is a record's reader-facing explanation.
type Prose struct {
	Problem      string
	Intervention string
	LegacyNote   string
}

// ProseOf reads a record's explanation back. A record written before the fields
// existed yields empty strings plus whatever its note held.
func ProseOf(record Record) Prose {
	return Prose{
		Problem:      str(record[ProblemKey]),
		Intervention: str(record[InterventionKey]),
		LegacyNote:   str(record[LegacyNoteKey]),
	}
}

// Validate refuses a blank explanation, and one that only repeats the class name.
// Register, Reject and Describe all call it, so every writer meets one contract.
func (p Prose) Validate(class string) error {
	hint := fmt.Sprintf("\n  A rejection that installed nothing says so: --%s %s",
		InterventionKey, pyRepr(NoInterventionFound))
	if err := ValidateArgument(ProblemKey, p.Problem, "what goes wrong", class, ""); err != nil {
		return err
	}
	return ValidateArgument(InterventionKey, p.Intervention,
		"what this record does about it", class, hint)
}

// ValidateArgument refuses a blank field and one that only restates the class name.
// A required field a blank satisfies is not required, and the class name is the
// filler a required field invites — it is also the one filler a machine can name.
// Presence is what this preserves; the argument's soundness stays the reader's.
func ValidateArgument(key, value, what, class, hint string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("--%s is required: one sentence naming %s, readable by "+
			"someone who was not in the session that found it%s", key, what, hint)
	}
	if strings.EqualFold(value, strings.TrimSpace(class)) {
		return fmt.Errorf("--%s repeats the class name %s. The class name is the "+
			"label; this field is the explanation the label cannot carry", key, pyRepr(class))
	}
	return nil
}

// writeInto stamps the explanation onto a record under construction.
func (p Prose) writeInto(record Record) {
	record[ProblemKey] = strings.TrimSpace(p.Problem)
	record[InterventionKey] = strings.TrimSpace(p.Intervention)
}

// DescribeOpts are the flags of `intervention describe`. An empty field leaves the
// record's current value in place, so one half can be corrected on its own.
type DescribeOpts struct {
	Problem      string
	Intervention string
}

// Describe rewrites a record's explanation and nothing else. Wording is not
// evidence: a clearer sentence must never move a baseline, an instrument or a
// window, or improving a description would restart the experiment it describes.
// This is the opposite of ScopeSet, which stamps scope_set_ts precisely because
// changing eligibility does invalidate the window it opened.
func Describe(w io.Writer, stateDir, id string, o DescribeOpts) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	rec := findRecord(records, id)
	if rec == nil {
		return fmt.Errorf("no intervention with id %s", pyRepr(id))
	}
	prose := ProseOf(rec)
	if strings.TrimSpace(o.Problem) != "" {
		prose.Problem = o.Problem
	}
	if strings.TrimSpace(o.Intervention) != "" {
		prose.Intervention = o.Intervention
	}
	if err := prose.Validate(str(rec["class"])); err != nil {
		return err
	}
	prose.writeInto(rec)
	if err := Save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(rec, 2))
	return nil
}
