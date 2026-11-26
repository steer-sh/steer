package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/protection"
	"github.com/steer-sh/steer-cli/internal/sleep"
	"github.com/steer-sh/steer-cli/internal/status"
)

var (
	sleepSession string
	sleepMinutes int
	sleepRepo    string
	// sleepGuardRepos are the repositories whose project guard runner a guard's sleep checks.
	sleepGuardRepos []string
	sleepJSON       bool
)

var sleepCmd = &cobra.Command{
	Use:   "sleep",
	Short: "Let one guard, or one repository's check.sh, pass for a few minutes in one session",
	Long: `Sleep lets one guard pass the calls it would block, or lets a repository's turns
end without running check.sh, in one agent session for a few minutes. It is for one
block you do not want right now; the guard and check.sh stay installed and wake on
their own.

Only you set a sleep. Steer's self-protection guard refuses an agent's steer sleep
and steer wake and any agent write to the sleep state, and the self-protection guard
itself never sleeps. In Claude Code, the Sleep button in Steer's band runs this
command. Each call a sleeping guard lets through, and each turn that ends while
check.sh sleeps, is still recorded, and steer sleep list shows every sleep.`,
	Example: dedent(`
  steer sleep guard no-direct-main-hook --session 8f2c…
  steer sleep check --session 8f2c… --minutes 10
  steer wake guard no-direct-main-hook --session 8f2c…
  steer sleep list`),
}

var sleepGuardCmd = &cobra.Command{
	Use:   "guard NAME",
	Short: "Let one guard pass the calls it would block in one session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] == protection.HookName {
			return fmt.Errorf("the self-protection guard has no sleep; turning it off needs %s", "steer config set protection.self off")
		}
		if err := guardCanSleep(args[0]); err != nil {
			return err
		}
		return putSleep(cmd, sleep.KindGuard, args[0], args[0])
	},
}

var sleepCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Let one repository's turns end without check.sh in one session",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		identity, err := sleepRepoIdentity()
		if err != nil {
			return err
		}
		return putSleep(cmd, sleep.KindCheck, identity, "check.sh in "+identity)
	},
}

var sleepListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show every sleep set on this machine, oldest first",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		events, err := sleep.Events()
		if err != nil {
			return err
		}
		var shown []sleep.Event
		for _, ev := range events {
			if sleepSession == "" || ev.Session == sleepSession {
				shown = append(shown, ev)
			}
		}
		if sleepJSON {
			if shown == nil {
				shown = []sleep.Event{}
			}
			return writeJSON(cmd.OutOrStdout(), shown)
		}
		out := cmd.OutOrStdout()
		if len(shown) == 0 {
			fmt.Fprintln(out, "No sleep is recorded on this machine.")
			return nil
		}
		for _, ev := range shown {
			at := time.UnixMilli(ev.AtMS).Local().Format("2006-01-02 15:04")
			if ev.Action == sleep.ActionWake {
				fmt.Fprintf(out, "%s  wake   %s %s  session %s\n", at, ev.Kind, ev.Target, ev.Session)
				continue
			}
			until := time.UnixMilli(ev.UntilMS).Local().Format("15:04")
			fmt.Fprintf(out, "%s  sleep  %s %s  session %s  until %s\n", at, ev.Kind, ev.Target, ev.Session, until)
		}
		return nil
	},
}

var wakeCmd = &cobra.Command{
	Use:   "wake",
	Short: "End a sleep of a guard or of a repository's check.sh before its time",
}

var wakeGuardCmd = &cobra.Command{
	Use:   "guard NAME",
	Short: "Wake a sleeping guard in one session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return removeSleep(cmd, sleep.KindGuard, args[0], args[0])
	},
}

var wakeCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Wake one repository's sleeping check.sh in one session",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		identity, err := sleepRepoIdentity()
		if err != nil {
			return err
		}
		return removeSleep(cmd, sleep.KindCheck, identity, "check.sh in "+identity)
	},
}

// guardCanSleep refuses while a registered master that runs hook, globally or in
// a repository --repo names (default: the working directory's), cannot honor a sleep.
// A --repo directory that no longer exists is skipped; when every one is, the
// working directory's repository is checked.
func guardCanSleep(hook string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	var dirs []string
	for _, dir := range sleepGuardRepos {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(wd, dir)
		}
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		if refusal := status.SleepRefusal(hook, sleep.Version); refusal != "" {
			return errors.New(refusal)
		}
		return nil
	}
	defer func() { _ = os.Chdir(wd) }()
	for _, dir := range dirs {
		if err := os.Chdir(dir); err != nil {
			return fmt.Errorf("--repo %s: %w", dir, err)
		}
		if refusal := status.SleepRefusal(hook, sleep.Version); refusal != "" {
			return errors.New(refusal)
		}
	}
	return nil
}

// sleepRepoIdentity is the repository --repo names, or the one the working directory is in.
func sleepRepoIdentity() (string, error) {
	dir := sleepRepo
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	return adoptionIdentity(dir)
}

func putSleep(cmd *cobra.Command, kind, target, name string) error {
	rec, err := sleep.Put(sleepSession, kind, target, time.Duration(sleepMinutes)*time.Minute, time.Now())
	if err != nil {
		return err
	}
	if sleepJSON {
		return writeJSON(cmd.OutOrStdout(), rec)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s sleeps in session %s until %s.\n",
		name, sleepSession, time.UnixMilli(rec.UntilMS).Local().Format("15:04:05"))
	return nil
}

func removeSleep(cmd *cobra.Command, kind, target, name string) error {
	_, woke, err := sleep.Remove(sleepSession, kind, target, time.Now())
	if err != nil {
		return err
	}
	if woke {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is awake in session %s.\n", name, sleepSession)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s was not asleep in session %s.\n", name, sleepSession)
	}
	return nil
}

func init() {
	for _, c := range []*cobra.Command{sleepGuardCmd, sleepCheckCmd, wakeGuardCmd, wakeCheckCmd} {
		c.Flags().StringVar(&sleepSession, "session", "", "the agent session the sleep applies to (required)")
		_ = c.MarkFlagRequired("session")
	}
	for _, c := range []*cobra.Command{sleepGuardCmd, sleepCheckCmd} {
		c.Flags().IntVar(&sleepMinutes, "minutes", sleep.DefaultMinutes, fmt.Sprintf("how long the sleep lasts, 1 to %d", sleep.MaxMinutes))
		c.Flags().BoolVar(&sleepJSON, "json", false, "emit the sleep's record as JSON")
	}
	for _, c := range []*cobra.Command{sleepCheckCmd, wakeCheckCmd} {
		c.Flags().StringVar(&sleepRepo, "repo", "", "the repository (default: the one the working directory is in)")
	}
	sleepGuardCmd.Flags().StringArrayVar(&sleepGuardRepos, "repo", nil,
		"a repository whose guard runner must honor the sleep, repeatable (default: the one the working directory is in)")
	sleepListCmd.Flags().StringVar(&sleepSession, "session", "", "list only this session's sleeps")
	sleepListCmd.Flags().BoolVar(&sleepJSON, "json", false, "emit JSON")
	sleepCmd.AddCommand(sleepGuardCmd, sleepCheckCmd, sleepListCmd)
	wakeCmd.AddCommand(wakeGuardCmd, wakeCheckCmd)
	rootCmd.AddCommand(sleepCmd, wakeCmd)
}
