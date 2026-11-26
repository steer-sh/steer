package settings

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
)

const (
	// DefaultBranchRuns is the panel size of a branch review with none configured.
	DefaultBranchRuns = 1
	// MaxBranchRuns is the largest panel a branch review may ask for.
	MaxBranchRuns = 5
	// DefaultBranchTimeout is a branch reviewer's wall clock with none configured.
	DefaultBranchTimeout = 600 * time.Second

	// The API reviewer's tool loop caps for a branch, which spans many more files
	// than a commit.
	branchToolCalls = 48
	branchToolTurns = 16
)

// The environment variables that outrank the branch_reviewer keys.
const (
	BranchProviderEnv = "BRANCH_REVIEW_PROVIDER"
	BranchModelEnv    = "BRANCH_REVIEW_MODEL"
	BranchEffortEnv   = "BRANCH_REVIEW_EFFORT"
	BranchRunsEnv     = "BRANCH_REVIEW_RUNS"
	BranchTimeoutEnv  = "BRANCH_REVIEW_TIMEOUT"
)

// BranchConfig derives the branch reviewer's configuration from the commit
// reviewer's, base, under the file's branch_reviewer keys, then the BRANCH_REVIEW_*
// environment, then flags. A key none of them sets inherits base's value, except
// runs and timeout, which take the branch defaults. A provider other than base's
// drops base's model and effort, which name another provider's settings.
func BranchConfig(base gate.Config, file, flags config.BranchReviewer) gate.Config {
	want := file
	envString(&want.Provider, BranchProviderEnv)
	envString(&want.Model, BranchModelEnv)
	envString(&want.Effort, BranchEffortEnv)
	envPositive(&want.Runs, BranchRunsEnv)
	envPositive(&want.Timeout, BranchTimeoutEnv)
	overlay(&want, flags)

	cfg := base
	if provider := strings.TrimSpace(want.Provider); provider != "" && !sameProvider(provider, base.ReviewerProvider) {
		cfg.ReviewerProvider = provider
		cfg.ReviewerModel, cfg.ReviewerModelOverride, cfg.ReviewerEffort = "", "", ""
	}
	if model := strings.TrimSpace(want.Model); model != "" {
		cfg.ReviewerModel, cfg.ReviewerModelOverride = model, model
	}
	if effort := strings.TrimSpace(want.Effort); effort != "" {
		cfg.ReviewerEffort = effort
	}
	cfg.ReviewerRuns = DefaultBranchRuns
	if want.Runs > 0 {
		cfg.ReviewerRuns = min(want.Runs, MaxBranchRuns)
	}
	cfg.ReviewTimeout = DefaultBranchTimeout
	if want.Timeout > 0 {
		cfg.ReviewTimeout = time.Duration(want.Timeout) * time.Second
	}
	cfg.ReviewerToolCalls, cfg.ReviewerToolTurns = branchToolCalls, branchToolTurns
	return cfg
}

// LoadBranch is BranchConfig over the operator's settings and environment. A
// settings file that cannot be read rides back as the error beside the defaults.
func LoadBranch(flags config.BranchReviewer) (gate.Config, error) {
	file, err := config.Load()
	if err != nil {
		file = config.Config{}
	}
	return BranchConfig(gate.WithEnv(gate.WithFile(gate.Defaults(), file)), file.BranchReviewer, flags), err
}

func sameProvider(a, b string) bool {
	pa, okA := reviewers.FindProvider(a)
	pb, okB := reviewers.FindProvider(b)
	if okA && okB {
		return pa.ID == pb.ID
	}
	return a == b
}

func envString(dst *string, key string) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		*dst = v
	}
}

func envPositive(dst *int, key string) {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		*dst = n
	}
}

// overlay copies every key top sets onto dst.
func overlay(dst *config.BranchReviewer, top config.BranchReviewer) {
	if top.Provider != "" {
		dst.Provider = top.Provider
	}
	if top.Model != "" {
		dst.Model = top.Model
	}
	if top.Effort != "" {
		dst.Effort = top.Effort
	}
	if top.Runs > 0 {
		dst.Runs = top.Runs
	}
	if top.Timeout > 0 {
		dst.Timeout = top.Timeout
	}
}

// branchInForce is the branch configuration the settings file and the environment
// put in force, for the keys' FileEffective.
func branchInForce(file config.Config) gate.Config {
	return BranchConfig(gate.WithEnv(gate.WithFile(gate.Defaults(), file)), file.BranchReviewer, config.BranchReviewer{})
}

// sameProviderInherits returns an Inherits for a key that inherits name while the
// branch reviewer runs the commit reviewer's provider.
func sameProviderInherits(name string) func(config.Config) string {
	return func(file config.Config) string {
		commit := gate.WithEnv(gate.WithFile(gate.Defaults(), file)).ReviewerProvider
		if sameProvider(branchInForce(file).ReviewerProvider, commit) {
			return name
		}
		return ""
	}
}

// BranchReviewerFields are the keys of the branch reviewer. The dashboard's
// Reviewer panel does not list them.
var BranchReviewerFields = []Field{
	{
		Name: "branch_reviewer.provider", Env: BranchProviderEnv,
		Label:        "Branch reviewer",
		Help:         "The CLI or API Steer runs to review a whole branch before a pull request.",
		Kind:         KindChoice,
		DefaultLabel: "the commit reviewer",
		Choices:      providerChoices,
		Usage:        "Branch reviewer (" + strings.Join(reviewers.ProviderNames(), "|") + "); unset means reviewer.provider",
		Get:          func(c config.Config) string { return c.BranchReviewer.Provider },
		Set: func(c *config.Config, v string) error {
			return setProvider(&c.BranchReviewer.Provider, v)
		},
		FileEffective: func(c config.Config) string { return branchInForce(c).ReviewerProvider },
		Inherits:      func(config.Config) string { return "reviewer.provider" },
	},
	{
		Name: "branch_reviewer.model", Env: BranchModelEnv,
		Label:        "Branch model",
		Help:         "Which model the branch reviewer uses.",
		Kind:         KindText,
		DefaultLabel: "reviewer.model for the same provider",
		Usage:        "Branch reviewer model; unset means reviewer.model when the provider matches, else its default",
		Get:          func(c config.Config) string { return c.BranchReviewer.Model },
		Set: func(c *config.Config, v string) error {
			c.BranchReviewer.Model = v
			return nil
		},
		FileEffective: func(c config.Config) string {
			cfg := branchInForce(c)
			if m := cfg.ReviewerModelOverride; m != "" {
				return m
			}
			p, ok := reviewers.FindProvider(cfg.ReviewerProvider)
			switch {
			case ok && gate.DefaultReviewerModels[p.ID] != "":
				return gate.DefaultReviewerModels[p.ID]
			case ok && p.API:
				return "(not set)"
			}
			return "(the CLI's own)"
		},
		Inherits: sameProviderInherits("reviewer.model"),
	},
	{
		Name: "branch_reviewer.effort", Env: BranchEffortEnv,
		Label:        "Branch reasoning effort",
		Help:         "How hard the branch reviewer thinks. Any value is passed through.",
		Kind:         KindText,
		DefaultLabel: "reviewer.effort for the same provider",
		Usage:        "Branch reviewer reasoning effort; unset means reviewer.effort when the provider matches",
		Get:          func(c config.Config) string { return c.BranchReviewer.Effort },
		Set: func(c *config.Config, v string) error {
			c.BranchReviewer.Effort = v
			return nil
		},
		FileEffective: func(c config.Config) string {
			if e := branchInForce(c).ReviewerEffort; e != "" {
				return e
			}
			return "(the provider's own)"
		},
		Inherits: sameProviderInherits("reviewer.effort"),
	},
	{
		Name: "branch_reviewer.runs", Env: BranchRunsEnv, Numeric: true,
		Label: "Branch review runs",
		Help:  "How many reviewers read the branch.",
		Kind:  KindNumber,
		Unit:  "run",
		Usage: fmt.Sprintf("Branch reviewers in the panel, 1 to %d; unset means %d", MaxBranchRuns, DefaultBranchRuns),
		Get:   func(c config.Config) string { return IntValue(c.BranchReviewer.Runs) },
		Set: func(c *config.Config, v string) error {
			n, err := positiveInt(v)
			if err != nil || n > MaxBranchRuns || (v != "" && n < 1) {
				return fmt.Errorf("branch_reviewer.runs takes a whole number from 1 to %d, got %q", MaxBranchRuns, v)
			}
			c.BranchReviewer.Runs = n
			return nil
		},
		FileEffective: func(c config.Config) string { return strconv.Itoa(branchInForce(c).ReviewerRuns) },
	},
	{
		Name: "branch_reviewer.timeout", Env: BranchTimeoutEnv, Numeric: true,
		Label: "Branch timeout per run",
		Help:  "How long one branch reviewer may take.",
		Kind:  KindNumber,
		Unit:  "second",
		Usage: fmt.Sprintf("Per-branch-reviewer wall clock, in seconds; unset means %d", int(DefaultBranchTimeout.Seconds())),
		Get:   func(c config.Config) string { return IntValue(c.BranchReviewer.Timeout) },
		Set: func(c *config.Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.BranchReviewer.Timeout = n
			return nil
		},
		FileEffective: func(c config.Config) string {
			return strconv.Itoa(int(branchInForce(c).ReviewTimeout.Seconds()))
		},
	},
}
