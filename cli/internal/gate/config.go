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
)

// Config holds every tunable the engine reads. Defaults() carries the values the
// Python hook shipped with; Load() applies environment overrides on top.
type Config struct {
	StateDir string

	ReviewerModel   string
	ReviewerCommand []string
	ReviewerRuns    int
	ReviewTimeout   time.Duration

	StaleAfter   time.Duration
	MaxFirings   int
	MinDiffChars int

	MaxDiffChars        int
	MaxContextFiles     int
	MaxContextChars     int
	MaxContextFileChars int

	MaxTaskChars   int
	MaxDetailChars int

	HostID string
}

const (
	defaultReviewerModel = "gemini-3.7-flash-high"
	defaultReviewerRuns  = 3

	// Per-reviewer wall clock. The reviewers run in parallel, so an adapter's own
	// timeout must stay above this.
	defaultReviewTimeout = 150 * time.Second
	defaultStaleAfter    = 30 * time.Minute

	// A chain re-fires while the retry diff changed and stops when it did not.
	// MaxFirings is a backstop against a chain that keeps changing the diff without
	// converging, not the expected stop.
	defaultMaxFirings = 4

	// Below this the round trip costs more than it returns.
	defaultMinDiffChars = 400

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
// patch release. Bump it only when gate behaviour that changes reviewer inputs changes.
const InstrumentSuffix = "go"

// Phases where no reviewer ran. They open no chain and close none, so a reader of
// the log must not treat one as a boundary. mine.py keeps its own copy; the two must
// stay in step.
var NonReviewPhases = map[string]bool{
	"skip-prose": true, "skip-empty": true, "skip-cleared": true, "ungated": true,
	"ungated-partial": true,
}

// Defaults returns the shipped configuration.
func Defaults() Config {
	return Config{
		StateDir:        defaultStateDir(),
		ReviewerModel:   defaultReviewerModel,
		ReviewerCommand: []string{"agy", "--model", defaultReviewerModel, "--output-format", "json", "-p"},
		ReviewerRuns:    defaultReviewerRuns,
		ReviewTimeout:   defaultReviewTimeout,

		StaleAfter:   defaultStaleAfter,
		MaxFirings:   defaultMaxFirings,
		MinDiffChars: defaultMinDiffChars,

		MaxDiffChars:        defaultMaxDiffChars,
		MaxContextFiles:     defaultMaxContextFiles,
		MaxContextChars:     defaultMaxContextChars,
		MaxContextFileChars: defaultMaxContextFileChars,

		MaxTaskChars:   defaultMaxTaskChars,
		MaxDetailChars: defaultMaxDetailChars,

		HostID: HostID(),
	}
}

// Load returns the shipped configuration with environment overrides applied.
func Load() Config {
	c := Defaults()
	// COMMIT_GATE_STATE_DIR wins over COMMIT_REVIEW_STATE. The second is mine.py's
	// own variable, so the miner and an existing mine.py invocation read one directory.
	if v := os.Getenv("COMMIT_GATE_STATE_DIR"); v != "" {
		c.StateDir = v
	} else if v := os.Getenv("COMMIT_REVIEW_STATE"); v != "" {
		c.StateDir = v
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
		c.ReviewerCommand = []string{"agy", "--model", v, "--output-format", "json", "-p"}
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

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude", "state", "commit-review")
	}
	return filepath.Join(home, ".claude", "state", "commit-review")
}

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

func (c Config) DiffDir() string   { return filepath.Join(c.StateDir, "diffs") }
func (c Config) TaskDir() string   { return filepath.Join(c.StateDir, "tasks") }
func (c Config) PrefixDir() string { return filepath.Join(c.StateDir, "prefixes") }
