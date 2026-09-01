package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var (
	verifyStaged        bool
	verifyChanged       bool
	verifyBase          string
	verifyHead          string
	verifyIncludeGlobal bool
)

type verifyTarget struct {
	Path    string
	Content string
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify changed files against active hooks",
	Long: `Verify runs active steer hook policy checks against files from git:
- --staged verifies staged/index content (for local pre-commit)
- --changed --base <sha> --head <sha> verifies files changed in a commit range (for CI)

By default verify runs project scope hooks. Use --include-global to also run
global hooks after project hooks.`,
	Example: `  steer verify --staged
  steer verify --staged --include-global
  steer verify --changed --base origin/main --head HEAD`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, err := resolveVerifyMode()
		if err != nil {
			return err
		}

		var targets []verifyTarget
		switch mode {
		case "staged":
			targets, err = loadStagedTargets()
		case "changed":
			targets, err = loadChangedTargets(strings.TrimSpace(verifyBase), strings.TrimSpace(verifyHead))
		default:
			return fmt.Errorf("unsupported verify mode: %s", mode)
		}
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(styles.MainHeader.Render("Verify Hooks"))
		fmt.Println()

		if len(targets) == 0 {
			utils.Success("No files to verify.")
			return nil
		}

		scopes := verificationScopes(currentScope(), verifyIncludeGlobal)
		utils.Info(fmt.Sprintf("Mode: %s | Files: %d | Scopes: %s", mode, len(targets), strings.Join(scopes, ", ")))

		totalBlocked := 0
		totalErrors := 0
		verifiedScopes := 0

		for _, verifyScope := range scopes {
			managed, err := claude.ListHooks(verifyScope)
			if err != nil {
				return err
			}
			if len(managed) == 0 {
				utils.Info(fmt.Sprintf("Scope %s: no managed hooks; skipping.", verifyScope))
				continue
			}

			// Verifying files is a read. Regenerating here migrated a legacy
			// store as a side effect and left the harness registered on the
			// legacy master, so the repaired file sat unused.
			masterHookPath, err := claude.GetMasterHookPath(verifyScope)
			if err != nil {
				return err
			}
			if _, err := os.Stat(masterHookPath); err != nil {
				return fmt.Errorf("scope %s has managed hooks but missing master hook at %s (run `steer hooks rebuild --scope %s`)",
					verifyScope, masterHookPath, verifyScope)
			}

			verifiedScopes++
			scopeBlocked := 0
			scopeErrors := 0
			utils.Working(fmt.Sprintf("Scope %s: verifying %d files with %d managed hooks...", verifyScope, len(targets), len(managed)))

			for _, target := range targets {
				blocked, reason, _, execErr := runMasterHookVerification(masterHookPath, target)
				if execErr != nil {
					scopeErrors++
					totalErrors++
					fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ [%s] %s: %s", verifyScope, target.Path, execErr)))
					continue
				}
				if blocked {
					scopeBlocked++
					totalBlocked++
					if strings.TrimSpace(reason) == "" {
						reason = "policy violation"
					}
					fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ [%s] %s: %s", verifyScope, target.Path, reason)))
				}
			}

			if scopeBlocked == 0 && scopeErrors == 0 {
				fmt.Println(styles.SuccessText.Render(fmt.Sprintf("✓ Scope %s: all checks passed.", verifyScope)))
			} else {
				fmt.Println(styles.WarningText.Render(fmt.Sprintf("Scope %s: blocked=%d errors=%d", verifyScope, scopeBlocked, scopeErrors)))
			}
			fmt.Println()
		}

		if verifiedScopes == 0 {
			utils.Info("No managed hooks found in selected scope(s). Nothing to enforce.")
			return nil
		}

		summary := fmt.Sprintf(
			"Verification summary\nFiles checked: %d\nScope runs: %d\nBlocked: %d\nErrors: %d",
			len(targets),
			verifiedScopes,
			totalBlocked,
			totalErrors,
		)

		fmt.Println()
		if totalBlocked > 0 || totalErrors > 0 {
			fmt.Println(styles.WarningBox.Render(summary))
			return fmt.Errorf("verification failed")
		}

		fmt.Println(styles.SuccessBox.Render(summary))
		return nil
	},
}

func resolveVerifyMode() (string, error) {
	staged := verifyStaged
	changed := verifyChanged
	base := strings.TrimSpace(verifyBase)
	head := strings.TrimSpace(verifyHead)

	if staged && changed {
		return "", fmt.Errorf("choose exactly one mode: --staged or --changed")
	}
	if !staged && !changed {
		return "", fmt.Errorf("missing mode: use --staged or --changed")
	}

	if staged {
		if base != "" || head != "" {
			return "", fmt.Errorf("--base/--head are only valid with --changed")
		}
		return "staged", nil
	}

	if base == "" || head == "" {
		return "", fmt.Errorf("--changed requires both --base and --head")
	}
	return "changed", nil
}

func verificationScopes(primary string, includeGlobal bool) []string {
	primary = strings.ToLower(strings.TrimSpace(primary))
	if primary == "" {
		primary = claude.ProjectScope
	}

	scopes := []string{primary}
	if includeGlobal && primary != claude.GlobalScope {
		scopes = append(scopes, claude.GlobalScope)
	}
	return scopes
}

func loadStagedTargets() ([]verifyTarget, error) {
	paths, err := gitChangedFiles("diff", "--cached", "--name-only", "--diff-filter=ACMR", "-M")
	if err != nil {
		return nil, err
	}

	targets := make([]verifyTarget, 0, len(paths))
	for _, path := range paths {
		content, err := runGit("show", ":"+path)
		if err != nil {
			return nil, fmt.Errorf("failed to read staged content for %s: %w", path, err)
		}
		targets = append(targets, verifyTarget{
			Path:    path,
			Content: content,
		})
	}

	return targets, nil
}

func loadChangedTargets(base, head string) ([]verifyTarget, error) {
	paths, err := gitChangedFiles("diff", "--name-only", "--diff-filter=ACMR", "-M", base, head)
	if err != nil {
		return nil, err
	}

	targets := make([]verifyTarget, 0, len(paths))
	for _, path := range paths {
		content, err := runGit("show", fmt.Sprintf("%s:%s", head, path))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s at %s: %w", path, head, err)
		}
		targets = append(targets, verifyTarget{
			Path:    path,
			Content: content,
		})
	}

	return targets, nil
}

func runMasterHookVerification(masterHookPath string, target verifyTarget) (bool, string, string, error) {
	payload := map[string]any{
		"tool_name": "Write",
		"tool_input": map[string]any{
			"file_path": target.Path,
			"content":   target.Content,
		},
	}

	inputJSON, err := json.Marshal(payload)
	if err != nil {
		return false, "", "", fmt.Errorf("failed to marshal hook payload: %w", err)
	}

	execCmd := exec.Command("python3", masterHookPath)
	execCmd.Stdin = strings.NewReader(string(inputJSON))
	output, err := execCmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	blocked, reason := parseHookDecision(outputStr)
	if err != nil && !blocked {
		return false, "", outputStr, fmt.Errorf("hook execution failed: %w", err)
	}

	return blocked, reason, outputStr, nil
}

func init() {
	rootCmd.AddCommand(verifyCmd)
	verifyCmd.Flags().BoolVar(&verifyStaged, "staged", false, "Verify staged/index file content")
	verifyCmd.Flags().BoolVar(&verifyChanged, "changed", false, "Verify files changed between --base and --head")
	verifyCmd.Flags().StringVar(&verifyBase, "base", "", "Base git ref/sha for --changed mode")
	verifyCmd.Flags().StringVar(&verifyHead, "head", "", "Head git ref/sha for --changed mode")
	verifyCmd.Flags().BoolVar(&verifyIncludeGlobal, "include-global", false, "Also verify global hooks in addition to selected scope")
}
