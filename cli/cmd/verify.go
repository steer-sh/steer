package cmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// steerPreCommitMarker marks the git pre-commit hook an earlier Steer's
// `git-hooks install` wrote, which runs `steer verify --staged`.
const steerPreCommitMarker = "# steer-managed: verify-staged"

// verifyRemovedNotice is the stub's one stderr line.
const verifyRemovedNotice = "steer: steer verify is removed, and this git pre-commit hook checks nothing; " +
	"to remove it, run: steer commit-gate remove --precommit"

// verifyCmd answers the git pre-commit hook an earlier Steer installed, whose last
// line runs `steer verify --staged`. git refuses the commit on a non-zero exit, so
// that form exits 0; any other form, such as a CI job's --changed, fails.
var verifyCmd = &cobra.Command{
	Use:                "verify",
	Short:              "Removed: guard verification of staged or changed files",
	Hidden:             true,
	DisableFlagParsing: true,
	SilenceUsage:       true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if slices.Contains(args, "--staged") {
			fmt.Fprintln(cmd.ErrOrStderr(), verifyRemovedNotice)
			return nil
		}
		return errors.New("steer verify is removed; a guard runs on the agent's tool call, not on files in git")
	},
}

// buildSteerPreCommitScript is the hook `git-hooks install` wrote, which removal
// matches exactly so it deletes no line an operator added.
func buildSteerPreCommitScript(includeGlobal bool) string {
	verifyCommand := "steer verify --staged --scope project"
	if includeGlobal {
		verifyCommand += " --include-global"
	}
	return strings.Join([]string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"",
		steerPreCommitMarker,
		"if ! command -v steer >/dev/null 2>&1; then",
		"  echo \"steer pre-commit: 'steer' is not in PATH.\" >&2",
		"  exit 1",
		"fi",
		"",
		verifyCommand,
		"",
	}, "\n")
}

func init() {
	rootCmd.AddCommand(verifyCmd)
}
