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

// Field is one editable key of ~/.steer/config.json. Get and Set read and write the
// stored value; Effective reads what the resolved configuration would actually use,
// which is not the same value whenever an environment variable outranks the file.
type Field struct {
	Name string
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
}

// Fields is the whole editable surface, in the order an editor lists it.
var Fields = []Field{
	{
		Name: "reviewer.provider", Identity: true,
		Usage: "Reviewer CLI (agy|codex|claude-code); unset means agy",
		Get:   func(c config.Config) string { return c.Reviewer.Provider },
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
		Usage: "Reviewer model; unset means the provider's own default",
		Get:   func(c config.Config) string { return c.Reviewer.Model },
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
	},
	{
		Name: "reviewer.runs", Env: "COMMIT_GATE_RUNS", Numeric: true, Identity: true,
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
	},
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
// majorly drifted is one `steer intervention score` will withhold rather than a
// second opinion about it.
func Impact(cfg gate.Config) []mine.InstrumentImpact {
	records, err := mine.Load(cfg.StateDir)
	if err != nil || len(records) == 0 {
		return nil
	}
	ev := evidence.Gather(evidence.Options{GateStateDir: cfg.StateDir})
	fields, _ := mine.InstrumentFieldsFrom(cfg.InstrumentFields())
	return mine.ImpactOf(records, ev.Chains, mine.InstrumentUse{ID: cfg.InstrumentID(), Fields: fields})
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
