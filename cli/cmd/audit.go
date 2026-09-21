package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/evidence"
	"github.com/steer-sh/steer-cli/internal/firingaudit"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Read and judge the blocks a guard recorded",
	Long: `Audit reads the guard audit stream and records one judgment per block.

A judgment is a reading of one recorded firing: whether the blocked action would
have produced the failure its rule names, judged from the action in its original
task context, and separately what the task did after the block. Field counts
describe recurrence; they establish no fix's efficacy, and neither does the
fraction of firings a pass reads.`,
}

// group is one guard's rows in one store. The store decides which master
// answers for a hook name, so it is part of the unit rather than the hook name
// alone.
type group struct {
	StoreDir string
	HookName string
	Rows     []firingaudit.Row
	Identity adjudication.Identity
}

// auditSource is everything a subcommand reads: the rows, the judgments on
// hand, and which implementation each row's guard has installed now.
type auditSource struct {
	Rows      []firingaudit.Row
	Present   bool
	Receipts  []adjudication.Receipt
	Installed func(firingaudit.Row) adjudication.Identity
	Groups    []group
	// Excluded holds the rows an exclusion retires, keyed by row digest.
	Excluded map[string]adjudication.Exclusion
}

// loadAudit reads both guard stores and the adjudication stream.
//
// A master that cannot be read is an error rather than a silent Unknown: an
// unreadable identity would report every row as recorded by some other
// implementation, which is the opposite of what it means.
func loadAudit() (auditSource, error) {
	rows, present := firingaudit.Load(evidence.GuardStores()...)
	receipts, err := adjudication.Load(adjudication.Dir())
	if err != nil {
		return auditSource{}, err
	}
	exclusions, err := adjudication.LoadExclusions(adjudication.Dir())
	if err != nil {
		return auditSource{}, err
	}

	masters := map[string]claude.MasterIdentity{}
	for _, row := range rows {
		if _, ok := masters[row.StoreDir]; ok {
			continue
		}
		identity, err := claude.ReadMasterIdentity(row.StoreDir)
		if err != nil {
			return auditSource{}, err
		}
		masters[row.StoreDir] = identity
	}
	installed := func(row firingaudit.Row) adjudication.Identity {
		meta := masters[row.StoreDir].Hooks[row.HookName]
		return adjudication.Identity{HookDigest: meta.HookDigest, RuleHash: meta.RuleHash}
	}

	return auditSource{Rows: rows, Present: present, Receipts: receipts,
		Installed: installed, Groups: groupRows(rows, installed),
		Excluded: adjudication.Excluded(exclusions)}, nil
}

// groupRows collects each guard's rows per store, so the coverage arithmetic
// runs against the master that answers for those rows and no other.
func groupRows(rows []firingaudit.Row,
	installed func(firingaudit.Row) adjudication.Identity) []group {
	byKey := map[string]*group{}
	var order []string
	for _, row := range rows {
		key := row.StoreDir + "\x1f" + row.HookName
		if _, ok := byKey[key]; !ok {
			byKey[key] = &group{StoreDir: row.StoreDir, HookName: row.HookName,
				Identity: installed(row)}
			order = append(order, key)
		}
		byKey[key].Rows = append(byKey[key].Rows, row)
	}
	sort.Strings(order)
	groups := make([]group, 0, len(order))
	for _, key := range order {
		groups = append(groups, *byKey[key])
	}
	return groups
}

// guardCoverage is one guard's reading account in one store. Two stores can
// hold a guard of the same name, and their rows are not interchangeable, so
// each keeps its own row.
type guardCoverage struct {
	HookName string                `json:"hook_name"`
	StoreDir string                `json:"store_dir"`
	Coverage adjudication.Coverage `json:"coverage"`
}

// coverageOf accounts each guard's rows against the judgments on hand.
func (s auditSource) coverageOf(hookName string) []guardCoverage {
	var out []guardCoverage
	for _, g := range s.Groups {
		if hookName != "" && g.HookName != hookName {
			continue
		}
		out = append(out, guardCoverage{HookName: g.HookName, StoreDir: g.StoreDir,
			Coverage: adjudication.Cover("", firingaudit.Firings(g.Rows),
				s.Receipts, g.Identity, s.Excluded)})
	}
	return out
}

var auditQueueCmd = &cobra.Command{
	Use:   "queue",
	Short: "List the firings a reading pass should take next",
	Long: `Queue lists unread firings of the installed guard, oldest first.

A firing is here because no judgment names it, or because a judgment read the
block and nobody read what the task did next while that transcript is still on
this machine. Rows an earlier implementation recorded are a separate backlog:
no reading of them establishes what the guard running now does, so --other is
what asks for them.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := loadAudit()
		if err != nil {
			return err
		}
		items, counts := firingaudit.Queue(source.Rows, source.Receipts, firingaudit.QueueOptions{
			HookName:  auditHook,
			Installed: source.Installed,
			Other:     auditOther,
			Limit:     auditLimit,
			Excluded:  source.Excluded,
		})
		coverage := source.coverageOf(auditHook)
		if auditJSON {
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"audit_present": source.Present,
				"items":         items,
				"counts":        counts,
				"coverage":      coverage,
			})
		}
		return writeQueue(cmd.OutOrStdout(), source.Present, items, counts, coverage)
	},
}

func writeQueue(w io.Writer, present bool, items []firingaudit.Item,
	counts firingaudit.Counts, coverage []guardCoverage) error {
	if !present {
		fmt.Fprintln(w, "No guard store on this machine holds an audit stream.")
		return nil
	}
	for _, g := range coverage {
		c := g.Coverage
		fmt.Fprintf(w, "%s: %d rows, %d recorded by the installed guard, %d of those read\n",
			g.HookName, c.Rows, c.CurrentJudged+c.CurrentUnjudged, c.CurrentJudged)
		if c.Legacy > 0 {
			fmt.Fprintf(w, "  %d rows carry no identifier and can never be judged\n", c.Legacy)
		}
		if c.Excluded > 0 {
			fmt.Fprintf(w, "  %d rows excluded as recording no real work (steer audit rows --excluded)\n", c.Excluded)
		}
		if c.OtherJudged+c.OtherUnjudged > 0 {
			fmt.Fprintf(w, "  %d rows an earlier implementation recorded, %d of those read\n",
				c.OtherJudged+c.OtherUnjudged, c.OtherJudged)
		}
	}
	fmt.Fprintf(w, "\n%s\n", counts.Remaining())
	for _, item := range items {
		fmt.Fprintf(w, "  %s  %s  %s  %s  reach=%s\n",
			item.FiringID, item.Timestamp, item.HookName, item.Reason, item.Reach)
	}
	return nil
}

var auditShowCmd = &cobra.Command{
	Use:   "show FIRING_ID",
	Short: "Show one firing: the action it blocked and what followed",
	Long: `Show assembles one recorded block for reading.

It prints the row, the payload as it arrived beside the virtual edit the guard
objected to, and the continuation the harness allows. Anything it could not
resolve says so in place: a judgment of the action does not need the
continuation, and an unread continuation leaves benefit unresolved rather than
making the block wrong.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := loadAudit()
		if err != nil {
			return err
		}
		c, err := firingaudit.Show(source.Rows, args[0], source.Receipts, auditBudget())
		if err != nil {
			return err
		}
		render := firingaudit.Render(renderCase)
		if auditJSON {
			render = firingaudit.RenderJSON
		}
		out, _, err := firingaudit.Fit(c, auditMaxBytes, render)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(out)
		return err
	},
}

// renderCase writes one case as text at one clip level. It ends on the same
// account RenderJSON carries, so a reader of either form spends an allowance
// from the same numbers.
func renderCase(c firingaudit.Case, e firingaudit.Emission) ([]byte, error) {
	var buf bytes.Buffer
	w := &buf
	fmt.Fprintf(w, "firing %s\n", c.Row.FiringID)
	fmt.Fprintf(w, "  recorded  %s by %s on %s\n", c.Row.Timestamp, c.Row.HookName, c.Row.Harness)
	fmt.Fprintf(w, "  rule      %s\n", c.Row.Rule)
	if c.Row.Repository != "" {
		fmt.Fprintf(w, "  repository %s\n", c.Row.Repository)
	}
	fmt.Fprintf(w, "  guard     hook_digest=%s rule_hash=%s master=%s\n",
		or(c.Row.HookDigest), or(c.Row.RuleHash), or(c.Row.MasterBuildDigest))

	fmt.Fprintf(w, "\nblocked action (%s)\n", c.Evidence.Source)
	if c.Evidence.Reason != "" {
		fmt.Fprintf(w, "  %s\n", c.Evidence.Reason)
	}
	if len(c.Evidence.Bundle) > 0 {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, c.Evidence.Bundle, "  ", "  "); err != nil {
			fmt.Fprintf(w, "  %s\n", c.Evidence.Bundle)
		} else {
			fmt.Fprintf(w, "  %s\n", pretty.String())
		}
	} else if c.Row.Snippet != "" {
		fmt.Fprintf(w, "  snippet (truncated at write time): %s\n", c.Row.Snippet)
	}

	fmt.Fprintf(w, "\ncontinuation (join=%s)\n", c.Continuation.Granularity)
	if c.Continuation.Granularity == transcript.JoinTurn {
		// The steps are the whole turn, because the harness recorded no entry
		// for the call it blocked. Saying so at the point of reading is the
		// only place the attribution limit reaches the reader.
		fmt.Fprintf(w, "  the whole turn is shown, including work that preceded the block;\n"+
			"  position attributes nothing here to the block\n")
	}
	if c.Continuation.Reason != "" {
		fmt.Fprintf(w, "  %s\n", c.Continuation.Reason)
	}
	if c.Continuation.BranchPoints > 0 {
		fmt.Fprintf(w, "  %d fork(s) passed; this reading follows the first branch\n",
			c.Continuation.BranchPoints)
	}
	for _, step := range c.Continuation.Steps {
		line := string(step.Kind)
		if step.Tool != "" {
			line += " " + step.Tool
		}
		text := step.Target
		if text == "" {
			text = step.Text
		}
		fmt.Fprintf(w, "  %-14s %s\n", line, oneLine(text))
	}
	if end := endPhrase(c.Continuation.EndReason); end != "" {
		fmt.Fprintf(w, "  %s\n", end)
	}

	for _, prior := range c.Prior {
		fmt.Fprintf(w, "\nprior reading %s (%s): %s\n",
			prior.JudgedAt, prior.Judgment, prior.Rationale)
	}

	fmt.Fprintf(w, "\nthis reading: %d continuation step(s) of %d shown\n",
		e.StepsEmitted, e.StepsAvailable)
	for _, clip := range e.Clipped {
		fmt.Fprintf(w, "  %s\n", clip)
	}
	fmt.Fprintf(w, "  emitted_bytes %d\n", e.EmittedBytes)
	return buf.Bytes(), nil
}

// endPhrase says why a reading stopped. A budget that ran out leaves work
// unread and a finished turn does not, so the two never share a sentence.
func endPhrase(reason string) string {
	switch reason {
	case transcript.EndHumanTurn:
		return "ended where the person spoke next"
	case transcript.EndTurn:
		return "ended where the turn ended"
	case transcript.EndTranscript:
		return "ended at the end of the transcript, which may still be live"
	case transcript.EndBudget:
		return "stopped at the step budget, with entries left unread"
	}
	return ""
}

var auditRecordCmd = &cobra.Command{
	Use:   "record FIRING_ID",
	Short: "Record one judgment of a recorded block",
	Long: `Record writes one judgment to the append-only adjudication stream.

The reader supplies the judgment, the rationale and what it read. Record
resolves everything else from the row itself: the guard's implementation
stamps, the join granularity, its transcript anchors and its extent. It refuses
a judgment that reports an inspected continuation when no join reached one.

Judgments: prevented, incorrect, unresolved. Each describes this block and no
population, and a recurrence does not erase a demonstrated prevention.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			return err
		}
		dir, err := statestore.Prepare(adjudication.Component)
		if err != nil {
			return err
		}
		source, err := loadAudit()
		if err != nil {
			return err
		}
		if row, ok := firingaudit.Find(source.Rows, args[0]); ok {
			if _, retired := source.Excluded[row.RowDigest]; retired {
				return fmt.Errorf("firing %s is excluded as recording no real work; "+
					"steer audit reinstate it before judging it", args[0])
			}
		}
		receipt, err := firingaudit.Record(source.Rows, dir, firingaudit.Judgment{
			FiringID:              args[0],
			Judgment:              auditJudgment,
			Rationale:             auditRationale,
			Evidence:              auditEvidence,
			ContinuationInspected: auditInspected,
			InspectedSteps:        auditInspectedSteps,
			ContinuationOutcome:   auditOutcome,
			Selection:             auditSelection,
		}, auditBudget())
		if err != nil {
			return err
		}
		if auditJSON {
			return writeJSON(cmd.OutOrStdout(), receipt)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "recorded %s as %s (join=%s, %d step(s))\n",
			receipt.FiringID, receipt.Judgment, receipt.Join, receipt.JoinSteps)
		return nil
	},
}

// rowLine is one audit row as `audit rows` lists it: the fields that identify
// the row and say where it came from, without its evidence bundle.
type rowLine struct {
	RowDigest  string `json:"row_digest"`
	FiringID   string `json:"firing_id,omitempty"`
	Timestamp  string `json:"timestamp"`
	HookName   string `json:"hook_name"`
	Harness    string `json:"harness,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Repository string `json:"repository,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
	StoreDir   string `json:"store_dir"`
	Excluded   bool   `json:"excluded"`
	// ExcludedFor is the reason of the exclusion that retires this row.
	ExcludedFor string `json:"excluded_for,omitempty"`
}

var auditRowsCmd = &cobra.Command{
	Use:   "rows",
	Short: "List every audit row with the digest that names it",
	Long: `Rows lists every recorded block, oldest first, with its row digest.

The row digest names a row by its own bytes, so it names a row written before
firing identifiers existed too. Exclude and reinstate take a firing identifier,
a row digest, or a digest prefix of at least eight characters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := loadAudit()
		if err != nil {
			return err
		}
		lines := listRows(source, auditHook, auditExcludedOnly)
		if auditJSON {
			return writeJSON(cmd.OutOrStdout(), lines)
		}
		w := cmd.OutOrStdout()
		if !source.Present {
			fmt.Fprintln(w, "No guard store on this machine holds an audit stream.")
			return nil
		}
		for _, l := range lines {
			mark := ""
			if l.Excluded {
				mark = "  EXCLUDED: " + l.ExcludedFor
			}
			fmt.Fprintf(w, "%s  %s  %s  %s  session=%s  firing=%s%s\n    %s  %s\n",
				l.RowDigest[:12], l.Timestamp, l.HookName, or(l.Harness), or(l.SessionID),
				or(l.FiringID), mark, or(l.Repository), oneLine(l.Snippet))
		}
		return nil
	},
}

// listRows selects the rows `audit rows` prints, oldest first.
func listRows(source auditSource, hook string, excludedOnly bool) []rowLine {
	var out []rowLine
	for _, r := range source.Rows {
		if hook != "" && r.HookName != hook {
			continue
		}
		e, retired := source.Excluded[r.RowDigest]
		if excludedOnly && !retired {
			continue
		}
		out = append(out, rowLine{
			RowDigest: r.RowDigest, FiringID: r.FiringID, Timestamp: r.Timestamp,
			HookName: r.HookName, Harness: r.Harness, SessionID: r.SessionID,
			Repository: r.Repository, Snippet: r.Snippet, StoreDir: r.StoreDir,
			Excluded: retired, ExcludedFor: e.Reason,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out
}

// correctCmd builds exclude and reinstate, which differ only in the action.
func correctCmd(action, use, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := prepareState(); err != nil {
				return err
			}
			dir, err := statestore.Prepare(adjudication.Component)
			if err != nil {
				return err
			}
			source, err := loadAudit()
			if err != nil {
				return err
			}
			e, err := firingaudit.Correct(source.Rows, dir, firingaudit.Correction{
				Ref: args[0], Action: action, Reason: auditReason, Evidence: auditEvidence,
			}, source.Excluded)
			if err != nil {
				return err
			}
			if auditJSON {
				return writeJSON(cmd.OutOrStdout(), e)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s row %s (%s, %s)\n",
				map[string]string{adjudication.Exclude: "excluded", adjudication.Reinstate: "reinstated"}[action],
				e.RowDigest[:12], e.HookID, e.Timestamp)
			return nil
		},
	}
}

var auditExcludeCmd = correctCmd(adjudication.Exclude, "exclude REF",
	"Retire one audit row that records no real work",
	`Exclude appends a correction naming one row: a firing a test run caused under
the operator's guard store, for example. The row stays in the audit stream. The
queue, the coverage counts and the recurrence counts leave it out, and
'steer audit rows --excluded' lists it with its reason.

REF is a firing identifier, a row digest, or a digest prefix of at least eight
characters. --reason and at least one --evidence are required: the evidence is
what shows the row records no real work.`)

var auditReinstateCmd = correctCmd(adjudication.Reinstate, "reinstate REF",
	"Withdraw the exclusion of one audit row",
	`Reinstate appends a correction that withdraws an earlier exclusion. The latest
entry for a row decides. --reason is required.`)

func auditBudget() transcript.ContinuationBudget {
	return transcript.ContinuationBudget{Steps: auditSteps, TextBytes: auditText}
}

func writeJSON(w io.Writer, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(encoded))
	return nil
}

// or names an absent stamp rather than printing an empty column.
func or(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// oneLine keeps a step readable in a list. The whole text is in --json.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

var (
	auditHook           string
	auditLimit          int
	auditOther          bool
	auditJSON           bool
	auditSteps          int
	auditText           int
	auditJudgment       string
	auditRationale      string
	auditEvidence       []string
	auditInspected      bool
	auditInspectedSteps int
	auditMaxBytes       int
	auditOutcome        string
	auditSelection      string
	auditReason         string
	auditExcludedOnly   bool
)

func init() {
	rootCmd.AddCommand(auditCmd)
	auditCmd.AddCommand(auditQueueCmd, auditShowCmd, auditRecordCmd,
		auditRowsCmd, auditExcludeCmd, auditReinstateCmd)
	auditCmd.PersistentFlags().BoolVar(&auditJSON, "json", false, "emit JSON")

	q := auditQueueCmd.Flags()
	q.StringVar(&auditHook, "hook", "", "only this guard")
	q.IntVar(&auditLimit, "limit", 0, "at most this many firings (default all)")
	q.BoolVar(&auditOther, "other", false,
		"include rows an implementation other than the installed one recorded")

	for _, c := range []*cobra.Command{auditShowCmd, auditRecordCmd} {
		f := c.Flags()
		f.IntVar(&auditSteps, "steps", 0, "continuation steps to read")
		f.IntVar(&auditText, "text-bytes", 0, "text kept per continuation step")
	}
	auditShowCmd.Flags().IntVar(&auditMaxBytes, "max-bytes", 0,
		"bound this command's whole output, withholding the least decisive evidence first (default unbounded)")

	r := auditRecordCmd.Flags()
	r.StringVar(&auditJudgment, "judgment", "",
		"prevented|incorrect|unresolved (required)")
	r.StringVar(&auditRationale, "rationale", "", "why, from the action in its task context")
	r.StringArrayVar(&auditEvidence, "evidence", nil,
		"what was read, beyond the anchors record stamps itself (repeatable)")
	r.BoolVar(&auditInspected, "continuation-inspected", false,
		"the task after the block was read")
	r.IntVar(&auditInspectedSteps, "inspected-steps", 0,
		"how many continuation steps reached the reader, when show withheld some")
	r.StringVar(&auditOutcome, "continuation-outcome", "", "what the task did next")
	r.StringVar(&auditSelection, "selection", "", "why this firing was read")
	_ = auditRecordCmd.MarkFlagRequired("judgment")

	auditRowsCmd.Flags().StringVar(&auditHook, "hook", "", "only this guard")
	auditRowsCmd.Flags().BoolVar(&auditExcludedOnly, "excluded", false, "only the excluded rows")
	for _, c := range []*cobra.Command{auditExcludeCmd, auditReinstateCmd} {
		c.Flags().StringVar(&auditReason, "reason", "", "why (required)")
		_ = c.MarkFlagRequired("reason")
	}
	auditExcludeCmd.Flags().StringArrayVar(&auditEvidence, "evidence", nil,
		"what shows the row records no real work (repeatable, required)")
}
