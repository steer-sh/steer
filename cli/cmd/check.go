package cmd

import (
	"fmt"
	"os"
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
	Short: "Create the check manifest and a check.sh scaffold, keeping existing files",
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
		scaffolded, err := checks.WriteScaffold(root)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		rel, _ := filepath.Rel(root, checks.ManifestPath(root))
		var made []string
		if created {
			made = append(made, "Created "+rel)
		} else {
			fmt.Fprintf(out, "%s already exists with %d checks; left unchanged.\n", rel, len(m.Checks))
		}
		if scaffolded {
			made = append(made, "Created "+checks.OracleName)
		} else if !checks.OracleRunsExec(root) {
			fmt.Fprintf(out, "%s exists and was left unchanged. Add the managed checks to its sequence:\n  %s\nSee `steer check guide` for the full integration.\n",
				checks.OracleName, checks.ExecInvocation)
		}
		if len(made) > 0 {
			made = append(made, "Repository: "+root)
			fmt.Fprintln(out, styles.SuccessBox.Render(strings.Join(made, "\n")))
		}
		return nil
	},
}

var checkExecCmd = &cobra.Command{
	Use:   "exec (--all | <id>...)",
	Short: "Run managed script checks and record each outcome",
	Long: `Runs script checks one after another from the repository root and records
each outcome in machine-local execution evidence. A failing check does not stop
the checks after it. Exits non-zero when any check did not pass.

check.sh calls this; go-test bindings run through ` + "`steer check go-test`" + ` instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		if all == (len(args) > 0) {
			return fmt.Errorf("name the checks to run, or pass --all")
		}
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		inv, err := checks.InvocationFromEnv()
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		records, err := checks.Exec(root, checks.ExecOptions{
			IDs: args, Stdout: os.Stdout, Stderr: os.Stderr, Invocation: inv, Timeout: timeout,
		})
		if err != nil && records == nil {
			return err
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "steer check exec:", err)
		}
		failed := 0
		for _, r := range records {
			if !r.Passed() {
				failed++
			}
		}
		if failed > 0 {
			fmt.Fprintf(os.Stderr, "steer check exec: %d of %d checks did not pass\n", failed, len(records))
			os.Exit(1)
		}
		return nil
	},
}

var checkGoTestCmd = &cobra.Command{
	Use:   "go-test [-- <go test arguments>]",
	Short: "Run the Go suite once and record each bound test's result",
	Long: `Runs ` + "`go test -json`" + ` with the given arguments, prints the build errors,
failing test output and package results that plain ` + "`go test`" + ` prints, and
exits with its status. Each go-test binding whose
package ran is recorded from that run; no test runs a second time.

check.sh calls this in place of ` + "`go test`" + `. Put go test flags after --.`,
	Example: `  steer check go-test -- ./...
  steer check go-test -- -race ./cli/...`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		inv, err := checks.InvocationFromEnv()
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		code, records, err := checks.RunGoSuite(root, checks.GoSuiteOptions{
			Dir: cwd, Args: args, Stdout: os.Stdout, Stderr: os.Stderr, Invocation: inv, Timeout: timeout,
		})
		if err != nil && records == nil {
			return err
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "steer check go-test:", err)
		}
		if code != 0 {
			os.Exit(code)
		}
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
		latest, err := checks.LatestExecutions(root)
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
			if e, ok := latest[d.ID]; ok {
				fmt.Fprintf(out, "  latest execution: %s at %s (%s)\n", e.Outcome, e.Finished.Local().Format("2006-01-02 15:04"), e.Source)
			} else {
				fmt.Fprintln(out, "  latest execution: none recorded on this machine")
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
	checkCmd.AddCommand(checkGuideCmd, checkInitCmd, checkAddCmd, checkTestCmd, checkListCmd, checkRemoveCmd,
		checkExecCmd, checkGoTestCmd)
	checkExecCmd.Flags().Bool("all", false, "run every script check in the manifest")
	checkExecCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "bound on this run; a parent deadline that is earlier wins")
	checkGoTestCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "bound on this run; a parent deadline that is earlier wins")
	checkAddCmd.Flags().String("script", "", "repository-relative script the check runs")
	checkAddCmd.Flags().String("go-package", "", "repository-relative directory of the Go test's package")
	checkAddCmd.Flags().String("go-test", "", "exact name of the top-level Go test function")
	checkAddCmd.Flags().StringSlice("input", nil, "further repository-relative detector file (repeatable)")
}
