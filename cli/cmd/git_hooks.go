package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

const steerPreCommitMarker = "# steer-managed: verify-staged"

var (
	gitHooksInstallForce         bool
	gitHooksInstallIncludeGlobal bool
)

var gitHooksCmd = &cobra.Command{
	Use:   "git-hooks",
	Short: "Manage Git hook integration",
}

var gitHooksInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install a pre-commit hook that runs steer verify",
	Long: `Install writes pre-commit in the directory git runs hooks from for the current
repository: core.hooksPath when it is set, otherwise .git/hooks. Every repository that
shares that directory runs the hook. The hook runs:
  steer verify --staged --scope project

Use --include-global to also run global hooks.`,
	Example: `  steer git-hooks install
  steer git-hooks install --include-global
  steer git-hooks install --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		hooksDir, err := workTreeHooksDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			return fmt.Errorf("failed to create hooks directory: %w", err)
		}

		preCommitPath := filepath.Join(hooksDir, "pre-commit")
		existing, readErr := os.ReadFile(preCommitPath)
		if readErr == nil {
			existingText := string(existing)
			managed := strings.Contains(existingText, steerPreCommitMarker)
			if !managed && !gitHooksInstallForce {
				return fmt.Errorf("pre-commit hook already exists and is not steer-managed: %s (use --force to overwrite)", preCommitPath)
			}
		} else if !os.IsNotExist(readErr) {
			return fmt.Errorf("failed to read existing pre-commit hook: %w", readErr)
		}

		script := buildSteerPreCommitScript(gitHooksInstallIncludeGlobal)
		if err := os.WriteFile(preCommitPath, []byte(script), 0o755); err != nil {
			return fmt.Errorf("failed to write pre-commit hook: %w", err)
		}
		if err := os.Chmod(preCommitPath, 0o755); err != nil {
			return fmt.Errorf("failed to mark pre-commit hook executable: %w", err)
		}

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Git Hooks"))
		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Installed pre-commit hook\nPath: %s\nCommand: %s",
			preCommitPath,
			strings.TrimSpace(preCommitVerifyArgs(gitHooksInstallIncludeGlobal)),
		)))
		utils.Tip("Add `steer verify --changed --base <sha> --head <sha>` as a required CI check.")
		return nil
	},
}

// workTreeHooksDir is gitHooksDir for a command run from a work tree. It refuses
// anywhere else: inside .git a relative core.hooksPath would resolve under .git.
func workTreeHooksDir() (string, error) {
	if _, err := gitTopLevel(); err != nil {
		return "", err
	}
	return gitHooksDir()
}

func preCommitVerifyArgs(includeGlobal bool) string {
	if includeGlobal {
		return "steer verify --staged --scope project --include-global"
	}
	return "steer verify --staged --scope project"
}

func buildSteerPreCommitScript(includeGlobal bool) string {
	verifyCommand := preCommitVerifyArgs(includeGlobal)
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
	rootCmd.AddCommand(gitHooksCmd)
	gitHooksCmd.AddCommand(gitHooksInstallCmd)
	gitHooksInstallCmd.Flags().BoolVar(&gitHooksInstallForce, "force", false, "Overwrite an existing non-steer pre-commit hook")
	gitHooksInstallCmd.Flags().BoolVar(&gitHooksInstallIncludeGlobal, "include-global", false, "Include global hooks in pre-commit verification")
}
