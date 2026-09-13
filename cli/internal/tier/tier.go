// Package tier holds the intervention ladder's vocabulary once. Two spellings of
// the ladder are live and both reach a stored record: the CLI's original
// check|steer|prose|prompt and the later check|guard|instruction|review. Every
// reader — the flag validator, the display phrase and the guard-scope rule —
// reads this one table, so a record written under either spelling is understood
// wherever it is read.
package tier

import (
	"sort"
	"strings"
)

// Kind is one rung of the ladder, independent of which spelling named it.
type Kind string

const (
	// None is a record that installed nothing.
	None Kind = ""
	// Check is an executable test of repository content.
	Check Kind = "check"
	// Guard is a check on an agent's action before it runs.
	Guard Kind = "guard"
	// Instruction is written guidance for the coding agent.
	Instruction Kind = "instruction"
	// Review is a change to the review prompt.
	Review Kind = "review"
)

// spellings maps every accepted flag value to its rung.
var spellings = map[string]Kind{
	"check":       Check,
	"steer":       Guard,
	"guard":       Guard,
	"prose":       Instruction,
	"instruction": Instruction,
	"prompt":      Review,
	"review":      Review,
}

// names are the rungs in the reader's words. A tier is a type and never a
// behavior, so each name is a noun.
var names = map[Kind]string{
	Check:       "repository check",
	Guard:       "pre-tool guard",
	Instruction: "instruction rule",
	Review:      "review prompt",
}

// Of reads a stored or typed tier value. ok is false for a value no spelling
// names, which a display falls back on verbatim and a validator refuses.
func Of(value string) (Kind, bool) {
	k, ok := spellings[strings.ToLower(strings.TrimSpace(value))]
	return k, ok
}

// Valid reports whether a value names a rung. The empty value names none, so a
// caller that requires a tier checks for emptiness itself.
func Valid(value string) bool {
	_, ok := Of(value)
	return ok
}

// Accepted lists every spelling a flag takes, sorted, for help and error text.
func Accepted() []string {
	out := make([]string, 0, len(spellings))
	for s := range spellings {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Name is the rung in the reader's words, "" for a rung with no name.
func Name(k Kind) string { return names[k] }

// IsCheck says a record's fix is a repository check, which runs in one repository
// and under the harnesses whose Stop runner is registered.
func IsCheck(value string) bool {
	k, _ := Of(value)
	return k == Check
}

// IsGuard says a record's fix is a steer guard, whose enforcement is per harness.
func IsGuard(value string) bool {
	k, _ := Of(value)
	return k == Guard
}

// IsInstruction says a record's fix is written guidance, whose artifact is a rule
// in the shared store rather than a guard file.
func IsInstruction(value string) bool {
	k, _ := Of(value)
	return k == Instruction
}
