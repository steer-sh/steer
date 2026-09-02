package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

// configField is one editable key of ~/.steer/config.json. get and set read and write
// the stored value; effective reads what the resolved configuration would actually
// use, which is not the same value whenever an environment variable outranks the file.
type configField struct {
	name string
	// env is the environment variable that outranks this key, empty when none does.
	env string
	// numeric marks a key whose environment override counts only when it parses
	// above zero, the same test gate.WithEnv applies.
	numeric bool
	// identity marks a key the ledger stamps as part of the reviewer instrument, so
	// changing it drifts every measurement still collecting evidence.
	identity bool
	usage    string
	get      func(config.Config) string
	set      func(*config.Config, string) error
	// effective renders the value the commit gate would run with.
	effective func(gate.Config) string
}

var configFields = []configField{
	{
		name: "reviewer.provider", identity: true,
		usage: "Reviewer CLI (agy|codex|claude-code); unset means agy",
		get:   func(c config.Config) string { return c.Reviewer.Provider },
		set: func(c *config.Config, v string) error {
			// The provider set is closed, so a typo is a write-time error rather than
			// a hook that fails open on every commit. buildReviewer is the one
			// resolver, so this can never accept a spelling the gate rejects.
			if v != "" {
				if _, err := buildReviewer(gate.Config{ReviewerProvider: v}); err != nil {
					return err
				}
			}
			c.Reviewer.Provider = v
			return nil
		},
		effective: func(c gate.Config) string { return c.ReviewerProvider },
	},
	{
		name: "reviewer.model", env: "COMMIT_GATE_MODEL", identity: true,
		usage: "Reviewer model; unset means the provider's own default",
		get:   func(c config.Config) string { return c.Reviewer.Model },
		set: func(c *config.Config, v string) error {
			// No model list. A list becomes a gate that refuses the new model on the
			// day it ships.
			c.Reviewer.Model = v
			return nil
		},
		effective: func(c gate.Config) string { return c.ReviewerModel },
	},
	{
		name: "reviewer.effort", env: "COMMIT_GATE_EFFORT", identity: true,
		usage: "Reviewer reasoning effort; unset means the provider's own",
		get:   func(c config.Config) string { return c.Reviewer.Effort },
		set: func(c *config.Config, v string) error {
			c.Reviewer.Effort = v
			return nil
		},
		effective: func(c gate.Config) string {
			if c.ReviewerEffort == "" {
				return "(the provider's own)"
			}
			return c.ReviewerEffort
		},
	},
	{
		name: "reviewer.runs", env: "COMMIT_GATE_RUNS", numeric: true, identity: true,
		usage: "Reviewers in the panel",
		get:   func(c config.Config) string { return intValue(c.Reviewer.Runs) },
		set: func(c *config.Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.Reviewer.Runs = n
			return nil
		},
		effective: func(c gate.Config) string { return strconv.Itoa(c.ReviewerRuns) },
	},
	{
		name: "reviewer.timeout", env: "COMMIT_GATE_TIMEOUT", numeric: true,
		usage: "Per-reviewer wall clock, in seconds",
		get:   func(c config.Config) string { return intValue(c.Reviewer.Timeout) },
		set: func(c *config.Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.Reviewer.Timeout = n
			return nil
		},
		effective: func(c gate.Config) string {
			return strconv.Itoa(int(c.ReviewTimeout.Seconds()))
		},
	},
}

// findConfigField names the key, refusing an unknown one by listing the surface.
func findConfigField(name string) (configField, error) {
	for _, f := range configFields {
		if f.name == name {
			return f, nil
		}
	}
	return configField{}, fmt.Errorf("unknown setting %q: expected one of %s",
		name, strings.Join(configFieldNames(), ", "))
}

func configFieldNames() []string {
	names := make([]string, 0, len(configFields))
	for _, f := range configFields {
		names = append(names, f.name)
	}
	return names
}

// intValue renders a stored integer, and an unset one as the empty string: zero is
// how the file spells "no override", not a value.
func intValue(n int) string {
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

// fieldSource names which layer of the precedence chain supplies the effective value.
func fieldSource(f configField, file config.Config) string {
	if f.env != "" {
		v := strings.TrimSpace(os.Getenv(f.env))
		if v != "" && (!f.numeric || positiveEnv(v)) {
			return "env " + f.env
		}
	}
	if f.get(file) != "" {
		return "config"
	}
	return "default"
}

func positiveEnv(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n > 0
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write ~/.steer/config.json",
	Long: `config reads and writes Steer's harness-neutral operator settings.

The file is global: one reviewer configuration for every repository and every
harness. Values resolve in this order — the shipped defaults, this file, the
environment, then a command's own flags — so an environment variable or a flag
outranks anything set here.`,
	Example: `  steer config show
  steer config get reviewer.effort
  steer config set reviewer.provider codex
  steer config set reviewer.model ""`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show every setting, the value in force, and where it comes from",
	RunE: func(cmd *cobra.Command, args []string) error {
		file, err := config.Load()
		if err != nil {
			return err
		}
		cfg, _, resolveErr := resolvedGateConfig()

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Steer Settings"))
		fmt.Println()
		fmt.Printf("  File  %s%s\n", steerpath.ConfigFile(), configFileNote())
		fmt.Println()
		for _, f := range configFields {
			fmt.Printf("  %-18s %-24s %s\n", f.name, f.effective(cfg), styles.MutedText.Render(fieldSource(f, file)))
		}
		fmt.Println()
		if resolveErr != nil {
			utils.Warning(resolveErr.Error())
		}
		return nil
	},
}

func configFileNote() string {
	if _, err := os.Stat(steerpath.ConfigFile()); os.IsNotExist(err) {
		return styles.MutedText.Render("  (not written yet — every value is the shipped default)")
	}
	return ""
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one stored value",
	Long: `get prints the value the settings file stores, and nothing when the key is
unset. It is the file's own value, not the value in force: run "steer config show"
to see what an environment variable overrides.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := findConfigField(args[0])
		if err != nil {
			return err
		}
		file, err := config.Load()
		if err != nil {
			return err
		}
		fmt.Println(f.get(file))
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Write one value, naming the measurements the change lands in",
	Long: `set writes one key to ~/.steer/config.json. An empty value clears the key,
which returns it to the shipped default.

A change to the reviewer's identity — its provider, model, effort or run count —
changes the instrument the ledger stamps. Every class still collecting evidence is
then measured across two instruments, so set names those classes before it writes.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := findConfigField(args[0])
		if err != nil {
			return err
		}
		value := strings.TrimSpace(args[1])
		file, err := config.Load()
		if err != nil {
			return err
		}
		next := file
		if err := f.set(&next, value); err != nil {
			return err
		}
		if next == file {
			utils.Info(fmt.Sprintf("%s is already %s.", f.name, storedLabel(value)))
			return nil
		}

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Steer Settings"))
		fmt.Println()
		// An effort no provider documents is warned about by buildReviewer, which
		// reportReviewerImpact runs; warning here too would print it twice.
		if f.identity {
			reportReviewerImpact(next)
		}
		if err := config.Save(next); err != nil {
			return err
		}
		utils.Success(fmt.Sprintf("%s = %s", f.name, storedLabel(value)))
		if f.name == "reviewer.timeout" {
			// The engine reads the new value on the next firing; the harness's own
			// hook timeout is a registered number, and a review killed by the smaller
			// wall clock lands the commit ungated with no row at all.
			utils.Info("The harness hook's wall clock is this timeout plus a margin, and it is " +
				"written at registration time. Re-run `steer commit-gate install` for the new " +
				"timeout to take full effect.")
		}
		if source := fieldSource(f, next); strings.HasPrefix(source, "env ") {
			utils.Warning(fmt.Sprintf("%s is set and outranks the settings file, so this "+
				"value is not in force here.", strings.TrimPrefix(source, "env ")))
		}
		return nil
	},
}

func configKeyHelp() string {
	var b strings.Builder
	for _, f := range configFields {
		fmt.Fprintf(&b, "  %-18s %s\n", f.name, f.usage)
	}
	return strings.TrimRight(b.String(), "\n")
}

func storedLabel(value string) string {
	if value == "" {
		return "unset (the shipped default)"
	}
	return value
}

// reportReviewerImpact names the classes whose measurement the pending reviewer change
// lands in. It grades the prospective identity through the scorer's own computation,
// so a class named here as majorly drifted is one `steer intervention score` will
// withhold rather than a second opinion about it.
func reportReviewerImpact(next config.Config) {
	cfg, _, err := resolveReviewer(gate.WithEnv(gate.WithFile(gate.Defaults(), next)))
	if err != nil {
		utils.Warning(err.Error())
		return
	}
	records, err := mine.Load(cfg.StateDir)
	if err != nil || len(records) == 0 {
		return
	}
	ev := evidence.Gather(evidence.Options{GateStateDir: cfg.StateDir})
	fields, _ := mine.InstrumentFieldsFrom(cfg.InstrumentFields())
	impacts := mine.ImpactOf(records, ev.Chains, mine.InstrumentUse{ID: cfg.InstrumentID(), Fields: fields})
	if len(impacts) == 0 {
		return
	}
	utils.Warning(fmt.Sprintf("This reviewer change lands in %d measurement(s) already collecting:",
		len(impacts)))
	for _, im := range impacts {
		fmt.Printf("    %-6s %-28s %s\n", im.Grade, im.Class, impactNote(im))
	}
	fmt.Println()
}

// impactNote says what the grade costs the reader: a major drift withholds the
// verdict, a minor one rides beside the range.
func impactNote(im mine.InstrumentImpact) string {
	consequence := "reported beside the range"
	if im.Grade == mine.DriftMajor {
		consequence = "the verdict is withheld while the windows disagree"
	}
	if len(im.Dims) == 0 {
		return styles.MutedText.Render(consequence)
	}
	return styles.MutedText.Render(strings.Join(im.Dims, ", ") + " — " + consequence)
}

func init() {
	// The key documentation is generated from the one table, so a key added there
	// can never be missing from the command's help.
	configCmd.Long += "\n\nSettings:\n" + configKeyHelp()
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
}
