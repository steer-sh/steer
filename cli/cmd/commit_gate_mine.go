package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// mineLedger is the persistent --ledger override: a single ledger file, read in place
// of the shard glob. It sits before the subcommand, matching mine.py's placement.
var mineLedger string

var commitGateMineCmd = &cobra.Command{
	Use:   "mine",
	Short: "Mine the adjudication ledger and score the fixes it motivates",
	Long: `mine reads the commit gate's adjudication ledger, groups its rows into chains
(one per commit attempt), and scores the interventions that a class's chain-level
incidence motivates. It is the reader half of the loop the gate's review half writes.

Output is byte-identical to the mine.py it replaces.`,
}

var (
	mineFindingsKind         string
	mineFindingsSince        float64
	mineFindingsStrictJoins  bool
	mineFindingsIncludeEmpty bool
	mineFindingsJSON         bool
)

var commitGateMineFindingsCmd = &cobra.Command{
	Use:   "findings",
	Short: "Emit adjudicated findings grouped by chain",
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.Findings(os.Stdout, gateConfig().StateDir, mine.FindingsOpts{
			Ledger:       mineLedger,
			Kind:         mineFindingsKind,
			Since:        mineFindingsSince,
			StrictJoins:  mineFindingsStrictJoins,
			IncludeEmpty: mineFindingsIncludeEmpty,
			JSON:         mineFindingsJSON,
		})
	},
}

var (
	mineMatchKind        string
	mineMatchStrictJoins bool
)

var commitGateMineMatchCmd = &cobra.Command{
	Use:   "match REGEX",
	Short: "Test a candidate class regex against the corpus",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.Match(os.Stdout, gateConfig().StateDir, mineLedger, args[0],
			mineMatchKind, mineMatchStrictJoins)
	},
}

var (
	mineRegisterClass       string
	mineRegisterMatch       string
	mineRegisterKind        string
	mineRegisterTier        string
	mineRegisterTarget      string
	mineRegisterHookID      string
	mineRegisterNote        string
	mineRegisterSupersedes  string
	mineRegisterInstalledTS int64
	mineRegisterForce       bool
	mineRegisterScopeAll    bool
	mineRegisterScopeRepo   string
	mineRegisterScopePath   string
)

var commitGateMineRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Record an intervention with its baseline incidence",
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.Register(os.Stdout, gateConfig().StateDir, mine.RegisterOpts{
			Ledger:      mineLedger,
			Class:       mineRegisterClass,
			Match:       mineRegisterMatch,
			Kind:        mineRegisterKind,
			Tier:        mineRegisterTier,
			Target:      mineRegisterTarget,
			Note:        mineRegisterNote,
			HookID:      mineRegisterHookID,
			Supersedes:  mineRegisterSupersedes,
			InstalledTS: mineRegisterInstalledTS,
			Force:       mineRegisterForce,
			ScopeAll:    mineRegisterScopeAll,
			ScopeRepo:   mineRegisterScopeRepo,
			ScopePath:   mineRegisterScopePath,
		})
	},
}

var (
	mineRejectClass      string
	mineRejectMatch      string
	mineRejectKind       string
	mineRejectReason     string
	mineRejectReopen     string
	mineRejectNote       string
	mineRejectRejectedTS int64
	mineRejectForce      bool
	mineRejectScopeAll   bool
	mineRejectScopeRepo  string
	mineRejectScopePath  string
)

var commitGateMineRejectCmd = &cobra.Command{
	Use:   "reject",
	Short: "Record a class examined and refused",
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.Reject(os.Stdout, gateConfig().StateDir, mine.RejectOpts{
			Ledger:     mineLedger,
			Class:      mineRejectClass,
			Match:      mineRejectMatch,
			Kind:       mineRejectKind,
			Reason:     mineRejectReason,
			Reopen:     mineRejectReopen,
			Note:       mineRejectNote,
			RejectedTS: mineRejectRejectedTS,
			Force:      mineRejectForce,
			ScopeAll:   mineRejectScopeAll,
			ScopeRepo:  mineRejectScopeRepo,
			ScopePath:  mineRejectScopePath,
		})
	},
}

var (
	mineScopeAll   bool
	mineScopeRepo  string
	mineScopePath  string
	mineScopeSetTS int64
	mineScopeForce bool
)

var commitGateMineScopeCmd = &cobra.Command{
	Use:   "scope ID",
	Short: "Install a frozen scope on a legacy, unscoped intervention",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.ScopeSet(os.Stdout, gateConfig().StateDir, args[0], mine.ScopeSetOpts{
			Ledger:     mineLedger,
			ScopeAll:   mineScopeAll,
			ScopeRepo:  mineScopeRepo,
			ScopePath:  mineScopePath,
			ScopeSetTS: mineScopeSetTS,
			Force:      mineScopeForce,
		})
	},
}

var (
	mineScoreID       string
	mineScoreAll      bool
	mineScoreMinAfter int
	mineScoreJSON     bool
)

var commitGateMineScoreCmd = &cobra.Command{
	Use:   "score",
	Short: "Recompute before/after incidence and verdict",
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.Score(os.Stdout, gateConfig().StateDir, mine.ScoreOpts{
			Ledger:   mineLedger,
			ID:       mineScoreID,
			All:      mineScoreAll,
			MinAfter: mineScoreMinAfter,
			JSON:     mineScoreJSON,
		})
	},
}

var mineListJSON bool

var commitGateMineListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show recorded interventions",
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.List(os.Stdout, gateConfig().StateDir, mineListJSON)
	},
}

var (
	mineRetireReason string
	mineRetireStatus string
)

var commitGateMineRetireCmd = &cobra.Command{
	Use:   "retire ID",
	Short: "Mark an intervention reworked or removed",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return mine.Retire(os.Stdout, gateConfig().StateDir, args[0],
			mineRetireReason, mineRetireStatus)
	},
}

func init() {
	commitGateCmd.AddCommand(commitGateMineCmd)
	commitGateMineCmd.AddCommand(commitGateMineFindingsCmd, commitGateMineMatchCmd,
		commitGateMineRegisterCmd, commitGateMineRejectCmd, commitGateMineScopeCmd,
		commitGateMineScoreCmd, commitGateMineListCmd, commitGateMineRetireCmd)

	commitGateMineCmd.PersistentFlags().StringVar(&mineLedger, "ledger", "",
		"Override the adjudication ledger path (a single file, not the shard glob)")

	f := commitGateMineFindingsCmd.Flags()
	f.StringVar(&mineFindingsKind, "kind", "accepted", "accepted|rejected|both")
	f.Float64Var(&mineFindingsSince, "since", 0, "only the last N days")
	f.BoolVar(&mineFindingsStrictJoins, "strict-joins", false, "round-joined rows only")
	f.BoolVar(&mineFindingsIncludeEmpty, "include-empty", false, "keep chains with no findings")
	f.BoolVar(&mineFindingsJSON, "json", false, "emit JSON")

	m := commitGateMineMatchCmd.Flags()
	m.StringVar(&mineMatchKind, "kind", "accepted", "accepted|rejected|both")
	m.BoolVar(&mineMatchStrictJoins, "strict-joins", false, "round-joined rows only")

	r := commitGateMineRegisterCmd.Flags()
	r.StringVar(&mineRegisterClass, "class", "", "class name (required)")
	r.StringVar(&mineRegisterMatch, "match", "", "regex defining class membership (required)")
	r.StringVar(&mineRegisterKind, "kind", "defect", "defect|false-positive")
	r.StringVar(&mineRegisterTier, "tier", "", "check|steer|prose|prompt (required)")
	r.StringVar(&mineRegisterTarget, "target", "", "file the fix landed in (required)")
	r.StringVar(&mineRegisterHookID, "hook-id", "",
		"steer-manifest key, matching the audit log's hook_name; required for --tier steer")
	r.StringVar(&mineRegisterNote, "note", "", "free-text note")
	r.StringVar(&mineRegisterSupersedes, "supersedes", "", "id of a record this replaces")
	r.Int64Var(&mineRegisterInstalledTS, "installed-ts", 0, "install timestamp (default now)")
	r.BoolVar(&mineRegisterForce, "force", false, "record despite a zero baseline or a blocking rejection")
	r.BoolVar(&mineRegisterScopeAll, "scope-all", false, "eligibility scope: every chain (exclusive with --scope-repo/--scope-path)")
	r.StringVar(&mineRegisterScopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	r.StringVar(&mineRegisterScopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	_ = commitGateMineRegisterCmd.MarkFlagRequired("class")
	_ = commitGateMineRegisterCmd.MarkFlagRequired("match")
	_ = commitGateMineRegisterCmd.MarkFlagRequired("tier")
	_ = commitGateMineRegisterCmd.MarkFlagRequired("target")

	rj := commitGateMineRejectCmd.Flags()
	rj.StringVar(&mineRejectClass, "class", "", "class name (required)")
	rj.StringVar(&mineRejectMatch, "match", "", "regex defining class membership (required)")
	rj.StringVar(&mineRejectKind, "kind", "defect", "defect|false-positive")
	rj.StringVar(&mineRejectReason, "reason", "", "why no gate was installed (required)")
	rj.StringVar(&mineRejectReopen, "reopen", "", "the observation that would make this worth reconsidering (required)")
	rj.StringVar(&mineRejectNote, "note", "", "free-text note")
	rj.Int64Var(&mineRejectRejectedTS, "rejected-ts", 0, "rejection timestamp (default now)")
	rj.BoolVar(&mineRejectForce, "force", false, "record despite a zero baseline")
	rj.BoolVar(&mineRejectScopeAll, "scope-all", false, "eligibility scope: every chain (exclusive with --scope-repo/--scope-path)")
	rj.StringVar(&mineRejectScopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	rj.StringVar(&mineRejectScopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	_ = commitGateMineRejectCmd.MarkFlagRequired("class")
	_ = commitGateMineRejectCmd.MarkFlagRequired("match")
	_ = commitGateMineRejectCmd.MarkFlagRequired("reason")
	_ = commitGateMineRejectCmd.MarkFlagRequired("reopen")

	sc := commitGateMineScopeCmd.Flags()
	sc.BoolVar(&mineScopeAll, "scope-all", false, "eligibility scope: every chain (exclusive with --scope-repo/--scope-path)")
	sc.StringVar(&mineScopeRepo, "scope-repo", "", "eligibility scope: regex on the repository root basename")
	sc.StringVar(&mineScopePath, "scope-path", "", "eligibility scope: regex on a repo-relative changed path")
	sc.Int64Var(&mineScopeSetTS, "scope-set-ts", 0, "migration timestamp opening the after-window (default now)")
	sc.BoolVar(&mineScopeForce, "force", false, "record despite a zero scoped baseline")

	s := commitGateMineScoreCmd.Flags()
	s.StringVar(&mineScoreID, "id", "", "score only this intervention")
	s.BoolVar(&mineScoreAll, "all", false, "include reworked and removed interventions")
	s.IntVar(&mineScoreMinAfter, "min-after", mine.MinAfterChains, "withhold a verdict below this many post-install chains")
	s.BoolVar(&mineScoreJSON, "json", false, "emit JSON")

	commitGateMineListCmd.Flags().BoolVar(&mineListJSON, "json", false, "emit JSON")

	commitGateMineRetireCmd.Flags().StringVar(&mineRetireReason, "reason", "", "why (required)")
	commitGateMineRetireCmd.Flags().StringVar(&mineRetireStatus, "status", "removed", "reworked|removed")
	_ = commitGateMineRetireCmd.MarkFlagRequired("reason")
}
