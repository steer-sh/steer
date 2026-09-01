package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/denials"
	"github.com/steer-sh/steer-cli/internal/styles"
)

// denialsTimeout is the wall clock the recorder's settings.json entry carries. The
// hook parses one payload and appends one line, so the budget covers process start.
const denialsTimeout = 10

var denialsCmd = &cobra.Command{
	Use:   "denials",
	Short: "Record the tool calls auto mode's classifier blocked",
	Long: `denials keeps a corpus of the tool calls Claude Code's auto mode classifier
denied. The PermissionDenied hook fires after that decision, so the recorder observes
and never adjudicates: it returns no retry grant and cannot clear a denial.

The corpus is a per-machine JSONL shard. Rows share their field names with the
block-event audit log, so one reader covers both.`,
}

var denialsRecordCmd = &cobra.Command{
	Use:   "record",
	Short: "PermissionDenied hook: append one denial to the corpus",
	Long: `record reads a Claude Code PermissionDenied payload on stdin and appends one
row. It writes nothing on stdout and always exits 0: a recorder that reported failure
would put its own errors in front of the user on a tool call that already failed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			fmt.Fprintln(os.Stderr, "steer denials:", err)
		}
		if _, err := denials.Record(os.Stdin, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "steer denials:", err)
		}
		return nil
	},
}

var denialsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Register the recorder on the PermissionDenied channel",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prepareState(); err != nil {
			return err
		}
		target := currentScope()
		if err := claude.RegisterDenialsHook(target, "steer denials record", denialsTimeout); err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Recording auto mode denials\nScope: %s\nCorpus: %s", target, denials.LogFile())))
		return nil
	},
}

var denialsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Deregister the recorder, leaving the corpus in place",
	RunE: func(cmd *cobra.Command, args []string) error {
		target := currentScope()
		if err := claude.RemoveDenialsHook(target); err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Stopped recording auto mode denials\nScope: %s\nCorpus kept: %s",
			target, denials.LogFile())))
		return nil
	},
}

var denialsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report where the recorder is registered and how large the corpus is",
	RunE: func(cmd *cobra.Command, args []string) error {
		rows, err := denials.Read()
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Auto Mode Denials"))
		fmt.Println()
		fmt.Printf("  Corpus       %s\n", denials.LogFile())
		fmt.Printf("  Rows         %d\n", len(rows))
		if len(rows) > 0 {
			fmt.Printf("  Span         %s to %s\n", rows[0].Timestamp, rows[len(rows)-1].Timestamp)
		}
		for _, target := range []string{claude.ProjectScope, claude.GlobalScope} {
			arming, err := claude.DenialsHookArming(target)
			if err != nil {
				return err
			}
			state := "not registered"
			if arming.Registered {
				state = fmt.Sprintf("registered, matcher %q, timeout %ds",
					arming.Matcher, arming.Timeout)
			}
			fmt.Printf("  %-12s %s scope: %s\n", "PermissionDenied", target, state)
		}
		fmt.Println()
		return nil
	},
}

var denialsListLimit int
var denialsListByTool bool

var denialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the recorded denials",
	RunE: func(cmd *cobra.Command, args []string) error {
		rows, err := denials.Read()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Println()
			fmt.Println(styles.InfoText.Render(fmt.Sprintf(
				"No denials recorded yet (%s)", denials.LogFile())))
			return nil
		}
		fmt.Println()
		if denialsListByTool {
			counts := map[string]int{}
			for _, row := range rows {
				counts[row.ToolName]++
			}
			names := make([]string, 0, len(counts))
			for name := range counts {
				names = append(names, name)
			}
			sort.Slice(names, func(i, j int) bool {
				if counts[names[i]] != counts[names[j]] {
					return counts[names[i]] > counts[names[j]]
				}
				return names[i] < names[j]
			})
			for _, name := range names {
				fmt.Printf("  %4d  %s\n", counts[name], name)
			}
			fmt.Println()
			return nil
		}
		start := 0
		if denialsListLimit > 0 && len(rows) > denialsListLimit {
			start = len(rows) - denialsListLimit
		}
		for _, row := range rows[start:] {
			fmt.Printf("  %s  %-8s %s\n", row.Timestamp, row.ToolName, filepath.Base(row.CWD))
			if snippet := oneLine(row.Snippet); snippet != "" {
				fmt.Printf("        %s\n", snippet)
			}
		}
		fmt.Println()
		return nil
	},
}

// oneLine flattens a snippet so a multi-line command stays on one row of the list.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func init() {
	rootCmd.AddCommand(denialsCmd)
	denialsCmd.AddCommand(denialsRecordCmd, denialsInstallCmd, denialsRemoveCmd,
		denialsStatusCmd, denialsListCmd)

	denialsListCmd.Flags().IntVar(&denialsListLimit, "limit", 20,
		"Show at most this many of the most recent denials; 0 shows all")
	denialsListCmd.Flags().BoolVar(&denialsListByTool, "by-tool", false,
		"Count denials per tool instead of listing them")
}
