package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

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
	Short: "Read and judge the blocks guards and check.sh recorded",
	Long: `Audit reads the guard audit stream and the recorded check.sh Stop blocks, and
records one judgment per block.

A guard judgment is a reading of one recorded firing: whether the blocked action
would have produced the failure its rule names, judged from the action in its
original task context, and separately what the task did after the block. A check
judgment reads each failure a blocked run reported: whether it is real, whether
the blocked turn introduced it, and what the task did about it. Field counts
describe recurrence; they establish no fix's efficacy, and neither does the
fraction of blocks a pass reads.`,
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
	// Cases are the check.sh Stop blocks recorded on this machine.
	Cases []firingaudit.CheckCase
}

// classify stamps each check failure with what a reading of it says about the
// detector installed now. It reads repositories, so only readers call it.
func (s auditSource) classify() {
	firingaudit.ClassifyCases(s.Cases, firingaudit.InstalledDetectors())
}

// loadAudit reads both guard stores, the recorded check blocks and the
// adjudication stream.
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

	cases, err := firingaudit.LoadCheckCases()
	if err != nil {
		return auditSource{}, err
	}

	return auditSource{Rows: rows, Present: present, Receipts: receipts,
		Installed: installed, Groups: groupRows(rows, installed),
		Excluded: adjudication.Excluded(exclusions), Cases: cases}, nil
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
	Short: "List the blocks a reading pass should take next",
	Long: `Queue lists unread guard firings of the installed guard and unread check.sh
Stop blocks, in one list, oldest first.

A block is here because no judgment names it, or because a judgment read the
block and nobody read what the task did next while that transcript is still on
this machine. Guard rows an earlier implementation recorded are a separate
backlog: no reading of them establishes what the guard running now does, so
--other is what asks for them. A check block is listed whatever its detectors
are now, and each failure says whether a reading of it describes the detector
installed now (applies=current). --subject guard|check narrows the list; --hook
names one guard and so lists no check block.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validSubject(auditSubject); err != nil {
			return err
		}
		source, err := loadAudit()
		if err != nil {
			return err
		}
		source.classify()
		items, counts := firingaudit.Queue(source.Rows, source.Cases, source.Receipts, firingaudit.QueueOptions{
			Subject:   auditSubject,
			HookName:  auditHook,
			Installed: source.Installed,
			Other:     auditOther,
			Limit:     auditLimit,
			Excluded:  source.Excluded,
		})
		var coverage []guardCoverage
		if auditSubject != adjudication.SubjectCheck {
			coverage = source.coverageOf(auditHook)
		}
		var checkCoverage []firingaudit.CheckCoverage
		if auditSubject != adjudication.SubjectGuard && auditHook == "" {
			checkCoverage = firingaudit.CoverChecks(source.Cases, source.Receipts)
		}
		if auditJSON {
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"audit_present":  source.Present,
				"items":          items,
				"counts":         counts,
				"coverage":       coverage,
				"check_coverage": checkCoverage,
			})
		}
		return writeQueue(cmd.OutOrStdout(), source.Present, items, counts, coverage, checkCoverage)
	},
}

func writeQueue(w io.Writer, present bool, items []firingaudit.Item,
	counts firingaudit.Counts, coverage []guardCoverage, checkCoverage []firingaudit.CheckCoverage) error {
	if !present && len(checkCoverage) == 0 {
		fmt.Fprintln(w, "No guard store on this machine holds an audit stream, and no check.sh Stop block is recorded.")
		return nil
	}
	for _, c := range checkCoverage {
		fmt.Fprintf(w, "check.sh in %s: %d Stop blocks, %d of those read\n", c.Repo, c.Blocks, c.Judged)
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
		if item.Subject == adjudication.SubjectCheck {
			var failures []string
			for _, f := range item.Failures {
				failures = append(failures, fmt.Sprintf("%s(applies=%s)",
					firingaudit.FailureName(f.Kind, f.Check), f.Applies))
			}
			fmt.Fprintf(w, "  %s  %s  check.sh block in %s  %s  reach=%s  %s\n",
				item.RunID, item.Timestamp, item.Repo, item.Reason, item.Reach, strings.Join(failures, " "))
			continue
		}
		fmt.Fprintf(w, "  %s  %s  %s  %s  reach=%s\n",
			item.FiringID, item.Timestamp, item.HookName, item.Reason, item.Reach)
	}
	return nil
}

// validSubject refuses a --subject value other than guard, check or empty.
func validSubject(subject string) error {
	switch subject {
	case "", adjudication.SubjectGuard, adjudication.SubjectCheck:
		return nil
	}
	return fmt.Errorf("invalid --subject %q: expected %s or %s",
		subject, adjudication.SubjectGuard, adjudication.SubjectCheck)
}

// resolveSubject decides whether id names a guard firing or a check run. The
// two identifiers can read alike, so an id both name needs --subject.
func resolveSubject(source auditSource, id, subject string) (string, error) {
	if err := validSubject(subject); err != nil {
		return "", err
	}
	if subject != "" {
		return subject, nil
	}
	_, guard := firingaudit.Find(source.Rows, id)
	_, check := firingaudit.FindCheckCase(source.Cases, id)
	switch {
	case guard && check:
		return "", fmt.Errorf("%s names both a guard firing and a check run; pass --subject", id)
	case check:
		return adjudication.SubjectCheck, nil
	}
	return adjudication.SubjectGuard, nil
}

var auditShowCmd = &cobra.Command{
	Use:   "show ID",
	Short: "Show one block: what it stopped and what followed",
	Long: `Show assembles one recorded block for reading. ID is a guard firing's
identifier or a check run's; --subject settles an ID that names both.

For a guard firing it prints the row, the payload as it arrived beside the
virtual edit the guard objected to, and the continuation the harness allows. For
a check block it prints the run, each failure it reported with whether a reading
of it describes the detector installed now, what the records show next, the turn
before the block and what the session did after it. Anything it could not
resolve says so in place: an unread continuation leaves benefit unresolved
rather than making the block wrong.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := loadAudit()
		if err != nil {
			return err
		}
		subject, err := resolveSubject(source, args[0], auditSubject)
		if err != nil {
			return err
		}
		if subject == adjudication.SubjectCheck {
			source.classify()
			c, err := firingaudit.ShowCheck(source.Cases, args[0], source.Receipts, auditBudget())
			if err != nil {
				return err
			}
			render := firingaudit.RenderCheck(renderCheckCase)
			if auditJSON {
				render = firingaudit.RenderCheckJSON
			}
			out, _, err := firingaudit.FitCheck(c, auditMaxBytes, render)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(out)
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
	case transcript.EndInterrupted:
		return "ended where the turn was interrupted"
	}
	return ""
}

// renderCheckCase writes one check block as text at one clip level. It ends on
// the same account RenderCheckJSON carries.
func renderCheckCase(c firingaudit.CheckShow, e firingaudit.Emission) ([]byte, error) {
	var buf bytes.Buffer
	w := &buf
	r := c.Case.Run
	fmt.Fprintf(w, "check.sh run %s blocked a %s turn\n", r.RunID, or(r.Harness))
	fmt.Fprintf(w, "  recorded   %s in %s (session %s)\n", r.Started.UTC().Format(time.RFC3339), r.Repo, or(r.Session))
	exit := "no exit status"
	if r.ExitCode != nil {
		exit = fmt.Sprintf("exit %d", *r.ExitCode)
	}
	fmt.Fprintf(w, "  check.sh   %s (%s)\n", r.Outcome, exit)
	if len(r.Missing) > 0 {
		fmt.Fprintf(w, "  not reached %s\n", strings.Join(r.Missing, ", "))
	}

	fmt.Fprintln(w, "\nfailures (each is read on its own)")
	for _, f := range c.Case.Failures {
		need := "optional"
		if f.Required {
			need = "required"
		}
		line := fmt.Sprintf("  %-24s %s  applies=%s", firingaudit.FailureName(f.Kind, f.Check), need, or(f.Applies))
		if f.Execution != nil {
			line += fmt.Sprintf("  %s  detector=%s", f.Execution.Outcome, or(f.Execution.DetectorDigest))
		}
		fmt.Fprintln(w, line)
		if f.Execution != nil && f.Execution.Excerpt != "" {
			writeIndented(w, "    ", f.Execution.Excerpt)
		}
	}
	if r.Excerpt != "" {
		fmt.Fprintln(w, "  check.sh output (the recorded excerpt):")
		writeIndented(w, "    ", r.Excerpt)
	}

	f := c.Followup
	fmt.Fprintln(w, "\nwhat the records show next")
	if f.NextStop != nil {
		fmt.Fprintf(w, "  next Stop in this session: %s at %s; tree %s\n", f.NextStop.Disposition, f.NextStop.Started, f.SessionTree)
	} else {
		fmt.Fprintln(w, "  next Stop in this session: none recorded")
	}
	if f.NextRun != nil {
		fmt.Fprintf(w, "  next check.sh run: %s at %s (%s)\n", f.NextRun.Outcome, f.NextRun.Started, f.NextRun.Source)
	} else {
		fmt.Fprintln(w, "  next check.sh run: none recorded")
	}
	for _, ch := range f.Checks {
		fmt.Fprintf(w, "  %s: %s at the block; next %s; detector at the next pass %s\n",
			ch.Check, ch.Blocked, or(ch.Next), ch.DetectorAtPass)
	}

	reading := c.Reading
	fmt.Fprintf(w, "\ntranscript (join=%s)\n", reading.Granularity)
	if reading.Reason != "" {
		fmt.Fprintf(w, "  %s\n", reading.Reason)
	}
	if reading.Matches > 1 {
		fmt.Fprintf(w, "  %d entries carry this block's reason; the reading anchors on the first\n", reading.Matches)
	}
	if reading.ContextOmitted > 0 {
		fmt.Fprintf(w, "  %d step(s) between the prompt and the block are not shown\n", reading.ContextOmitted)
	}
	writeSteps(w, "before", reading.Context)
	if reading.BranchPoints > 0 {
		fmt.Fprintf(w, "  %d fork(s) passed; this reading follows the first branch\n", reading.BranchPoints)
	}
	writeSteps(w, "after", reading.Steps)
	if end := endPhrase(reading.EndReason); end != "" {
		fmt.Fprintf(w, "  %s\n", end)
	}

	for _, prior := range c.Prior {
		var readings []string
		for _, pf := range prior.Failures {
			readings = append(readings, fmt.Sprintf("%s=%s,%s", firingaudit.FailureName(pf.Kind, pf.Check), pf.Validity, pf.Attribution))
		}
		fmt.Fprintf(w, "\nprior reading %s (%s): %s\n", prior.JudgedAt, strings.Join(readings, " "), prior.Rationale)
	}

	fmt.Fprintf(w, "\nthis reading: %d context step(s) of %d and %d continuation step(s) of %d shown\n",
		e.ContextEmitted, e.ContextAvailable, e.StepsEmitted, e.StepsAvailable)
	for _, clip := range e.Clipped {
		fmt.Fprintf(w, "  %s\n", clip)
	}
	fmt.Fprintf(w, "  emitted_bytes %d\n", e.EmittedBytes)
	return buf.Bytes(), nil
}

// writeIndented writes text line by line under a prefix. An excerpt is
// already bounded when it is recorded, so it is written whole.
func writeIndented(w io.Writer, prefix, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintf(w, "%s%s\n", prefix, line)
	}
}

// writeSteps lists transcript steps under a label, one line each.
func writeSteps(w io.Writer, label string, steps []transcript.ContinuationStep) {
	for _, step := range steps {
		line := string(step.Kind)
		if step.Tool != "" {
			line += " " + step.Tool
		}
		text := step.Target
		if text == "" {
			text = step.Text
		}
		fmt.Fprintf(w, "  %-6s %-16s %s\n", label, line, oneLine(text))
	}
}

var auditRecordCmd = &cobra.Command{
	Use:   "record ID",
	Short: "Record one judgment of a recorded block",
	Long: `Record writes one judgment to the append-only adjudication stream. ID is a
guard firing's identifier or a check run's; --subject settles an ID that names both.

The reader supplies the judgment, the rationale and what it read. Record
resolves everything else from the records: the guard's implementation stamps or
each check's detector digest, the join granularity, its transcript anchors and
its extent. It refuses a judgment that reports an inspected continuation when
no join reached one.

A guard firing takes --judgment: prevented, incorrect or unresolved. Each
describes this block and no population, and a recurrence does not erase a
demonstrated prevention.

A check block takes one --failure per failure it reads, and must read every
failure show marks required:
  --failure check:ID=VALIDITY,ATTRIBUTION[,CONTINUATION]
  --failure oracle=...        check.sh itself
  --failure integration=...   a declared check check.sh never ran
VALIDITY is valid, invalid or unresolved; ATTRIBUTION is introduced,
pre-existing or unresolved; CONTINUATION, only with --continuation-inspected,
is fixed, routed-around, declined, interrupted or unresolved. A failure that
was real and not this turn's is valid,pre-existing.`,
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
		subject, err := resolveSubject(source, args[0], auditSubject)
		if err != nil {
			return err
		}
		if subject == adjudication.SubjectCheck {
			return recordCheck(cmd, source, dir, args[0])
		}
		if len(auditFailures) > 0 {
			return fmt.Errorf("--failure reads a check block; %s is a guard firing, which takes --judgment", args[0])
		}
		if auditJudgment == "" {
			return fmt.Errorf("a guard firing needs --judgment")
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

// recordCheck writes one judgment of the check block of run runID.
func recordCheck(cmd *cobra.Command, source auditSource, dir, runID string) error {
	if auditJudgment != "" {
		return fmt.Errorf("--judgment reads a guard firing; %s is a check run, which takes one --failure per failure", runID)
	}
	if len(auditFailures) == 0 {
		return fmt.Errorf("a check block needs one --failure per failure it reads")
	}
	var failures []adjudication.Failure
	for _, spec := range auditFailures {
		f, err := firingaudit.ParseFailure(spec)
		if err != nil {
			return err
		}
		failures = append(failures, f)
	}
	receipt, err := firingaudit.RecordCheck(source.Cases, dir, firingaudit.CheckJudgment{
		RunID:                 runID,
		Failures:              failures,
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
	fmt.Fprintf(cmd.OutOrStdout(), "recorded run %s: %d failure(s) read (join=%s, %d step(s))\n",
		receipt.RunID, len(receipt.Failures), receipt.Join, receipt.JoinSteps)
	return nil
}

// rowLine is one audit row as `audit rows` lists it: the fields that identify
// the row and say where it came from, without its evidence bundle.
type rowLine struct {
	Subject    string `json:"subject"`
	RowDigest  string `json:"row_digest,omitempty"`
	RunID      string `json:"run_id,omitempty"`
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
	Short: "List every recorded block with the identifier that names it",
	Long: `Rows lists every recorded block, oldest first: each guard audit row with its
row digest, and each check.sh Stop block with its run identifier.

The row digest names a guard row by its own bytes, so it names a row written
before firing identifiers existed too. Exclude and reinstate take a firing
identifier, a row digest, or a digest prefix of at least eight characters.
--subject guard|check narrows the list; --hook names one guard.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validSubject(auditSubject); err != nil {
			return err
		}
		source, err := loadAudit()
		if err != nil {
			return err
		}
		lines := listRows(source, auditSubject, auditHook, auditExcludedOnly)
		if auditJSON {
			return writeJSON(cmd.OutOrStdout(), lines)
		}
		w := cmd.OutOrStdout()
		if !source.Present && len(source.Cases) == 0 {
			fmt.Fprintln(w, "No guard store on this machine holds an audit stream, and no check.sh Stop block is recorded.")
			return nil
		}
		for _, l := range lines {
			if l.Subject == adjudication.SubjectCheck {
				fmt.Fprintf(w, "%s  %s  check.sh  %s  session=%s\n    %s\n",
					l.RunID, l.Timestamp, or(l.Harness), or(l.SessionID), l.Repository)
				continue
			}
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

// listRows selects the rows `audit rows` prints, oldest first. A guard name or
// the excluded filter selects guard rows only.
func listRows(source auditSource, subject, hook string, excludedOnly bool) []rowLine {
	var out []rowLine
	if subject != adjudication.SubjectGuard && hook == "" && !excludedOnly {
		for _, c := range source.Cases {
			out = append(out, rowLine{
				Subject: adjudication.SubjectCheck, RunID: c.Run.RunID,
				Timestamp: c.Run.Started.UTC().Format(time.RFC3339Nano),
				HookName:  "check.sh", Harness: c.Run.Harness, SessionID: c.Run.Session,
				Repository: c.Run.Repo,
			})
		}
	}
	for _, r := range source.Rows {
		if subject == adjudication.SubjectCheck {
			break
		}
		if hook != "" && r.HookName != hook {
			continue
		}
		e, retired := source.Excluded[r.RowDigest]
		if excludedOnly && !retired {
			continue
		}
		out = append(out, rowLine{
			Subject:   adjudication.SubjectGuard,
			RowDigest: r.RowDigest, FiringID: r.FiringID, Timestamp: r.Timestamp,
			HookName: r.HookName, Harness: r.Harness, SessionID: r.SessionID,
			Repository: r.Repository, Snippet: r.Snippet, StoreDir: r.StoreDir,
			Excluded: retired, ExcludedFor: e.Reason,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return firingaudit.Earlier(out[i].Timestamp, out[j].Timestamp) })
	return out
}

var auditContestsCmd = &cobra.Command{
	Use:   "contests",
	Short: "List the blocks agents contested, per guard",
	Long: `Contests lists, per guard, the firings a blocked agent contested with
'steer contest', and each reason it gave. It reads no case evidence.

Every count is of distinct firings. contested and unread are separate counts:
a contested firing no receipt names is in both. not_offered counts the blocks
whose message carried no contest line, so their silence is not agreement. A
contest of an excluded row, and a contest no row read here carries, are listed
apart from the counts.

A contest is a complaint, not a judgment. Read the firing with 'steer audit
show' before judging it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := loadAudit()
		if err != nil {
			return err
		}
		contests, err := adjudication.LoadContests(adjudication.Dir())
		if err != nil {
			return err
		}
		report := firingaudit.Contests(source.Rows, contests, source.Receipts,
			source.Excluded, source.Installed, auditHook)
		if auditJSON {
			return writeJSON(cmd.OutOrStdout(), report)
		}
		w := cmd.OutOrStdout()
		if !source.Present && len(contests) == 0 {
			fmt.Fprintln(w, "No guard store on this machine holds an audit stream, and no contest is recorded.")
			return nil
		}
		for _, g := range report.Guards {
			fmt.Fprintf(w, "%s  (%s)\n  firings %d  contested %d  unread %d  no contest offered %d",
				g.HookName, g.StoreDir, g.Firings, g.Contested, g.Unread, g.NotOffered)
			if g.ContestedExcluded > 0 {
				fmt.Fprintf(w, "  contested but excluded %d", g.ContestedExcluded)
			}
			fmt.Fprintln(w)
			printContested(w, g.ContestedFirings)
		}
		if len(report.Unmatched) > 0 {
			fmt.Fprintf(w, "Contests no row read here carries: %d\n", len(report.Unmatched))
			printContested(w, report.Unmatched)
		}
		return nil
	},
}

// printContested lists contested firings with every reason given for each.
func printContested(w io.Writer, firings []firingaudit.ContestedFiring) {
	for _, f := range firings {
		judgment := f.Judgment
		if judgment == "" {
			judgment = "unread"
		}
		fmt.Fprintf(w, "  firing=%s  %s  %s  %s", f.FiringID, or(f.Timestamp), or(f.Harness), judgment)
		if f.HookName != "" && f.Applies == "" {
			fmt.Fprintf(w, "  guard=%s", f.HookName)
		}
		if f.Applies != "" && f.Applies != adjudication.Current {
			fmt.Fprintf(w, "  implementation=%s", f.Applies)
		}
		fmt.Fprintln(w)
		for _, r := range f.Reasons {
			fmt.Fprintf(w, "    %s  %s\n", r.RecordedAt, oneLine(r.Reason))
		}
	}
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
	auditSubject        string
	auditFailures       []string
)

func init() {
	rootCmd.AddCommand(auditCmd)
	auditCmd.AddCommand(auditQueueCmd, auditShowCmd, auditRecordCmd,
		auditRowsCmd, auditExcludeCmd, auditReinstateCmd, auditContestsCmd)
	auditCmd.PersistentFlags().BoolVar(&auditJSON, "json", false, "emit JSON")

	q := auditQueueCmd.Flags()
	q.StringVar(&auditHook, "hook", "", "only this guard")
	q.IntVar(&auditLimit, "limit", 0, "at most this many firings (default all)")
	q.BoolVar(&auditOther, "other", false,
		"include guard rows an implementation other than the installed one recorded")
	for _, c := range []*cobra.Command{auditQueueCmd, auditShowCmd, auditRecordCmd, auditRowsCmd} {
		c.Flags().StringVar(&auditSubject, "subject", "", "guard or check (default both, or whichever the ID names)")
	}

	for _, c := range []*cobra.Command{auditShowCmd, auditRecordCmd} {
		f := c.Flags()
		f.IntVar(&auditSteps, "steps", 0, "continuation steps to read")
		f.IntVar(&auditText, "text-bytes", 0, "text kept per continuation step")
	}
	auditShowCmd.Flags().IntVar(&auditMaxBytes, "max-bytes", 0,
		"bound this command's whole output, withholding the least decisive evidence first (default unbounded)")

	r := auditRecordCmd.Flags()
	r.StringVar(&auditJudgment, "judgment", "",
		"prevented|incorrect|unresolved (a guard firing needs it)")
	r.StringArrayVar(&auditFailures, "failure", nil,
		"NAME=VALIDITY,ATTRIBUTION[,CONTINUATION] for one failure of a check block (repeatable)")
	r.StringVar(&auditRationale, "rationale", "", "why, from the action in its task context")
	r.StringArrayVar(&auditEvidence, "evidence", nil,
		"what was read, beyond the anchors record stamps itself (repeatable)")
	r.BoolVar(&auditInspected, "continuation-inspected", false,
		"the task after the block was read")
	r.IntVar(&auditInspectedSteps, "inspected-steps", 0,
		"how many continuation steps reached the reader, when show withheld some")
	r.StringVar(&auditOutcome, "continuation-outcome", "", "what the task did next")
	r.StringVar(&auditSelection, "selection", "", "why this firing was read")

	auditRowsCmd.Flags().StringVar(&auditHook, "hook", "", "only this guard")
	auditContestsCmd.Flags().StringVar(&auditHook, "hook", "", "only this guard")
	auditRowsCmd.Flags().BoolVar(&auditExcludedOnly, "excluded", false, "only the excluded rows")
	for _, c := range []*cobra.Command{auditExcludeCmd, auditReinstateCmd} {
		c.Flags().StringVar(&auditReason, "reason", "", "why (required)")
		_ = c.MarkFlagRequired("reason")
	}
	auditExcludeCmd.Flags().StringArrayVar(&auditEvidence, "evidence", nil,
		"what shows the row records no real work (repeatable, required)")
}
