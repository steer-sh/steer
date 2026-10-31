// Package gate is the harness-neutral commit-review engine: diff acquisition and
// budgeting, context selection, the reviewer panel, chain state and the ledger row.
// Adapters (cc, gitgate) render the Decision it returns; nothing here knows what a
// PreToolUse permissionDecision is.
package gate

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	steerconfig "github.com/steer-sh/steer-cli/internal/config"
	"github.com/steer-sh/steer-cli/internal/statestore"
)

// Config holds every tunable the engine reads. Defaults() carries the values the
// Python hook shipped with; Load() applies environment overrides on top.
type Config struct {
	StateDir string
	// LogLockWait bounds a shard writer's wait for the shard's lock; zero is the default.
	LogLockWait time.Duration

	ReviewerProvider      string
	ReviewerModel         string
	ReviewerModelOverride string
	ReviewerEffort        string
	ReviewerRuns          int
	ReviewTimeout         time.Duration

	// ReviewerAPIBaseURL and ReviewerAPIKeyEnv configure the openai-compatible
	// provider; every other provider ignores them.
	ReviewerAPIBaseURL string
	ReviewerAPIKeyEnv  string
	// ReviewerAPITools is the tool mode: off, auto or required; "" is off.
	ReviewerAPITools string
	// ReviewerEndpoint is the host an API reviewer sends to, which the instrument
	// records. It is the reviewer's own answer, "" for a CLI reviewer.
	ReviewerEndpoint string

	StaleAfter time.Duration
	MaxFirings int

	MaxDiffChars        int
	MaxContextFiles     int
	MaxContextChars     int
	MaxContextFileChars int

	MaxTaskChars   int
	MaxDetailChars int

	HostID string
}

const (
	defaultReviewerProvider = "claude-code"
	defaultReviewerRuns     = 3

	// Per-reviewer wall clock. The reviewers run in parallel, so an adapter's own
	// timeout must stay above this. A tool-using reviewer over a large diff takes
	// turns, and a reviewer killed mid-answer produces no review at all: the cost of
	// waiting is a slower commit, the cost of cutting it short is an ungated one.
	defaultReviewTimeout = 300 * time.Second
	defaultStaleAfter    = 30 * time.Minute

	// A chain re-fires while the retry diff changed and stops when it did not.
	// MaxFirings is a backstop against a chain that keeps changing the diff without
	// converging, not the expected stop.
	defaultMaxFirings = 4

	// Above this, whole file-diffs are dropped smallest-first until the rest fits;
	// only a commit whose every single file overruns the budget alone falls back to
	// stat-only.
	defaultMaxDiffChars = 180_000

	// Unchanged files that reference a symbol the diff changes, attached whole.
	defaultMaxContextFiles     = 8
	defaultMaxContextChars     = 30_000
	defaultMaxContextFileChars = 12_000

	defaultMaxTaskChars   = 300
	defaultMaxDetailChars = 300
)

// InstrumentSuffix marks rows produced by this implementation. Diff assembly and
// context selection are part of the instrument, so a Python-to-Go swap is a genuine
// instrument change even at an identical prompt. It carries no CLI version: mine.py
// flags any change as drift, and a per-release suffix would fire that flag on every
// patch release. Bump it only when gate behaviour changes reviewer inputs or the
// parser semantics that decide which reviewer findings enter the corpus.
const InstrumentSuffix = "go-findings-v4-shell"

// PhaseChainEnd is the row the engine writes when it releases a review sequence that
// blocked: from then on the agent owes that sequence's ledger.
const PhaseChainEnd = "chain-end"

// Phases where no reviewer ran. They open no chain and close none, so a reader of
// the log must not treat one as a boundary.
var NonReviewPhases = map[string]bool{
	"skip-prose": true, "skip-binary": true, "skip-empty": true, "skip-cleared": true, "ungated": true,
	"ungated-partial": true, PhaseChainEnd: true,
}

// DefaultReviewerModels is the model each provider reviews with when no model is
// configured. A provider absent here runs its CLI's own default.
var DefaultReviewerModels = map[string]string{
	"claude-code": "claude-haiku-5-5",
	"codex":       "gpt-6-luna",
}

// Defaults returns the shipped configuration.
func Defaults() Config {
	return Config{
		StateDir:         defaultStateDir(),
		ReviewerProvider: defaultReviewerProvider,
		ReviewerModel:    DefaultReviewerModels[defaultReviewerProvider],
		ReviewerRuns:     defaultReviewerRuns,
		ReviewTimeout:    defaultReviewTimeout,

		StaleAfter: defaultStaleAfter,
		MaxFirings: defaultMaxFirings,

		MaxDiffChars:        defaultMaxDiffChars,
		MaxContextFiles:     defaultMaxContextFiles,
		MaxContextChars:     defaultMaxContextChars,
		MaxContextFileChars: defaultMaxContextFileChars,

		MaxTaskChars:   defaultMaxTaskChars,
		MaxDetailChars: defaultMaxDetailChars,

		HostID: HostID(),
	}
}

// Load returns the shipped configuration with file and environment overrides applied.
// A settings file that cannot be read or parsed contributes no overrides and rides
// back as the error, beside a usable configuration: the caller reports it, because a
// gate that cannot answer must fail open.
func Load() (Config, error) {
	file, fileErr := steerconfig.Load()
	if fileErr != nil {
		file = steerconfig.Config{}
	}
	return WithEnv(WithFile(Defaults(), file)), fileErr
}

// WithFile applies the operator's settings file to a configuration. It is the middle
// step of the precedence chain — defaults, this, then the environment — and is
// exported so a caller previewing an edit reads the one chain rather than a copy of
// it.
func WithFile(c Config, file steerconfig.Config) Config {
	if file.Reviewer.Provider != "" {
		c.ReviewerProvider = file.Reviewer.Provider
	}
	if file.Reviewer.Model != "" {
		c.ReviewerModelOverride = file.Reviewer.Model
	}
	if file.Reviewer.Effort != "" {
		c.ReviewerEffort = file.Reviewer.Effort
	}
	if file.Reviewer.Runs > 0 {
		c.ReviewerRuns = file.Reviewer.Runs
	}
	if file.Reviewer.Timeout > 0 {
		c.ReviewTimeout = time.Duration(file.Reviewer.Timeout) * time.Second
	}
	if v := strings.TrimSpace(file.Reviewer.API.BaseURL); v != "" {
		c.ReviewerAPIBaseURL = v
	}
	if v := strings.TrimSpace(file.Reviewer.API.KeyEnv); v != "" {
		c.ReviewerAPIKeyEnv = v
	}
	if v := strings.TrimSpace(file.Reviewer.API.Tools); v != "" {
		c.ReviewerAPITools = v
	}
	return c
}

// WithEnv applies the environment overrides, which outrank the settings file.
func WithEnv(c Config) Config {
	// COMMIT_GATE_STATE_DIR wins over COMMIT_REVIEW_STATE. The second is mine.py's
	// own variable, so the miner and an existing mine.py invocation read one directory.
	if v := os.Getenv("COMMIT_GATE_STATE_DIR"); v != "" {
		c.StateDir = v
	} else if v := os.Getenv("COMMIT_REVIEW_STATE"); v != "" {
		c.StateDir = v
	}
	if v := strings.TrimSpace(os.Getenv("COMMIT_GATE_EFFORT")); v != "" {
		c.ReviewerEffort = v
	}
	if v := envInt("COMMIT_GATE_RUNS"); v > 0 {
		c.ReviewerRuns = v
	}
	if v := envInt("COMMIT_GATE_TIMEOUT"); v > 0 {
		c.ReviewTimeout = time.Duration(v) * time.Second
	}
	if v := envInt("COMMIT_GATE_MAX_FIRINGS"); v > 0 {
		c.MaxFirings = v
	}
	if v := os.Getenv("COMMIT_GATE_MODEL"); v != "" {
		c.ReviewerModel = v
		c.ReviewerModelOverride = v
	}
	return c
}

func envInt(key string) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return 0
	}
	return n
}

func defaultStateDir() string { return statestore.Resolve("commit-review") }

var hostSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// HostID names this machine for the ledger: lowercase, alphanumerics and dashes.
// Stamped on every row and used as the shard suffix, so a merged log can still be
// split back into the per-machine event streams mine.py segments into chains.
func HostID() string {
	raw := os.Getenv("COMMIT_REVIEW_HOST")
	if raw == "" {
		raw, _ = os.Hostname()
	}
	first, _, _ := strings.Cut(strings.ToLower(raw), ".")
	slug := strings.Trim(hostSlugRe.ReplaceAllString(first, "-"), "-")
	if slug == "" {
		return "unknown"
	}
	return slug
}

// LogFile is the per-machine ledger shard, so two hosts never append to the same
// file and any transport can merge the shards without conflict logic.
func (c Config) LogFile() string {
	return filepath.Join(c.StateDir, "adjudication."+c.HostID+".jsonl")
}

// HookTimeoutMargin is how far a commit-gate registration's harness timeout exceeds
// the reviewer timeout. A harness that stops the hook first lets the commit land
// unreviewed, with no message and no ledger row.
const HookTimeoutMargin = 60

// HookTimeout is the harness timeout, in seconds, a commit-gate registration needs.
func (c Config) HookTimeout() int { return int(c.ReviewTimeout.Seconds()) + HookTimeoutMargin }

func (c Config) DiffDir() string   { return filepath.Join(c.StateDir, "diffs") }
func (c Config) TaskDir() string   { return filepath.Join(c.StateDir, "tasks") }
func (c Config) PrefixDir() string { return filepath.Join(c.StateDir, "prefixes") }

// LedgerGrace is how long after the findings were handed to the agent a ledger may
// still arrive. The cc resolver waits it out before scanning; mine reads it to tell a
// chain whose adjudication is merely young from one whose adjudication never came.
const LedgerGrace = 120 // seconds
