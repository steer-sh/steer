// Package settings owns the editable surface of ~/.steer/config.json: the one key
// table, the reviewer it resolves, and the measurements a reviewer edit lands in.
// The CLI's `steer config` and the dashboard's Reviewer panel both read this table,
// so a key can never exist in one editor only.
package settings

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
)

// ReviewerFlags are the flags that name the reviewer on a command line. A flag
// outranks ~/.steer/config.json, so one baked into a registration pins the reviewer
// that install named: every `steer config set` and every dashboard edit then changes
// nothing while `status` reports the new value. A registration carries none of them.
var ReviewerFlags = []string{"--provider", "--model", "--effort", "--runs"}

// The kinds of editor a key takes. Choice offers a closed set, Number takes a
// whole number in its own unit, and Text takes anything.
const (
	KindChoice = "choice"
	KindNumber = "number"
	KindText   = "text"
)

// Field is one editable key of ~/.steer/config.json. Get and Set read and write the
// stored value; Effective reads what the resolved configuration would actually use,
// which is not the same value whenever an environment variable outranks the file.
type Field struct {
	Name string
	// Label is the key's name in the reader's words, and Help is one sentence
	// saying what it does and what changing it costs.
	Label string
	Help  string
	// Kind is the editor this key takes; Unit names what a Number counts.
	Kind string
	Unit string
	// DefaultLabel is what an unset key means, in the reader's words.
	DefaultLabel string
	// Env is the environment variable that outranks this key, empty when none does.
	Env string
	// Numeric marks a key whose environment override counts only when it parses
	// above zero, the same test gate.WithEnv applies.
	Numeric bool
	// Identity marks a key the ledger stamps as part of the reviewer instrument, so
	// changing it drifts every measurement still collecting evidence.
	Identity bool
	Usage    string
	Get      func(config.Config) string
	Set      func(*config.Config, string) error
	// Effective renders the value the commit gate would run with.
	Effective func(gate.Config) string
	// Display renders that same value for a person: a name rather than an id, and
	// a duration rather than a count of seconds.
	Display func(gate.Config) string
	// Choices lists a KindChoice key's values, taken from the definition that owns
	// the set rather than from a copy of it.
	Choices func() []Choice
	// Suggestions are values that assist entry without limiting it. A key with
	// suggestions still accepts anything: a CLI can add a level before Steer knows
	// about it, and a list Steer refuses on would reject it on release day.
	Suggestions func(gate.Config) []string
}

// Choice is one value a KindChoice key offers: the value stored, and its name.
type Choice struct {
	Value string
	Label string
}

// Fields is the whole editable surface, in the order an editor lists it.
var Fields = []Field{
	{
		Name: "reviewer.provider", Identity: true,
		Label:        "Reviewer CLI",
		Help:         "The command Steer runs to review a change before it commits.",
		Kind:         KindChoice,
		DefaultLabel: "agy",
		Choices: func() []Choice {
			out := []Choice{}
			for _, p := range reviewers.Providers() {
				out = append(out, Choice{Value: p.ID, Label: p.Label})
			}
			return out
		},
		Display: func(c gate.Config) string { return reviewers.ProviderLabel(c.ReviewerProvider) },
		Usage:   "Reviewer CLI (agy|codex|claude-code); unset means agy",
		Get:     func(c config.Config) string { return c.Reviewer.Provider },
		Set: func(c *config.Config, v string) error {
			// The provider set is closed, so a typo is a write-time error rather than
			// a hook that fails open on every commit. reviewers.Resolve is the one
			// resolver, so this can never accept a spelling the gate rejects.
			if v != "" {
				if _, err := reviewers.Resolve(v, "", "", ""); err != nil {
					return err
				}
			}
			c.Reviewer.Provider = v
			return nil
		},
		Effective: func(c gate.Config) string { return c.ReviewerProvider },
	},
	{
		Name: "reviewer.model", Env: "COMMIT_GATE_MODEL", Identity: true,
		Label:        "Model",
		Help:         "Which model that CLI reviews with. Steer keeps no model list, so type any name the CLI accepts.",
		Kind:         KindText,
		DefaultLabel: "Provider default",
		Usage:        "Reviewer model; unset means the provider's own default",
		Get:          func(c config.Config) string { return c.Reviewer.Model },
		Set: func(c *config.Config, v string) error {
			// No model list. A list becomes a gate that refuses the new model on the
			// day it ships.
			c.Reviewer.Model = v
			return nil
		},
		Effective: func(c gate.Config) string { return c.ReviewerModel },
	},
	{
		Name: "reviewer.effort", Env: "COMMIT_GATE_EFFORT", Identity: true,
		Label: "Reasoning effort",
		Help: "How hard the reviewer thinks. Any value is passed through, so a level " +
			"the CLI added after this Steer shipped still works.",
		Kind:         KindText,
		DefaultLabel: "Provider default",
		Suggestions: func(c gate.Config) []string {
			p, ok := reviewers.FindProvider(c.ReviewerProvider)
			if !ok {
				return nil
			}
			return p.Efforts
		},
		Usage: "Reviewer reasoning effort; unset means the provider's own",
		Get:   func(c config.Config) string { return c.Reviewer.Effort },
		Set: func(c *config.Config, v string) error {
			c.Reviewer.Effort = v
			return nil
		},
		Effective: func(c gate.Config) string {
			if c.ReviewerEffort == "" {
				return "(the provider's own)"
			}
			return c.ReviewerEffort
		},
		Display: func(c gate.Config) string {
			if c.ReviewerEffort == "" {
				return "Provider default"
			}
			return c.ReviewerEffort
		},
	},
	{
		Name: "reviewer.runs", Env: "COMMIT_GATE_RUNS", Numeric: true, Identity: true,
		Label: "Review runs",
		Help: "How many reviewers read each change. Steer runs them all, so raising " +
			"this raises the work and the cost of every review.",
		Kind:  KindNumber,
		Unit:  "run",
		Usage: "Reviewers in the panel",
		Get:   func(c config.Config) string { return IntValue(c.Reviewer.Runs) },
		Set: func(c *config.Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.Reviewer.Runs = n
			return nil
		},
		Effective: func(c gate.Config) string { return strconv.Itoa(c.ReviewerRuns) },
	},
	{
		Name: "reviewer.timeout", Env: "COMMIT_GATE_TIMEOUT", Numeric: true,
		Label: "Timeout per run",
		Help: "How long one reviewer may take. Your coding agent keeps its own, " +
			"larger clock, which only a fresh install updates.",
		Kind:  KindNumber,
		Unit:  "second",
		Usage: "Per-reviewer wall clock, in seconds",
		Get:   func(c config.Config) string { return IntValue(c.Reviewer.Timeout) },
		Set: func(c *config.Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.Reviewer.Timeout = n
			return nil
		},
		Effective: func(c gate.Config) string {
			return strconv.Itoa(int(c.ReviewTimeout.Seconds()))
		},
		Display: func(c gate.Config) string {
			return Duration(int(c.ReviewTimeout.Seconds()))
		},
	},
}

// Duration renders a count of seconds the way a person reads a wall clock.
func Duration(seconds int) string {
	switch {
	case seconds <= 0:
		return "no limit"
	case seconds < 60:
		return fmt.Sprintf("%d %s", seconds, plural("second", seconds))
	case seconds%60 == 0:
		mins := seconds / 60
		return fmt.Sprintf("%d %s", mins, plural("minute", mins))
	}
	return fmt.Sprintf("%d minutes %d seconds", seconds/60, seconds%60)
}

// LabelOf is a key's name in the reader's words, falling back on the stored key
// so an editor can never render a blank row.
func LabelOf(f Field) string {
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

// DisplayValue is the value in force, in the reader's words.
func DisplayValue(f Field, cfg gate.Config) string {
	if f.Display != nil {
		return f.Display(cfg)
	}
	value := f.Effective(cfg)
	if value == "" && f.DefaultLabel != "" {
		return f.DefaultLabel
	}
	return value
}

// StoredValue is what the file itself holds, which an override may not be in
// force. It is the empty string when the key is unset.
func StoredValue(f Field, file config.Config) string { return f.Get(file) }

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// Find names the key, refusing an unknown one by listing the surface.
func Find(name string) (Field, error) {
	for _, f := range Fields {
		if f.Name == name {
			return f, nil
		}
	}
	return Field{}, fmt.Errorf("unknown setting %q: expected one of %s",
		name, strings.Join(Names(), ", "))
}

// Names lists every key.
func Names() []string {
	names := make([]string, 0, len(Fields))
	for _, f := range Fields {
		names = append(names, f.Name)
	}
	return names
}

// KeyHelp renders the key documentation an editor's help prints.
func KeyHelp() string {
	var b strings.Builder
	for _, f := range Fields {
		fmt.Fprintf(&b, "  %-18s %s\n", f.Name, f.Usage)
	}
	return strings.TrimRight(b.String(), "\n")
}

// IntValue renders a stored integer, and an unset one as the empty string: zero is
// how the file spells "no override", not a value.
func IntValue(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// positiveInt parses a stored integer. An empty value clears the key, which is why 0
// is accepted where a negative number is not.
func positiveInt(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("expected a whole number of seconds or runs, got %q", v)
	}
	return n, nil
}

// Source names which layer of the precedence chain supplies the effective value.
func Source(f Field, file config.Config) string {
	if f.Env != "" {
		v := strings.TrimSpace(os.Getenv(f.Env))
		if v != "" && (!f.Numeric || positiveEnv(v)) {
			return "env " + f.Env
		}
	}
	if f.Get(file) != "" {
		return "config"
	}
	return "default"
}

func positiveEnv(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n > 0
}

// StoredLabel names a written value, and an empty one as the default it returns to.
func StoredLabel(value string) string {
	if value == "" {
		return "unset (the shipped default)"
	}
	return value
}

// BuildReviewer resolves the configured provider, warning through warn when the
// effort is one the provider does not document. The value passes through either way.
func BuildReviewer(cfg gate.Config, warn func(string)) (gate.Reviewer, error) {
	if warning := reviewers.EffortWarning(cfg.ReviewerProvider, cfg.ReviewerEffort); warning != "" && warn != nil {
		warn(warning)
	}
	return reviewers.Resolve(cfg.ReviewerProvider, cfg.ReviewerModelOverride, cfg.ReviewerEffort, cfg.ReviewerModel)
}

// Resolve returns the configuration with ReviewerModel naming the model that would
// ACTUALLY run, beside the reviewer it resolved. An unresolvable provider yields the
// error, no reviewer, and the operator's own configured model where that label would
// have gone.
func Resolve(cfg gate.Config, warn func(string)) (gate.Config, gate.Reviewer, error) {
	reviewer, err := BuildReviewer(cfg, warn)
	if err != nil {
		if model := strings.TrimSpace(cfg.ReviewerModelOverride); model != "" {
			cfg.ReviewerModel = model
		}
		return cfg, nil, err
	}
	cfg.ReviewerModel = reviewer.ModelLabel()
	return cfg, reviewer, nil
}

// Preview resolves the configuration an unwritten settings edit would run under, over
// the one precedence chain rather than a copy of it.
func Preview(next config.Config, warn func(string)) (gate.Config, error) {
	cfg, _, err := Resolve(gate.WithEnv(gate.WithFile(gate.Defaults(), next)), warn)
	return cfg, err
}

// Impact names the measurements a pending reviewer identity lands in. It grades the
// prospective identity through the scorer's own computation, so a class named here as
// majorly drifted is one `steer fix score` will withhold rather than a
// second opinion about it.
func Impact(cfg gate.Config) []mine.InstrumentImpact {
	records, err := mine.Load(cfg.StateDir)
	if err != nil || len(records) == 0 {
		return nil
	}
	ev := evidence.Gather(evidence.Options{GateStateDir: cfg.StateDir})
	fields, _ := mine.InstrumentFieldsFrom(cfg.InstrumentFields())
	// A settings edit has no repository, so each record keeps its own review addendum.
	fields.AddendumKnown = false
	return mine.ImpactOf(records, ev.Chains, mine.InstrumentUse{ID: cfg.InstrumentID(), Fields: fields})
}

// ImpactWidth is the class column an impact list renders over: the widest class in
// the list, so a long class name pushes the column rather than colliding with it.
func ImpactWidth(impacts []mine.InstrumentImpact) int {
	width := 0
	for _, im := range impacts {
		if n := len([]rune(im.Class)); n > width {
			width = n
		}
	}
	return width
}

// ImpactNote says what a grade costs the reader: a major drift withholds the verdict,
// a minor one rides beside the range.
func ImpactNote(im mine.InstrumentImpact) string {
	consequence := "reported beside the range"
	if im.Grade == mine.DriftMajor {
		consequence = "the verdict is withheld while the windows disagree"
	}
	if len(im.Dims) == 0 {
		return consequence
	}
	return strings.Join(im.Dims, ", ") + " — " + consequence
}
