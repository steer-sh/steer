package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/checkscope"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/guardscope"
	"github.com/steer-sh/steer-cli/internal/sessions"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// snapshotFreshnessNote says how old the transcript snapshot these counts read is.
// score never rebuilds it: `steer report refresh` is the one writer of the snapshot
// store, so this names that command rather than re-reading the harnesses here. A
// reader who does not know the snapshot's age cannot tell a quiet window from an
// unread one.
func snapshotFreshnessNote(last, now time.Time) string {
	const how = "; `steer report refresh` re-reads the harnesses' transcripts"
	if last.IsZero() {
		return "# these counts read the stored transcript snapshot, whose last refresh " +
			"this machine does not record" + how
	}
	return fmt.Sprintf("# these counts read the transcript snapshot refreshed %s, %d days ago%s",
		last.Format(time.RFC3339), int(now.Sub(last).Hours()/24), how)
}

// The intervention commands exist under two parents: `steer fix`, the
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
	findingsUndecided    bool
	findingsJSON         bool

	matchKind        string
	matchStrictJoins bool
	matchCorpus      string
	matchSignalKind  string
	matchShowResidue bool

	registerClass         string
	registerMatch         string
	registerKind          string
	registerCorpus        string
	registerSignalKind    string
	registerTier          string
	registerTarget        string
	registerRuleID        string
	registerRuleScope     string
	registerAddendumID    string
	registerAddendumScope string
	registerNotMech       string
	registerHookID        string
	registerHookScope     string
	registerCheckID       string
	registerNote          string
	registerProblem       string
	registerIntervent     string
	registerSupersedes    string
	registerInstalledTS   int64
	registerForce         bool
	registerScopeAll      bool
	registerScopeHrn      []string
	registerScopeRepo     string
	registerScopeRoot     string
	registerScopePath     string

	rejectClass      string
	rejectMatch      string
	rejectKind       string
	rejectCorpus     string
	rejectSignalKind string
	rejectReason     string
	rejectReopen     string
	rejectReopenMtch string
	rejectReopenAt   int
	rejectNote       string
	rejectProblem    string
	rejectIntervent  string
	rejectRejectedTS int64
	rejectForce      bool
	rejectScopeAll   bool
	rejectScopeHrn   []string
	rejectScopeRepo  string
	rejectScopeRoot  string
	rejectScopePath  string

	scopeAll   bool
	scopeHrn   []string
	scopeRepo  string
	scopeRoot  string
	scopePath  string
	scopeSetTS int64
	scopeForce bool

	scoreID   string
	scoreAll  bool
	scoreJSON bool

	listJSON bool

	describeProblem   string
	describeIntervent string
	retireReason      string
	retireStatus      string
}

const scopeHarnessUsage = "eligibility scope: harness ids the class is measured on (repeatable or comma-separated)"

const scopeRepoRootUsage = "eligibility scope: the exact repository root the class is measured in, " +
	"which two checkouts sharing a basename do not share"

// The prose flags are required because a class name is a label, not an explanation.
// A reader arriving at a record months later has neither the evidence nor the session
// that produced it, so the record carries both halves in its own words.
const problemUsage = "one sentence naming what goes wrong, readable without the session that found it (required)"

const interventionUsage = "one sentence naming what this fix does about it (required)"

const rejectInterventionUsage = "one sentence naming the fix considered, or that none was found (required)"

const corpusUsage = "evidence corpus the class is counted over: review-findings (by chain) or transcript-friction (by session)"

const signalKindUsage = "transcript-friction only: the classifier kind the class is counted over " +
	"(any|user_corrections|user_rejections|tool_failures|hook_blocks)"

// bindCheckTier freezes the check a check-tier record names, from the repository the
// command runs in. Every other rung binds nothing here and gets a nil map.
func bindCheckTier(tierValue, checkID string, scopeHarness []string) (map[string]any, error) {
	if !tier.IsCheck(tierValue) {
		return nil, nil
	}
	root, err := repoRootOf(".")
	if err != nil {
		return nil, err
	}
	ids, err := guardscope.Normalize(scopeHarness)
	if err != nil {
		return nil, err
	}
	return checkscope.Bind(tierValue, root, checkID, ids)
}

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
				ShowResidue: v.matchShowResidue,
				Sessions:    storedSessions(),
			})
		},
	}
	m := match.Flags()
	m.StringVar(&v.matchKind, "kind", "accepted", "accepted|rejected|both")
	m.BoolVar(&v.matchStrictJoins, "strict-joins", false, "round-joined rows only")
	m.StringVar(&v.matchCorpus, "corpus", mine.CorpusReviewFindings, corpusUsage)
	m.StringVar(&v.matchSignalKind, "signal-kind", "", signalKindUsage)
	m.BoolVar(&v.matchShowResidue, "show-residue", false,
		"list the frequent terms among the findings this regex misses")

	register := &cobra.Command{
		Use:   "register",
		Short: "Record a fix with the baseline it is measured against",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := guardscope.Check(v.registerTier, v.registerScopeAll,
				v.registerScopeHrn, v.registerForce); err != nil {
				return err
			}
			checkFacts, err := bindCheckTier(v.registerTier, v.registerCheckID, v.registerScopeHrn)
			if err != nil {
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
				HookID:          v.registerHookID,
				HookScope:       v.registerHookScope,
				CheckID:         v.registerCheckID,
				CheckFacts:      checkFacts,
				RuleID:          v.registerRuleID,
				RuleScope:       v.registerRuleScope,
				AddendumID:      v.registerAddendumID,
				AddendumScope:   v.registerAddendumScope,
				NotMechanizable: v.registerNotMech,
				Supersedes:      v.registerSupersedes,
				InstalledTS:     v.registerInstalledTS,
				Force:           v.registerForce,
				ScopeAll:        v.registerScopeAll,
				ScopeHarness:    v.registerScopeHrn,
				ScopeRepo:       v.registerScopeRepo,
				ScopeRepoRoot:   v.registerScopeRoot,
				ScopePath:       v.registerScopePath,
			})
		},
	}
	r := register.Flags()
	r.StringVar(&v.registerClass, "class", "", "class name (required)")
	r.StringVar(&v.registerMatch, "match", "", "regex defining class membership (required)")
	r.StringVar(&v.registerKind, "kind", "defect", "defect|false-positive")
	r.StringVar(&v.registerCorpus, "corpus", mine.CorpusReviewFindings, corpusUsage)
	r.StringVar(&v.registerSignalKind, "signal-kind", "", signalKindUsage)
	r.StringVar(&v.registerTier, "tier", "", "type of fix: check|guard|instruction|review (required)")
	r.StringVar(&v.registerTarget, "target", "", "file the fix landed in (required)")
	r.StringVar(&v.registerHookID, "hook-id", "",
		"steer-manifest key, matching the audit log's hook_name; required for a guard tier")
	r.StringVar(&v.registerHookScope, "hook-scope", "",
		"guard store owning --hook-id: project|global (default: whichever holds it, refusing both)")
	r.StringVar(&v.registerCheckID, "check-id", "",
		"id in .steer/checks/manifest.json naming the declared check; required for a check tier")
	r.StringVar(&v.registerRuleID, "rule-id", "",
		"steer-manifest rules key naming the installed prose; required for an instruction tier")
	r.StringVar(&v.registerRuleScope, "rule-scope", "",
		"store owning --rule-id: project|global (default: whichever holds it, refusing both)")
	r.StringVar(&v.registerAddendumID, "addendum-id", "",
		"steer-manifest review_addendum key naming the installed entry; required for a review tier")
	r.StringVar(&v.registerAddendumScope, "addendum-scope", "",
		"store owning --addendum-id: project|global (default: whichever holds it, refusing both)")
	r.StringVar(&v.registerNotMech, mine.NotMechanizableFlag, "",
		"why a repository check and a pre-tool guard cannot detect this class; "+
			"required for an instruction tier")
	r.StringVar(&v.registerNote, "note", "", "free-text note")
	r.StringVar(&v.registerProblem, "problem", "", problemUsage)
	r.StringVar(&v.registerIntervent, "intervention", "", interventionUsage)
	r.StringVar(&v.registerSupersedes, "supersedes", "", "id of a record this replaces")
	r.Int64Var(&v.registerInstalledTS, "installed-ts", 0, "install timestamp (default now)")
	r.BoolVar(&v.registerForce, "force", false, "record despite a zero baseline or a blocking rejection")
	r.BoolVar(&v.registerScopeAll, "scope-all", false, "eligibility scope: every unit (exclusive with the scope predicates)")
	r.StringSliceVar(&v.registerScopeHrn, "scope-harness", nil, scopeHarnessUsage)
	r.StringVar(&v.registerScopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	r.StringVar(&v.registerScopeRoot, "scope-repo-root", "", scopeRepoRootUsage)
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
				Ledger:      v.ledger,
				Class:       v.rejectClass,
				Match:       v.rejectMatch,
				Kind:        v.rejectKind,
				Corpus:      v.rejectCorpus,
				SignalKind:  v.rejectSignalKind,
				Sessions:    storedSessions(),
				Reason:      v.rejectReason,
				Reopen:      v.rejectReopen,
				ReopenMatch: v.rejectReopenMtch,
				ReopenAt:    v.rejectReopenAt,
				Note:        v.rejectNote,
				Prose: mine.Prose{
					Problem:      v.rejectProblem,
					Intervention: v.rejectIntervent,
				},
				RejectedTS:    v.rejectRejectedTS,
				Force:         v.rejectForce,
				ScopeAll:      v.rejectScopeAll,
				ScopeHarness:  v.rejectScopeHrn,
				ScopeRepo:     v.rejectScopeRepo,
				ScopeRepoRoot: v.rejectScopeRoot,
				ScopePath:     v.rejectScopePath,
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
	rj.StringVar(&v.rejectReopenMtch, "reopen-match", "", "regex for the subset the reopen threshold counts; required when --reopen-at is set")
	rj.IntVar(&v.rejectReopenAt, "reopen-at", 0, "chain count at which --reopen-match reopens this refusal")
	rj.StringVar(&v.rejectNote, "note", "", "free-text note")
	rj.StringVar(&v.rejectProblem, "problem", "", problemUsage)
	rj.StringVar(&v.rejectIntervent, "intervention", "", rejectInterventionUsage)
	rj.Int64Var(&v.rejectRejectedTS, "rejected-ts", 0, "rejection timestamp (default now)")
	rj.BoolVar(&v.rejectForce, "force", false, "record despite a zero baseline")
	rj.BoolVar(&v.rejectScopeAll, "scope-all", false, "eligibility scope: every unit (exclusive with the scope predicates)")
	rj.StringSliceVar(&v.rejectScopeHrn, "scope-harness", nil, scopeHarnessUsage)
	rj.StringVar(&v.rejectScopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	rj.StringVar(&v.rejectScopeRoot, "scope-repo-root", "", scopeRepoRootUsage)
	rj.StringVar(&v.rejectScopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	for _, name := range []string{"class", "match", "reason", "reopen", "problem", "intervention"} {
		_ = reject.MarkFlagRequired(name)
	}

	scope := &cobra.Command{
		Use:   "scope ID",
		Short: "Install a frozen scope on a legacy, unscoped record",
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
				Ledger:        v.ledger,
				Sessions:      storedSessions(),
				ScopeAll:      v.scopeAll,
				ScopeHarness:  v.scopeHrn,
				ScopeRepo:     v.scopeRepo,
				ScopeRepoRoot: v.scopeRoot,
				ScopePath:     v.scopePath,
				ScopeSetTS:    v.scopeSetTS,
				Force:         v.scopeForce,
			})
		},
	}
	sc := scope.Flags()
	sc.BoolVar(&v.scopeAll, "scope-all", false, "eligibility scope: every unit (exclusive with the scope predicates)")
	sc.StringSliceVar(&v.scopeHrn, "scope-harness", nil, scopeHarnessUsage)
	sc.StringVar(&v.scopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	sc.StringVar(&v.scopeRoot, "scope-repo-root", "", scopeRepoRootUsage)
	sc.StringVar(&v.scopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	sc.Int64Var(&v.scopeSetTS, "scope-set-ts", 0, "migration timestamp opening the after-window (default now)")
	sc.BoolVar(&v.scopeForce, "force", false, "record despite a zero scoped baseline")

	score := &cobra.Command{
		Use:   "score",
		Short: "Recompute before/after incidence and the observation",
		RunE: func(cmd *cobra.Command, args []string) error {
			ev := evidence.Gather(evidence.Options{
				GateStateDir: gateConfig().StateDir,
				Ledger:       v.ledger,
			})
			if !v.scoreJSON {
				fmt.Fprintln(out(), snapshotFreshnessNote(
					cadence.LastRefresh(cadence.StateDir()), time.Now()))
			}
			return mine.Score(out(), gateConfig().StateDir, mine.ScoreOpts{
				Ledger:       v.ledger,
				ID:           v.scoreID,
				All:          v.scoreAll,
				JSON:         v.scoreJSON,
				Sessions:     ev.Sessions,
				Firings:      ev.Firings,
				AuditPresent: ev.AuditPresent,
				CheckRuns:    ev.CheckRuns,
				CheckRepos:   ev.CheckRepos,
			})
		},
	}
	s := score.Flags()
	s.StringVar(&v.scoreID, "id", "", "score only this record")
	s.BoolVar(&v.scoreAll, "all", false, "include reworked and removed records")
	s.BoolVar(&v.scoreJSON, "json", false, "emit JSON")

	list := &cobra.Command{
		Use:   "list",
		Short: "Show every recorded fix and refusal",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mine.List(out(), gateConfig().StateDir, v.listJSON)
		},
	}
	list.Flags().BoolVar(&v.listJSON, "json", false, "emit JSON")

	describe := &cobra.Command{
		Use:   "describe ID",
		Short: "Rewrite a record's explanation, and nothing else",
		Long: "Rewrite a record's explanation, and nothing else.\n\n" +
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
		Short: "Stop tracking a fix — mark it reworked or removed",
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
					Undecided:    v.findingsUndecided,
					JSON:         v.findingsJSON,
				})
			},
		}
		f := findings.Flags()
		f.StringVar(&v.findingsKind, "kind", "accepted", "accepted|rejected|both")
		f.Float64Var(&v.findingsSince, "since", 0, "only the last N days")
		f.BoolVar(&v.findingsStrictJoins, "strict-joins", false, "round-joined rows only")
		f.BoolVar(&v.findingsIncludeEmpty, "include-empty", false, "keep chains with no findings")
		f.BoolVar(&v.findingsUndecided, "undecided", false,
			"drop the chains a live class already covers, and mark the covered findings in the rest")
		f.BoolVar(&v.findingsJSON, "json", false, "emit JSON")
		cmds = append([]*cobra.Command{findings}, cmds...)
	}
	return v, cmds
}

var interventionCmd = &cobra.Command{
	Use:     "fix",
	Aliases: []string{"intervention"},
	Short:   "Register, refuse and score the fixes a recurring problem motivates",
	Long: `fix is the writer and scorer of the measured-fix loop: it tests a candidate
problem regex against a body of evidence, freezes an eligibility rule and a baseline at
registration, and reports the effect range that baseline earns afterwards.

A record names the evidence it is counted over and the item that evidence counts by.
This engine reads the review history by review sequence; a record from other evidence is
carried unscored rather than scored against evidence it was never authored over.

steer intervention is the same command set under its former name, and steer commit-gate
mine is the same set under its original one.`,
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
