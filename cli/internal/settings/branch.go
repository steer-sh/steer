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
	DefaultBranchTimeout = 720 * time.Second
	// DefaultBranchEffort is a CLI branch reviewer's reasoning effort with none
	// configured, whatever the commit reviewer's is. An API reviewer has no such
	// default, since an endpoint may refuse reasoning_effort: it keeps the
	// inherited effort, and sends none when that is unset.
	DefaultBranchEffort = "high"

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
// environment, then flags, each applied as a layer over the one below. A layer
// that names a provider other than the one in force drops the model and effort
// beneath it, which name another provider's settings. A key no layer sets
// inherits base's value, except runs, timeout and a CLI reviewer's effort, which
// take the branch defaults.
func BranchConfig(base gate.Config, file, flags config.BranchReviewer) gate.Config {
	var env config.BranchReviewer
	envString(&env.Provider, BranchProviderEnv)
	envString(&env.Model, BranchModelEnv)
	envString(&env.Effort, BranchEffortEnv)
	envPositive(&env.Runs, BranchRunsEnv)
	envPositive(&env.Timeout, BranchTimeoutEnv)

	cfg := base
	var want config.BranchReviewer
	for _, layer := range []config.BranchReviewer{file, env, flags} {
		if provider := strings.TrimSpace(layer.Provider); provider != "" && !sameProvider(provider, cfg.ReviewerProvider) {
			cfg.ReviewerProvider = provider
			cfg.ReviewerModel, cfg.ReviewerModelOverride, cfg.ReviewerEffort = "", "", ""
			want.Effort = ""
		}
		if model := strings.TrimSpace(layer.Model); model != "" {
			cfg.ReviewerModel, cfg.ReviewerModelOverride = model, model
		}
		overlay(&want, layer)
	}
	if !isAPI(cfg.ReviewerProvider) {
		cfg.ReviewerEffort = DefaultBranchEffort
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

func isAPI(provider string) bool {
	p, ok := reviewers.FindProvider(provider)
	return ok && p.API
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

// sameProviderInherits returns an Inherits for a key that inherits name while
// neither the settings file nor the environment names a branch provider other
// than the commit reviewer's: a switch away drops the inherited value even when
// a higher layer switches back.
func sameProviderInherits(name string) func(config.Config) string {
	return func(file config.Config) string {
		commit := gate.WithEnv(gate.WithFile(gate.Defaults(), file)).ReviewerProvider
		for _, p := range []string{file.BranchReviewer.Provider, os.Getenv(BranchProviderEnv)} {
			if p = strings.TrimSpace(p); p != "" && !sameProvider(p, commit) {
				return ""
			}
		}
		return name
	}
}

// envDropsFile reports that BRANCH_REVIEW_PROVIDER names a provider other than
// the one the settings file puts in force, which discards the file's branch model
// and effort.
func envDropsFile(file config.Config) bool {
	env := strings.TrimSpace(os.Getenv(BranchProviderEnv))
	if env == "" {
		return false
	}
	inForce := gate.WithEnv(gate.WithFile(gate.Defaults(), file)).ReviewerProvider
	if p := strings.TrimSpace(file.BranchReviewer.Provider); p != "" {
		inForce = p
	}
	return !sameProvider(env, inForce)
}

// BranchProviderRefusal is the reason provider cannot review a branch, or nil when
// it can or no provider spells it. agy takes the prompt on its command line, which
// a branch diff overruns, and cannot search the repository.
func BranchProviderRefusal(provider string) error {
	p, ok := reviewers.FindProvider(provider)
	if !ok || branchCapable(p) {
		return nil
	}
	return fmt.Errorf("the %s reviewer cannot review a branch: it takes the prompt on its command line, "+
		"which a branch diff overruns, and it cannot search the repository; set branch_reviewer.provider "+
		"to one of %s", p.ID, strings.Join(branchProviderNames(), ", "))
}

// branchCapable reports whether p can review a branch.
func branchCapable(p reviewers.Provider) bool { return p.ID != "agy" }

// branchProviderNames lists the IDs of the providers that can review a branch.
func branchProviderNames() []string {
	out := []string{}
	for _, c := range branchProviderChoices() {
		out = append(out, c.Value)
	}
	return out
}

// branchProviderChoices lists the providers branch_reviewer.provider offers.
func branchProviderChoices() []Choice {
	out := []Choice{}
	for _, p := range reviewers.Providers() {
		if branchCapable(p) {
			out = append(out, Choice{Value: p.ID, Label: p.Label})
		}
	}
	return out
}

// BranchEffortWarning is the effort warning for the branch reviewer that file and
// the environment put in force, or "" when its effort is documented or unchecked.
func BranchEffortWarning(file config.Config) string {
	cfg := branchInForce(file)
	if w := reviewers.EffortWarning(cfg.ReviewerProvider, cfg.ReviewerEffort); w != "" {
		return "branch " + w
	}
	return ""
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
		Choices:      branchProviderChoices,
		Usage:        "Branch reviewer (" + strings.Join(branchProviderNames(), "|") + "); unset means reviewer.provider",
		Get:          func(c config.Config) string { return c.BranchReviewer.Provider },
		Set: func(c *config.Config, v string) error {
			if err := BranchProviderRefusal(v); err != nil {
				return err
			}
			return setProvider(&c.BranchReviewer.Provider, v, branchProviderNames())
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
		Dropped:  envDropsFile,
	},
	{
		Name: "branch_reviewer.effort", Env: BranchEffortEnv,
		Label:        "Branch reasoning effort",
		Help:         "How hard the branch reviewer thinks. Any value is passed through.",
		Kind:         KindText,
		DefaultLabel: DefaultBranchEffort + "; an API reviewer: reviewer.effort for the same provider",
		Usage:        "Branch reviewer reasoning effort; unset means " + DefaultBranchEffort + ", or for an API reviewer reviewer.effort when the provider matches",
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
		Inherits: func(file config.Config) string {
			if isAPI(branchInForce(file).ReviewerProvider) {
				return sameProviderInherits("reviewer.effort")(file)
			}
			return ""
		},
		Dropped: envDropsFile,
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
