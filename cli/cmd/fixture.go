package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var fixtureMineDiff string
var fixtureMineSession string
var fixtureMinePath string
var fixtureMineFinding string
var fixtureOut string

// fixtureLabelWidth is the label column for one case's stacked lines.
const fixtureLabelWidth = 11

var fixtureCmd = &cobra.Command{
	Use:   "fixture",
	Short: "Store, replay and mine a guard's test cases",
	Long: `Fixture manages the replayable cases stored beside a guard.

A case is a hook payload plus the outcome it asserts. Stored cases are rerun
whenever the store changes, so a guard's test is evidence rather than a count.`,
}

var fixtureRunCmd = &cobra.Command{
	Use:   "run <hook-name-or-path>",
	Short: "Replay a guard's stored fixtures",
	Long: `Run replays a guard's stored cases twice: against the guard alone, which is
its whole contract, and against the generated master, which is where a guard
actually runs.

Both passes use byte-identical copies in a temporary store, so the operator's
audit log records no synthetic firing.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStoredFixtures(currentScope(), args[0])
	},
}

var fixtureMineCmd = &cobra.Command{
	Use:   "mine",
	Short: "Reconstruct candidate cases from a stored diff or a transcript",
	Long: `Mine reads a corpus and prints candidate payloads as JSON.

It prints CANDIDATES, never labelled cases. No corpus stores the payload a guard
saw, so every candidate is a reconstruction; and a finding's filename narrows
which hunks could be violations without making any of them one. Selecting the
violating hunk, labelling it and writing the fixture file is the reader's work.`,
	Example: `  steer fixture mine --diff 1737000000-abc-r1.diff --path cli/main.go
  steer fixture mine --diff 1737000000-abc-r1.diff --finding review.txt
  steer fixture mine --session 0f2c1e4a-... -o candidates.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		diff := strings.TrimSpace(fixtureMineDiff)
		session := strings.TrimSpace(fixtureMineSession)
		path := strings.TrimSpace(fixtureMinePath)
		finding := strings.TrimSpace(fixtureMineFinding)
		if (diff == "") == (session == "") {
			return fmt.Errorf("provide exactly one of --diff or --session")
		}
		if path != "" && finding != "" {
			return fmt.Errorf("provide --path or --finding, not both: --finding reads the file out of the finding itself")
		}
		if finding != "" && session != "" {
			return fmt.Errorf("--finding narrows the hunks of a stored diff, so it needs --diff")
		}
		if diff != "" && finding != "" {
			return mineDiffByFindings(diff, finding)
		}
		if diff != "" {
			return mineDiff(diff, path)
		}
		return mineSession(session)
	},
}

// runStoredFixtures replays one guard's cases and prints both passes.
func runStoredFixtures(scope, identifier string) error {
	hook, err := claude.ResolveHook(scope, identifier)
	if err != nil {
		return err
	}
	file, ok, err := claude.LoadFixtures(scope, hook.Name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no stored fixtures for %s: register it with --fixtures, or use steer test --code/--file", hook.Name)
	}

	result, err := claude.VerifyHookFixtures(scope, hook.Name)
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("no stored fixtures for %s", hook.Name)
	}

	fmt.Println()
	fmt.Println(styles.SectionHeader.Render(fmt.Sprintf("Fixtures: %s (%d cases)", hook.Name, len(file.Cases))))
	fmt.Println()
	width := styles.Width()
	for _, c := range result.Cases {
		fmt.Print(styles.Field("case", []string{c.Name}, "  ", fixtureLabelWidth, width))
		fmt.Print(styles.Field("expected", []string{c.Expect}, "  ", fixtureLabelWidth, width))
		fmt.Print(styles.Field("guard alone", []string{passWord(c.IndividualPass, string(c.Individual))}, "  ", fixtureLabelWidth, width))
		fmt.Print(styles.Field("master", []string{masterWord(c)}, "  ", fixtureLabelWidth, width))
		if c.Err != "" {
			fmt.Print(styles.Field("error", []string{c.Err}, "  ", fixtureLabelWidth, width))
		}
		fmt.Println()
	}

	if result.Tested() {
		text := "The guard intercepts its stored cases.\nThis says nothing about a field block being correct."
		if n := result.Shadowed(); n > 0 {
			text += fmt.Sprintf("\n%d case(s) another guard caught first, so this guard is not shown to run through the master.", n)
		}
		fmt.Println(styles.SuccessBox.Render(text))
		return nil
	}
	fmt.Println(styles.ErrorBox.Render(fmt.Sprintf(
		"Not verified: %d case(s) the guard answered wrongly, %d the master allowed.",
		result.IndividualFailures(), result.MasterFailures())))
	return fmt.Errorf("fixture verification failed for %s", hook.Name)
}

func passWord(pass bool, outcome string) string {
	if pass {
		return outcome + " · as expected"
	}
	return outcome + " · not what the case expects"
}

func masterWord(c fixture.CaseResult) string {
	switch c.MasterState {
	case fixture.MasterVerified:
		return "blocked by this guard"
	case fixture.MasterShadowed:
		return "blocked first by " + c.MasterObjector + " · this guard is not shown to run"
	case fixture.MasterFailed:
		return "allowed · the guard blocks it alone"
	case fixture.MasterNotRun:
		if c.Master == "" {
			return "not asserted · another guard may legitimately block it"
		}
		return string(c.Master) + " · not asserted, another guard may legitimately block it"
	default:
		return "not established"
	}
}

// readStoredDiff resolves a ledger row's diff_file, or a path, and parses it.
func readStoredDiff(name string) (string, []fixture.Hunk, fixture.Truncation, error) {
	source := name
	if !filepath.IsAbs(source) && !strings.ContainsRune(source, filepath.Separator) {
		source = filepath.Join(gateConfig().DiffDir(), name)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", nil, fixture.Truncation{}, fmt.Errorf("failed to read the stored diff: %w", err)
	}
	hunks, trunc, err := fixture.ParseDiff(string(data))
	if err != nil {
		return "", nil, fixture.Truncation{}, err
	}
	return source, hunks, trunc, nil
}

// readFindingText takes the flag value as a file when one exists at that path,
// and as the finding text otherwise.
func readFindingText(value string) (string, error) {
	if info, err := os.Stat(value); err == nil && !info.IsDir() {
		data, err := os.ReadFile(value)
		if err != nil {
			return "", fmt.Errorf("failed to read the finding: %w", err)
		}
		return string(data), nil
	}
	return value, nil
}

// mineDiffByFindings groups the candidates by the finding that narrows them.
// Each finding is its own group: a reply holding several findings names several
// files, and pooling them would let one finding's file answer for another.
func mineDiffByFindings(name, findingValue string) error {
	text, err := readFindingText(findingValue)
	if err != nil {
		return err
	}
	findings, err := gate.ParseFindings(text)
	if err != nil {
		return fmt.Errorf("failed to read the finding: %w", err)
	}
	if len(findings) == 0 {
		return fmt.Errorf("the finding text holds no finding block, so it narrows nothing")
	}

	source, hunks, trunc, err := readStoredDiff(name)
	if err != nil {
		return err
	}

	groups := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		cited := gate.CleanPath(f.File)
		group := map[string]any{
			"index":      f.Index,
			"severity":   f.Severity,
			"file":       cited,
			"claim":      f.Claim,
			"fails_when": f.FailsWhen,
		}
		if !fixture.Complete(hunks, trunc, cited) {
			group["complete"] = false
			group["candidates"] = []map[string]any{}
			group["reason"] = fmt.Sprintf("the stored diff does not carry %s: the review harness dropped it to fit its budget, so no fixture can be built from it", cited)
		} else {
			group["complete"] = true
			group["candidates"] = candidateRows(fixture.HunksFor(hunks, cited), &fixture.Source{
				Corpus:       "review-findings",
				DiffFile:     filepath.Base(source),
				FindingIndex: f.Index,
				FindingClaim: f.Claim,
			})
		}
		groups = append(groups, group)
	}

	return emitJSON(map[string]any{
		"source":        source,
		"dropped_files": trunc.DroppedFiles,
		"stat_only":     trunc.StatOnly,
		"findings":      groups,
	})
}

func mineDiff(name, path string) error {
	source, hunks, trunc, err := readStoredDiff(name)
	if err != nil {
		return err
	}
	selected := hunks
	if path != "" {
		selected = fixture.HunksFor(hunks, path)
		if !fixture.Complete(hunks, trunc, path) {
			return fmt.Errorf("the stored diff does not carry %s: the review harness dropped it to fit its budget, so no fixture can be built from it", path)
		}
	}

	out := map[string]any{
		"source":        source,
		"dropped_files": trunc.DroppedFiles,
		"stat_only":     trunc.StatOnly,
		"candidates":    candidateRows(selected, nil),
	}
	return emitJSON(out)
}

func candidateRows(hunks []fixture.Hunk, src *fixture.Source) []map[string]any {
	rows := make([]map[string]any, 0, len(hunks))
	for _, h := range hunks {
		row := map[string]any{
			"path":     h.Path,
			"hunk":     h.Index,
			"header":   h.Header,
			"new_file": h.NewFile,
			"origin":   fixture.OriginReconstructed,
		}
		if src != nil {
			row["source"] = *src
		}
		if h.Skip != "" {
			row["skip"] = h.Skip
		} else {
			row["payload"] = h.Payload
		}
		rows = append(rows, row)
	}
	return rows
}

func mineSession(id string) error {
	calls, err := fixture.FromSession(report.Adapters(harness.Unknown), id)
	if err != nil {
		return err
	}
	rows := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		rows = append(rows, map[string]any{
			"command": call.Command,
			"payload": call.Payload,
			"origin":  fixture.OriginReconstructed,
		})
	}
	return emitJSON(map[string]any{"session": id, "candidates": rows})
}

func emitJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if strings.TrimSpace(fixtureOut) != "" {
		return os.WriteFile(fixtureOut, data, 0o644)
	}
	_, err = os.Stdout.Write(data)
	return err
}

func init() {
	rootCmd.AddCommand(fixtureCmd)
	fixtureCmd.AddCommand(fixtureRunCmd)
	fixtureCmd.AddCommand(fixtureMineCmd)
	fixtureMineCmd.Flags().StringVar(&fixtureMineDiff, "diff", "", "A ledger row's diff_file, or a path to a stored diff")
	fixtureMineCmd.Flags().StringVar(&fixtureMineSession, "session", "", "A session id to re-read from the harnesses' transcripts")
	fixtureMineCmd.Flags().StringVar(&fixtureMinePath, "path", "", "The file a finding names, narrowing the candidate hunks")
	fixtureMineCmd.Flags().StringVar(&fixtureMineFinding, "finding", "", "A reviewer finding, as text or a file, whose FILE: narrows the candidate hunks")
	fixtureMineCmd.Flags().StringVarP(&fixtureOut, "out", "o", "", "Write the candidates to a file instead of stdout")
}
