package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/sessions"
)

// The intervention commands exist under two parents: `steer intervention`, the
// corpus-neutral name, and `steer commit-gate mine`, the review-findings alias every
// existing workflow types. One factory builds both trees, so the two can never drift;
// each tree owns its own flag storage, because cobra binds a flag to one variable and
// two trees registered against one variable would share a value across invocations.
type interventionFlags struct {
	ledger string

	findingsKind         string
	findingsSince        float64
	findingsStrictJoins  bool
	findingsIncludeEmpty bool
	findingsJSON         bool

	matchKind        string
	matchStrictJoins bool
	matchCorpus      string
	matchSignalKind  string

	registerClass       string
	registerMatch       string
	registerKind        string
	registerCorpus      string
	registerSignalKind  string
	registerTier        string
	registerTarget      string
	registerHookID      string
	registerHookScope   string
	registerNote        string
	registerProblem     string
	registerIntervent   string
	registerSupersedes  string
	registerInstalledTS int64
	registerForce       bool
	registerScopeAll    bool
	registerScopeHrn    []string
	registerScopeRepo   string
	registerScopePath   string

	rejectClass      string
	rejectMatch      string
	rejectKind       string
	rejectCorpus     string
	rejectSignalKind string
	rejectReason     string
	rejectReopen     string
	rejectNote       string
	rejectProblem    string
	rejectIntervent  string
	rejectRejectedTS int64
	rejectForce      bool
	rejectScopeAll   bool
	rejectScopeHrn   []string
	rejectScopeRepo  string
	rejectScopePath  string

	scopeAll   bool
	scopeHrn   []string
	scopeRepo  string
	scopePath  string
	scopeSetTS int64
	scopeForce bool

	scoreID       string
	scoreAll      bool
	scoreMinAfter int
	scoreJSON     bool

	listJSON bool

	describeProblem   string
	describeIntervent string
	retireReason      string
	retireStatus      string
}

const scopeHarnessUsage = "eligibility scope: harness ids the class is measured on (repeatable or comma-separated)"

// The prose flags are required because a class name is a label, not an explanation.
// A reader arriving at a record months later has neither the evidence nor the session
// that produced it, so the record carries both halves in its own words.
const problemUsage = "one sentence naming what goes wrong, readable without the session that found it (required)"

const interventionUsage = "one sentence naming what this intervention does about it (required)"

const rejectInterventionUsage = "one sentence naming the intervention considered, or that none was found (required)"

const corpusUsage = "evidence corpus the class is counted over: review-findings (by chain) or transcript-friction (by session)"

const signalKindUsage = "transcript-friction only: the classifier kind the class is counted over " +
	"(any|user_corrections|user_rejections|tool_failures|hook_blocks)"

// storedSessions reads the transcript-session snapshot store. A machine that never
// refreshed it has none, and a session-corpus write then refuses for want of a
// baseline rather than freezing an empty one.
func storedSessions() []mine.SessionUnit {
	units, _ := sessions.Load(sessions.StateDir())
	return units
}

// interventionCommands builds one tree's subcommands. withFindings adds `findings`,
// which reads the review corpus rather than managing an intervention, so it belongs to
// the commit-gate tree alone.
func newInterventionCommands(withFindings bool) (*interventionFlags, []*cobra.Command) {
	v := &interventionFlags{}
	out := func() io.Writer { return os.Stdout }

	match := &cobra.Command{
		Use:   "match REGEX",
		Short: "Test a candidate class regex against the corpus",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mine.Match(out(), gateConfig().StateDir, mine.MatchOpts{
				Ledger:      v.ledger,
				Regex:       args[0],
				Kind:        v.matchKind,
				StrictJoins: v.matchStrictJoins,
				Corpus:      v.matchCorpus,
				SignalKind:  v.matchSignalKind,
				Sessions:    storedSessions(),
			})
		},
	}
	m := match.Flags()
	m.StringVar(&v.matchKind, "kind", "accepted", "accepted|rejected|both")
	m.BoolVar(&v.matchStrictJoins, "strict-joins", false, "round-joined rows only")
	m.StringVar(&v.matchCorpus, "corpus", mine.CorpusReviewFindings, corpusUsage)
	m.StringVar(&v.matchSignalKind, "signal-kind", "", signalKindUsage)

	register := &cobra.Command{
		Use:   "register",
		Short: "Record an intervention with its baseline incidence",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := guardscope.Check(v.registerTier, v.registerScopeAll,
				v.registerScopeHrn, v.registerForce); err != nil {
				return err
			}
			if err := prepareState(); err != nil {
				return err
			}
			return mine.Register(out(), gateConfig().StateDir, mine.RegisterOpts{
				Ledger:     v.ledger,
				Class:      v.registerClass,
				Match:      v.registerMatch,
				Kind:       v.registerKind,
				Corpus:     v.registerCorpus,
				SignalKind: v.registerSignalKind,
				Sessions:   storedSessions(),
				Tier:       v.registerTier,
				Target:     v.registerTarget,
				Note:       v.registerNote,
				Prose: mine.Prose{
					Problem:      v.registerProblem,
					Intervention: v.registerIntervent,
				},
				HookID:       v.registerHookID,
				HookScope:    v.registerHookScope,
				Supersedes:   v.registerSupersedes,
				InstalledTS:  v.registerInstalledTS,
				Force:        v.registerForce,
				ScopeAll:     v.registerScopeAll,
				ScopeHarness: v.registerScopeHrn,
				ScopeRepo:    v.registerScopeRepo,
				ScopePath:    v.registerScopePath,
			})
		},
	}
	r := register.Flags()
	r.StringVar(&v.registerClass, "class", "", "class name (required)")
	r.StringVar(&v.registerMatch, "match", "", "regex defining class membership (required)")
	r.StringVar(&v.registerKind, "kind", "defect", "defect|false-positive")
	r.StringVar(&v.registerCorpus, "corpus", mine.CorpusReviewFindings, corpusUsage)
	r.StringVar(&v.registerSignalKind, "signal-kind", "", signalKindUsage)
	r.StringVar(&v.registerTier, "tier", "", "check|steer|prose|prompt (required)")
	r.StringVar(&v.registerTarget, "target", "", "file the fix landed in (required)")
	r.StringVar(&v.registerHookID, "hook-id", "",
		"steer-manifest key, matching the audit log's hook_name; required for --tier steer")
	r.StringVar(&v.registerHookScope, "hook-scope", "",
		"guard store owning --hook-id: project|global (default: whichever holds it, refusing both)")
	r.StringVar(&v.registerNote, "note", "", "free-text note")
	r.StringVar(&v.registerProblem, "problem", "", problemUsage)
	r.StringVar(&v.registerIntervent, "intervention", "", interventionUsage)
	r.StringVar(&v.registerSupersedes, "supersedes", "", "id of a record this replaces")
	r.Int64Var(&v.registerInstalledTS, "installed-ts", 0, "install timestamp (default now)")
	r.BoolVar(&v.registerForce, "force", false, "record despite a zero baseline or a blocking rejection")
	r.BoolVar(&v.registerScopeAll, "scope-all", false, "eligibility scope: every unit (exclusive with the scope predicates)")
	r.StringSliceVar(&v.registerScopeHrn, "scope-harness", nil, scopeHarnessUsage)
	r.StringVar(&v.registerScopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	r.StringVar(&v.registerScopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	for _, name := range []string{"class", "match", "tier", "target", "problem", "intervention"} {
		_ = register.MarkFlagRequired(name)
	}

	reject := &cobra.Command{
		Use:   "reject",
		Short: "Record a class examined and refused",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := prepareState(); err != nil {
				return err
			}
			return mine.Reject(out(), gateConfig().StateDir, mine.RejectOpts{
				Ledger:     v.ledger,
				Class:      v.rejectClass,
				Match:      v.rejectMatch,
				Kind:       v.rejectKind,
				Corpus:     v.rejectCorpus,
				SignalKind: v.rejectSignalKind,
				Sessions:   storedSessions(),
				Reason:     v.rejectReason,
				Reopen:     v.rejectReopen,
				Note:       v.rejectNote,
				Prose: mine.Prose{
					Problem:      v.rejectProblem,
					Intervention: v.rejectIntervent,
				},
				RejectedTS:   v.rejectRejectedTS,
				Force:        v.rejectForce,
				ScopeAll:     v.rejectScopeAll,
				ScopeHarness: v.rejectScopeHrn,
				ScopeRepo:    v.rejectScopeRepo,
				ScopePath:    v.rejectScopePath,
			})
		},
	}
	rj := reject.Flags()
	rj.StringVar(&v.rejectClass, "class", "", "class name (required)")
	rj.StringVar(&v.rejectMatch, "match", "", "regex defining class membership (required)")
	rj.StringVar(&v.rejectKind, "kind", "defect", "defect|false-positive")
	rj.StringVar(&v.rejectCorpus, "corpus", mine.CorpusReviewFindings, corpusUsage)
	rj.StringVar(&v.rejectSignalKind, "signal-kind", "", signalKindUsage)
	rj.StringVar(&v.rejectReason, "reason", "", "why no gate was installed (required)")
	rj.StringVar(&v.rejectReopen, "reopen", "", "the observation that would make this worth reconsidering (required)")
	rj.StringVar(&v.rejectNote, "note", "", "free-text note")
	rj.StringVar(&v.rejectProblem, "problem", "", problemUsage)
	rj.StringVar(&v.rejectIntervent, "intervention", "", rejectInterventionUsage)
	rj.Int64Var(&v.rejectRejectedTS, "rejected-ts", 0, "rejection timestamp (default now)")
	rj.BoolVar(&v.rejectForce, "force", false, "record despite a zero baseline")
	rj.BoolVar(&v.rejectScopeAll, "scope-all", false, "eligibility scope: every unit (exclusive with the scope predicates)")
	rj.StringSliceVar(&v.rejectScopeHrn, "scope-harness", nil, scopeHarnessUsage)
	rj.StringVar(&v.rejectScopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	rj.StringVar(&v.rejectScopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	for _, name := range []string{"class", "match", "reason", "reopen", "problem", "intervention"} {
		_ = reject.MarkFlagRequired(name)
	}

	scope := &cobra.Command{
		Use:   "scope ID",
		Short: "Install a frozen scope on a legacy, unscoped intervention",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A legacy record names its tier already, so the scope it is about to
			// be given answers to the same rule a fresh registration does.
			tier, err := recordTier(args[0])
			if err != nil {
				return err
			}
			if err := guardscope.Check(tier, v.scopeAll, v.scopeHrn, v.scopeForce); err != nil {
				return err
			}
			if err := prepareState(); err != nil {
				return err
			}
			return mine.ScopeSet(out(), gateConfig().StateDir, args[0], mine.ScopeSetOpts{
				Ledger:       v.ledger,
				Sessions:     storedSessions(),
				ScopeAll:     v.scopeAll,
				ScopeHarness: v.scopeHrn,
				ScopeRepo:    v.scopeRepo,
				ScopePath:    v.scopePath,
				ScopeSetTS:   v.scopeSetTS,
				Force:        v.scopeForce,
			})
		},
	}
	sc := scope.Flags()
	sc.BoolVar(&v.scopeAll, "scope-all", false, "eligibility scope: every unit (exclusive with the scope predicates)")
	sc.StringSliceVar(&v.scopeHrn, "scope-harness", nil, scopeHarnessUsage)
	sc.StringVar(&v.scopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	sc.StringVar(&v.scopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	sc.Int64Var(&v.scopeSetTS, "scope-set-ts", 0, "migration timestamp opening the after-window (default now)")
	sc.BoolVar(&v.scopeForce, "force", false, "record despite a zero scoped baseline")

	score := &cobra.Command{
		Use:   "score",
		Short: "Recompute before/after incidence and verdict",
		RunE: func(cmd *cobra.Command, args []string) error {
			ev := evidence.Gather(evidence.Options{
				GateStateDir: gateConfig().StateDir,
				Ledger:       v.ledger,
			})
			return mine.Score(out(), gateConfig().StateDir, mine.ScoreOpts{
				Ledger:       v.ledger,
				ID:           v.scoreID,
				All:          v.scoreAll,
				MinAfter:     v.scoreMinAfter,
				JSON:         v.scoreJSON,
				Sessions:     ev.Sessions,
				Firings:      ev.Firings,
				AuditPresent: ev.AuditPresent,
			})
		},
	}
	s := score.Flags()
	s.StringVar(&v.scoreID, "id", "", "score only this intervention")
	s.BoolVar(&v.scoreAll, "all", false, "include reworked and removed interventions")
	s.IntVar(&v.scoreMinAfter, "min-after", 0, "extra floor: withhold a verdict below this many eligible after-units")
	s.BoolVar(&v.scoreJSON, "json", false, "emit JSON")

	list := &cobra.Command{
		Use:   "list",
		Short: "Show recorded interventions",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mine.List(out(), gateConfig().StateDir, v.listJSON)
		},
	}
	list.Flags().BoolVar(&v.listJSON, "json", false, "emit JSON")

	describe := &cobra.Command{
		Use:   "describe ID",
		Short: "Rewrite an intervention's explanation, and nothing else",
		Long: "Rewrite an intervention's explanation, and nothing else.\n\n" +
			"Wording is not evidence, so this writes the two prose fields and leaves the\n" +
			"id, the match, the scope, the baseline, the instrument and both windows\n" +
			"exactly as they were. Correcting a description never restarts a measurement.\n" +
			"An omitted flag keeps the value the record already carries.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := prepareState(); err != nil {
				return err
			}
			return mine.Describe(out(), gateConfig().StateDir, args[0], mine.DescribeOpts{
				Problem:      v.describeProblem,
				Intervention: v.describeIntervent,
			})
		},
	}
	describe.Flags().StringVar(&v.describeProblem, "problem", "", problemUsage)
	describe.Flags().StringVar(&v.describeIntervent, "intervention", "", interventionUsage)

	retire := &cobra.Command{
		Use:   "retire ID",
		Short: "Mark an intervention reworked or removed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := prepareState(); err != nil {
				return err
			}
			return mine.Retire(out(), gateConfig().StateDir, args[0],
				v.retireReason, v.retireStatus)
		},
	}
	retire.Flags().StringVar(&v.retireReason, "reason", "", "why (required)")
	retire.Flags().StringVar(&v.retireStatus, "status", "removed", "reworked|removed")
	_ = retire.MarkFlagRequired("reason")

	cmds := []*cobra.Command{match, register, reject, scope, score, list, describe, retire}

	if withFindings {
		findings := &cobra.Command{
			Use:   "findings",
			Short: "Emit adjudicated findings grouped by chain",
			RunE: func(cmd *cobra.Command, args []string) error {
				return mine.Findings(out(), gateConfig().StateDir, mine.FindingsOpts{
					Ledger:       v.ledger,
					Kind:         v.findingsKind,
					Since:        v.findingsSince,
					StrictJoins:  v.findingsStrictJoins,
					IncludeEmpty: v.findingsIncludeEmpty,
					JSON:         v.findingsJSON,
				})
			},
		}
		f := findings.Flags()
		f.StringVar(&v.findingsKind, "kind", "accepted", "accepted|rejected|both")
		f.Float64Var(&v.findingsSince, "since", 0, "only the last N days")
		f.BoolVar(&v.findingsStrictJoins, "strict-joins", false, "round-joined rows only")
		f.BoolVar(&v.findingsIncludeEmpty, "include-empty", false, "keep chains with no findings")
		f.BoolVar(&v.findingsJSON, "json", false, "emit JSON")
		cmds = append([]*cobra.Command{findings}, cmds...)
	}
	return v, cmds
}

var interventionCmd = &cobra.Command{
	Use:   "intervention",
	Short: "Register, refuse and score the fixes an evidence corpus motivates",
	Long: `intervention is the writer and scorer of the measured-fix loop: it tests a
candidate class regex against a corpus, freezes an eligibility scope and a baseline at
registration, and reports the effect range that baseline earns afterwards.

A record names the corpus it is counted over and the unit that corpus counts by. This
engine reads the commit-review ledger by chain; a record from another corpus is carried
unscored rather than scored against evidence it was never authored over.

steer commit-gate mine is the same command set under its original name.`,
}

func init() {
	v, cmds := newInterventionCommands(false)
	interventionCmd.AddCommand(cmds...)
	interventionCmd.PersistentFlags().StringVar(&v.ledger, "ledger", "",
		"Override the adjudication ledger path (a single file, not the shard glob)")
	rootCmd.AddCommand(interventionCmd)
}

// recordTier reads one intervention's tier, so a scope written onto an existing
// record is held to the rule its tier implies. An unreadable inventory yields an
// empty tier, which is not a guard tier and adds no refusal of its own.
func recordTier(id string) (string, error) {
	records, err := mine.Load(gateConfig().StateDir)
	if err != nil {
		return "", nil
	}
	for _, record := range records {
		if fmt.Sprint(record["id"]) == id {
			return fmt.Sprint(record["tier"]), nil
		}
	}
	return "", nil
}
