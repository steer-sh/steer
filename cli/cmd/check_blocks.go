package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
)

var checkBlocksCmd = &cobra.Command{
	Use:   "blocks",
	Short: "List the turns check.sh blocked here and what the records show next",
	Long: `blocks lists each Stop block recorded on this machine for the current
repository: where its reason landed in the transcript, the session's next Stop and
whether the tree changed, the next check.sh run, and for each check that did not
pass, its next result and whether its detector changed first.

It reports what happened after a block, not whether the block was correct; read
the transcript for that.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := repoRootOf(".")
		if err != nil {
			return err
		}
		followups, err := checks.BlockFollowups(root)
		if err != nil {
			return err
		}
		writeBlockFollowups(cmd.OutOrStdout(), followups)
		return nil
	},
}

func writeBlockFollowups(out io.Writer, followups []checks.BlockFollowup) {
	if len(followups) == 0 {
		fmt.Fprintln(out, "No Stop blocks recorded on this machine.")
		return
	}
	for i, f := range followups {
		if i > 0 {
			fmt.Fprintln(out)
		}
		b := f.Block
		fmt.Fprintf(out, "%s  run %s blocked a %s turn (session %s)\n", localTime(b.Started), b.RunID, b.Harness, b.Session)
		fmt.Fprintf(out, "  transcript: %s\n", transcriptPhrase(b))
		if s := f.NextStop; s != nil {
			fmt.Fprintf(out, "  next Stop in this session: %s at %s; %s\n", s.Disposition, localTime(s.Started), sessionTreePhrase(f.SessionTree))
		} else {
			fmt.Fprintln(out, "  next Stop in this session: none recorded")
		}
		if r := f.NextRun; r != nil {
			fmt.Fprintf(out, "  next check.sh run: %s at %s (%s)\n", r.Outcome, localTime(r.Started), runOrigin(*r))
		} else {
			fmt.Fprintln(out, "  next check.sh run: none recorded")
		}
		for _, c := range f.Checks {
			fmt.Fprintf(out, "  %s: %s at the block; %s\n", c.Blocked.CheckID, c.Blocked.Outcome, checkFollowPhrase(c))
		}
	}
}

func localTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

func transcriptPhrase(r checks.OracleRun) string {
	path, ok := r.Locators["transcript_path"]
	if !ok {
		return "not recorded"
	}
	for _, key := range []string{"prompt_id", "turn_id"} {
		if turn, ok := r.Locators[key]; ok && turn != "" {
			return fmt.Sprintf("%s, turn %s", path, turn)
		}
	}
	return path
}

func sessionTreePhrase(state string) string {
	switch state {
	case checks.FollowChanged:
		return "the tree changed"
	case checks.FollowUnchanged:
		return "the tree was unchanged"
	default:
		return "no tree comparison (a run recorded none)"
	}
}

func runOrigin(r checks.OracleRun) string {
	parts := []string{r.Source}
	if r.Harness != "" {
		parts = append(parts, r.Harness)
	}
	if r.Session != "" {
		parts = append(parts, "session "+r.Session)
	}
	return strings.Join(parts, ", ")
}

func checkFollowPhrase(c checks.CheckFollowup) string {
	if c.Next == nil {
		return "no later result recorded"
	}
	next := fmt.Sprintf("next %s at %s", c.Next.Outcome, localTime(c.Next.Started))
	switch {
	case c.NextPass == nil:
		return next + "; no pass recorded since"
	case c.Detector == checks.FollowChanged:
		return next + "; the detector changed before the recorded pass"
	case c.Detector == checks.FollowUnchanged:
		return next + "; the detector was unchanged at the recorded pass"
	default:
		return next + "; no detector comparison (a record carries no digest)"
	}
}

func init() {
	checkCmd.AddCommand(checkBlocksCmd)
}
