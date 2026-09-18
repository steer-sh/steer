package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/status"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/tier"
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

var checkRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run check.sh once under a new run ID and report each declared check",
	Long: `Runs the repository's check.sh with bash from its root, bounded by --timeout,
and records its outcome apart from the per-check records its executors write.
A declared check that no executor reported under the run is recorded as not
executed; when check.sh passed anyway, that is an integration failure.
Exits non-zero unless check.sh passed and every declared check reported.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		run, records, err := checks.RunOracle(root, checks.RunOptions{
			Source: checks.SourceRun, Timeout: timeout, Stdout: os.Stdout, Stderr: os.Stderr, SteerBin: steerBinForOracle(),
		})
		if err != nil {
			return err
		}
		printOracleRun(cmd, run, records)
		if !run.Passed() {
			code := 1
			if run.ExitCode != nil && *run.ExitCode != 0 {
				code = *run.ExitCode
			}
			os.Exit(code)
		}
		return nil
	},
}

var checkAdoptCmd = &cobra.Command{
	Use:   "adopt [repo]",
	Short: "Authorize automatic check.sh runs for this repository on this machine",
	Long: `Records machine-local authorization for the repository's resolved root.
A declared check without a current receipt is replayed first; then check.sh runs
with isolated evidence and must pass with every declared check reported.
Nothing is recorded as live execution. A linked worktree of an adopted checkout
is adopted with it unless it was unadopted itself. A clone elsewhere is not
adopted.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		root, err := repoRootOf(dir)
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		res, err := checks.Adopt(root, checks.AdoptOptions{
			Timeout: timeout, Stdout: os.Stdout, Stderr: os.Stderr, SteerBin: steerBinForOracle(),
		})
		out := cmd.OutOrStdout()
		printAdoption(cmd, root, res)
		if res.Already {
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, styles.SuccessBox.Render(fmt.Sprintf(
			"Adopted %s\nAutomatic check.sh runs are authorized for this root and its linked worktrees on this machine.", res.Adoption.Repo)))
		return nil
	},
}

var checkUnadoptCmd = &cobra.Command{
	Use:   "unadopt [repo]",
	Short: "Withdraw automatic check.sh runs for this repository, keeping its evidence",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		identity, err := adoptionIdentity(dir)
		if err != nil {
			return err
		}
		removed, err := checks.Unadopt(identity)
		if err != nil {
			return err
		}
		if !removed {
			fmt.Fprintf(cmd.OutOrStdout(), "%s is not adopted on this machine.\n", identity)
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), styles.SuccessBox.Render(fmt.Sprintf(
			"Unadopted %s\nKept: its checks, validation history and execution evidence", identity)))
		if main, ok := checks.MainCheckout(identity); ok {
			if _, adopted, err := checks.AdoptionOf(main); err == nil && adopted {
				fmt.Fprintf(cmd.OutOrStdout(), "This worktree only: %s and its other worktrees keep their adoption.\n", main)
			}
		}
		return nil
	},
}

var checkHookCmd = &cobra.Command{
	Use:   "hook --harness <claude-code|codex>",
	Short: "Run check.sh for a harness's root Stop event and reply in its hook format",
	Long: `Reads a Stop payload on stdin. In an adopted repository it runs check.sh the
way ` + "`steer check run`" + ` does, with source stop, and blocks the turn on a failure.
The same failure on an unchanged tree for the same session then lets the turn
end. A missing approval, check.sh or bash, a malformed payload, a timeout, a
run already in progress, or an execution error lets the turn end with a notice
to the operator. Subagent Stop events are ignored. Always exits 0.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("harness")
		target, err := blockingHarness(name)
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			raw = nil
		}
		_, reply := checks.StopHook(string(target), raw, checks.StopHookOptions{Timeout: timeout, SteerBin: steerBinForOracle()})
		if reply == nil {
			return nil
		}
		encoded, err := json.Marshal(reply)
		if err != nil {
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
		return nil
	},
}

// adoptionIdentity resolves dir to the identity adoption keys on. A directory
// that no longer resolves falls back to its absolute path, so a deleted
// checkout can still be unadopted.
func adoptionIdentity(dir string) (string, error) {
	if root, err := repoRootOf(dir); err == nil {
		return checks.RepoIdentity(root)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// steerBinForOracle is the STEER_BIN that check.sh receives.
func steerBinForOracle() string { return checks.SelfSteerBin() }

// printAdoption prints what an adoption attempt did: the receipt it reused or
// replayed for each declared check, and its isolated oracle run.
func printAdoption(cmd *cobra.Command, root string, res checks.AdoptResult) {
	out := cmd.OutOrStdout()
	if res.Already {
		since := res.Adoption.At.Local().Format("2006-01-02 15:04")
		if identity, err := checks.RepoIdentity(root); err == nil && identity != res.Adoption.Repo {
			fmt.Fprintf(out, "%s is a worktree of %s, adopted on this machine since %s.\n", identity, res.Adoption.Repo, since)
			return
		}
		fmt.Fprintf(out, "%s is already adopted on this machine since %s.\n", res.Adoption.Repo, since)
		return
	}
	for _, v := range res.Validations {
		if v.Reused {
			fmt.Fprintf(out, "Validation of %s: current receipt from %s reused\n", v.CheckID, v.Run.Finished.Local().Format("2006-01-02 15:04"))
			continue
		}
		printValidation(cmd, v.Run)
	}
	if res.Oracle.RunID != "" {
		printOracleRun(cmd, res.Oracle, res.Executions)
	}
}

func printOracleRun(cmd *cobra.Command, run checks.OracleRun, records []checks.Execution) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s %s in %s (run %s)", checks.OracleName, run.Outcome, run.Finished.Sub(run.Started).Round(time.Millisecond), run.RunID)
	if run.Reason != "" {
		fmt.Fprintf(out, ": %s", run.Reason)
	}
	fmt.Fprintln(out)
	for _, e := range records {
		fmt.Fprintf(out, "  %-14s %s", e.Outcome, e.CheckID)
		if e.Reason != "" {
			fmt.Fprintf(out, ": %s", e.Reason)
		}
		fmt.Fprintln(out)
	}
	if run.IntegrationFailure {
		fmt.Fprintf(out, "Integration failure: %s passed without running %s. Add the managed lines to its sequence (see `steer check guide`):\n  %s\n",
			checks.OracleName, strings.Join(run.Missing, ", "), checks.ExecInvocation)
	}
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
		// A repository with no manifest is an error here; StateOf tolerates one,
		// because a repository is adoptable before it declares a check.
		if _, err := checks.Load(root); err != nil {
			return err
		}
		st, err := checks.StateOf(root)
		if err != nil {
			return err
		}
		printRepositoryState(cmd, st)
		if len(st.Checks) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No checks declared.")
			return nil
		}
		out := cmd.OutOrStdout()
		for _, cs := range st.Checks {
			d, f := cs.Declaration, cs.Freshness
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
			if e := cs.Execution; e != nil {
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
		reason, _ := cmd.Flags().GetString("reason")
		d, archivePath, err := checks.Remove(root, args[0], strings.TrimSpace(reason))
		if errors.Is(err, checks.ErrNotDeclared) {
			return fmt.Errorf("%w\n%s", err, otherTierRemoveHint(tier.Check, args[0], currentScope()))
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, checks.CasesPath(root, d.ID))
		archived, _ := filepath.Rel(root, archivePath)
		fmt.Fprintln(cmd.OutOrStdout(), styles.SuccessBox.Render(fmt.Sprintf(
			"Removed check %s\nKept: %s and its validation history\nArchived: %s", d.ID, rel, archived)))
		if checks.OracleNamesCheck(root, d.ID) {
			fmt.Fprintf(cmd.OutOrStdout(), "%s runs `check exec %s` by name, and that line now fails. Remove %s from it.\n",
				checks.OracleName, d.ID, d.ID)
		}
		return nil
	},
}

// printRepositoryState prints the repository's adoption on this machine and
// the latest live check.sh run that did any work.
func printRepositoryState(cmd *cobra.Command, st checks.RepositoryState) {
	out := cmd.OutOrStdout()
	if st.Adopted {
		fmt.Fprintf(out, "Adoption: adopted on this machine since %s\n",
			st.Adoption.At.Local().Format("2006-01-02 15:04"))
	} else {
		fmt.Fprintln(out, "Adoption: not adopted on this machine (steer check adopt)")
	}
	printRunnerWiring(out)
	fmt.Fprintf(out, "Latest %s run: %s\n", checks.OracleName,
		status.CheckRunPhrase(status.CheckRunOf(st.Latest, st.Refused)))
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
func repoRootOf(dir string) (string, error) { return checks.RepoRootOf(dir) }

func init() {
	rootCmd.AddCommand(checkCmd)
	checkCmd.AddCommand(checkGuideCmd, checkInitCmd, checkAddCmd, checkTestCmd, checkListCmd, checkRemoveCmd,
		checkExecCmd, checkGoTestCmd, checkRunCmd, checkAdoptCmd, checkUnadoptCmd, checkHookCmd)
	checkHookCmd.Flags().String("harness", "", "the harness sending the payload: claude-code or codex")
	_ = checkHookCmd.MarkFlagRequired("harness")
	checkHookCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "total bound on check.sh; its process group is stopped at the deadline")
	checkExecCmd.Flags().Bool("all", false, "run every script check in the manifest")
	checkExecCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "bound on this run; a parent deadline that is earlier wins")
	checkGoTestCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "bound on this run; a parent deadline that is earlier wins")
	checkRunCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "total bound on check.sh; its process group is stopped at the deadline")
	checkAdoptCmd.Flags().Duration("timeout", checks.DefaultExecTimeout, "total bound on the isolated check.sh run")
	checkAddCmd.Flags().String("script", "", "repository-relative script the check runs")
	checkAddCmd.Flags().String("go-package", "", "repository-relative directory of the Go test's package")
	checkAddCmd.Flags().String("go-test", "", "exact name of the top-level Go test function")
	checkAddCmd.Flags().StringSlice("input", nil, "further repository-relative detector file (repeatable)")
	checkRemoveCmd.Flags().String("reason", "", "why the check is removed; the archive entry keeps it")
}
