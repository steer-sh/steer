package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/styles"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Manage repository checks — executable tests the turn-end oracle runs",
	Long: `A repository check is an executable test of repository content. The
repository's check.sh runs every blocking check; Steer manages individual checks
inside it, declared in .steer/checks/manifest.json.

Read the authoring guide before writing a check: steer check guide`,
}

var checkGuideCmd = &cobra.Command{
	Use:   "guide",
	Short: "Print the instructions for authoring repository checks",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprint(cmd.OutOrStdout(), checks.Guide())
		return nil
	},
}

var checkInitCmd = &cobra.Command{
	Use:   "init [repo]",
	Short: "Create the repository's check manifest, keeping an existing one",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		root, err := repoRootOf(dir)
		if err != nil {
			return err
		}
		m, created, err := checks.Init(root)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, checks.ManifestPath(root))
		if created {
			fmt.Fprintln(cmd.OutOrStdout(), styles.SuccessBox.Render(fmt.Sprintf(
				"Created %s\nRepository: %s", rel, root)))
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s already exists in %s with %d checks; left unchanged.\n",
			rel, root, len(m.Checks))
		return nil
	},
}

var checkAddCmd = &cobra.Command{
	Use:   "add <id> (--script <path> | --go-package <dir> --go-test <name>)",
	Short: "Validate a check against its cases, then declare it",
	Long: `Replays .steer/checks/cases/<id>.json against the check and declares it in
the manifest only when every case matched. The run is recorded in
machine-local evidence whatever its verdict.

A script binding runs a repository script. A go-test binding names one
top-level test in the repository's Go suite by its package directory and name.`,
	Example: `  steer check add no-count --script .steer/checks/no-count.sh
  steer check add no-count --script scripts/no-count.sh --input scripts/lib.sh
  steer check add json-contract --go-package cli/internal/status --go-test TestJSONContractIsStable`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		script, _ := cmd.Flags().GetString("script")
		goPackage, _ := cmd.Flags().GetString("go-package")
		goTest, _ := cmd.Flags().GetString("go-test")
		inputs, _ := cmd.Flags().GetStringSlice("input")
		d := checks.Declaration{ID: args[0], Inputs: inputs}
		switch {
		case script != "" && goPackage == "" && goTest == "":
			d.Kind, d.Script = checks.KindScript, script
		case script == "" && goPackage != "" && goTest != "":
			d.Kind, d.Package, d.Test = checks.KindGoTest, goPackage, goTest
		default:
			return fmt.Errorf("name one binding: --script, or --go-package with --go-test")
		}
		run, err := checks.Add(root, d)
		printValidation(cmd, run)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), styles.SuccessBox.Render(fmt.Sprintf("Declared check %s", d.ID)))
		return nil
	},
}

var checkTestCmd = &cobra.Command{
	Use:   "test <id>",
	Short: "Replay a declared check's cases with its current detector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		run, err := checks.Test(root, args[0])
		printValidation(cmd, run)
		return err
	},
}

var checkListCmd = &cobra.Command{
	Use:   "list",
	Short: "List declared checks and the freshness of their validation",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		m, err := checks.Load(root)
		if err != nil {
			return err
		}
		if len(m.Checks) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No checks declared.")
			return nil
		}
		history, err := checks.History(root)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for _, d := range m.Checks {
			f := checks.FreshnessOf(root, d, history)
			fmt.Fprintf(out, "%s  %s %s\n", d.ID, d.Kind, bindingTarget(d))
			fmt.Fprintf(out, "  validation: %s\n", freshnessPhrase(f))
			if f.Receipt != nil {
				if shas := testedSnapshots(f.Receipt); len(shas) > 0 {
					fmt.Fprintf(out, "  tested snapshot: %s\n", strings.Join(shas, ", "))
				}
			}
			if det, err := checks.DetectorOf(root, d); err == nil && len(det.Unresolved) > 0 {
				fmt.Fprintf(out, "  not tracked: %s\n", strings.Join(det.Unresolved, "; "))
			}
			if f.Latest != nil && f.Latest != f.Receipt {
				fmt.Fprintf(out, "  latest replay: %s at %s\n", f.Latest.Verdict, f.Latest.Finished.Local().Format("2006-01-02 15:04"))
			}
		}
		return nil
	},
}

var checkRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Drop a check from the manifest, keeping its cases and evidence",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		d, err := checks.Remove(root, args[0])
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, checks.CasesPath(root, d.ID))
		fmt.Fprintln(cmd.OutOrStdout(), styles.SuccessBox.Render(fmt.Sprintf(
			"Removed check %s\nKept: %s and its validation history", d.ID, rel)))
		return nil
	},
}

func bindingTarget(d checks.Declaration) string {
	if d.Kind == checks.KindGoTest {
		return d.Package + " " + d.Test
	}
	return d.Script
}

func freshnessPhrase(f checks.Freshness) string {
	switch f.State {
	case checks.FreshCurrent:
		return fmt.Sprintf("validated %s; tracked detector inputs unchanged", f.Receipt.Finished.Local().Format("2006-01-02 15:04"))
	case checks.FreshStale:
		phrase := fmt.Sprintf("stale since %s: %s changed", f.Receipt.Finished.Local().Format("2006-01-02 15:04"), strings.Join(f.Changed, ", "))
		if f.Reason != "" {
			phrase += " (" + f.Reason + ")"
		}
		return phrase
	default:
		return "never validated"
	}
}

func printValidation(cmd *cobra.Command, run checks.ValidationRun) {
	if run.CheckID == "" {
		return
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Validation of %s: %s\n", run.CheckID, run.Verdict)
	if run.Reason != "" {
		fmt.Fprintf(out, "  %s\n", run.Reason)
	}
	if len(run.Detector.Unresolved) > 0 {
		fmt.Fprintf(out, "  not tracked by the detector digest: %s\n", strings.Join(run.Detector.Unresolved, "; "))
	}
	for _, c := range run.Cases {
		observed := "no exit"
		if c.ObservedExit != nil {
			observed = fmt.Sprintf("exit %d", *c.ObservedExit)
		}
		fmt.Fprintf(out, "  %-10s %s (expected exit %d, %s)", c.Outcome, c.Name, c.ExpectedExit, observed)
		if c.Snapshot != "" {
			fmt.Fprintf(out, " at snapshot %s", shortHash(c.Snapshot))
		}
		if c.Reason != "" {
			fmt.Fprintf(out, ": %s", c.Reason)
		}
		if label := causeLabel(c.Cause); label != "" {
			fmt.Fprintf(out, " [%s]", label)
		}
		fmt.Fprintln(out)
		if c.Outcome != checks.OutcomeMatched && strings.TrimSpace(c.Excerpt) != "" {
			for _, line := range lastLines(c.Excerpt, 8) {
				fmt.Fprintf(out, "      | %s\n", line)
			}
		}
	}
}

func causeLabel(cause string) string {
	switch cause {
	case checks.CauseIncompatible:
		return "incompatible case: author it again"
	case checks.CauseEnvironment:
		return "environment unavailable"
	case checks.CauseSnapshotUnavailable:
		return "snapshot unavailable"
	}
	return ""
}

func shortHash(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// testedSnapshots lists the distinct snapshot commits a run's cases started from.
func testedSnapshots(run *checks.ValidationRun) []string {
	var shas []string
	seen := map[string]bool{}
	for _, c := range run.Cases {
		if c.Snapshot != "" && !seen[c.Snapshot] {
			seen[c.Snapshot] = true
			shas = append(shas, shortHash(c.Snapshot))
		}
	}
	return shas
}

func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// repoRootOf returns the top level of the Git work tree containing dir.
func repoRootOf(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository: %s", dir, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func init() {
	rootCmd.AddCommand(checkCmd)
	checkCmd.AddCommand(checkGuideCmd, checkInitCmd, checkAddCmd, checkTestCmd, checkListCmd, checkRemoveCmd)
	checkAddCmd.Flags().String("script", "", "repository-relative script the check runs")
	checkAddCmd.Flags().String("go-package", "", "repository-relative directory of the Go test's package")
	checkAddCmd.Flags().String("go-test", "", "exact name of the top-level Go test function")
	checkAddCmd.Flags().StringSlice("input", nil, "further repository-relative detector file (repeatable)")
}
