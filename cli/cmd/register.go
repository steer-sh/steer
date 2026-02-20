package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var registerRule string

var registerCmd = &cobra.Command{
	Use:   "register <hook-path>",
	Short: "Register an individual hook and rebuild steer master hook",
	Long: `Register installs an individual hook into the selected scope and keeps all
steer-managed artifacts in sync:
- updates steer-manifest.json
- rebuilds .claude/hooks/steer-hooks.py
- upserts master hook command in settings.json (preserving non-steer hooks)
- appends the provided rule to CLAUDE.md`,
	Example: `  steer register .claude/hooks/no-var-hook.py --rule "never use var | use const or let"
  steer register /tmp/my-hook.py --rule "block TODO in prod files" --scope global`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("📝 Register Hook"))
		fmt.Println()

		rule := strings.TrimSpace(registerRule)
		if rule == "" {
			return fmt.Errorf("--rule is required")
		}

		scope := currentScope()
		sourcePath := args[0]
		sourceAbs, err := filepath.Abs(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to resolve hook path: %w", err)
		}

		info, err := os.Stat(sourceAbs)
		if err != nil {
			return fmt.Errorf("failed to stat hook path: %w", err)
		}
		if info.IsDir() {
			return fmt.Errorf("hook path is a directory: %s", sourcePath)
		}
		if strings.ToLower(filepath.Ext(sourceAbs)) != ".py" {
			return fmt.Errorf("hook file must be a .py file")
		}

		utils.Working("Ensuring hook directories exist...")
		if err := claude.EnsureDirectories(scope); err != nil {
			return err
		}

		hooksDir, err := claude.GetHooksDir(scope)
		if err != nil {
			return err
		}

		fileName := filepath.Base(sourceAbs)
		if fileName == claude.MasterHookName {
			return fmt.Errorf("cannot register %s directly", claude.MasterHookName)
		}

		hookName := strings.TrimSuffix(fileName, ".py")
		targetPath := filepath.Join(hooksDir, fileName)
		targetAbs, err := filepath.Abs(targetPath)
		if err != nil {
			return fmt.Errorf("failed to resolve target hook path: %w", err)
		}

		manifest, err := claude.LoadManifest(scope)
		if err != nil {
			return err
		}
		if _, exists := manifest.Hooks[hookName]; exists {
			return fmt.Errorf("hook already registered: %s", hookName)
		}

		if sourceAbs != targetAbs {
			if _, err := os.Stat(targetPath); err == nil {
				return fmt.Errorf("hook file already exists in hooks directory: %s", targetPath)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("failed to check target hook file: %w", err)
			}

			content, err := os.ReadFile(sourceAbs)
			if err != nil {
				return fmt.Errorf("failed to read source hook file: %w", err)
			}
			utils.Saving("Copying hook into scope directory...")
			if err := os.WriteFile(targetPath, content, 0o755); err != nil {
				return fmt.Errorf("failed to copy hook file into hooks directory: %w", err)
			}
		} else {
			utils.Saving("Updating hook permissions...")
			if err := os.Chmod(targetPath, 0o755); err != nil {
				return fmt.Errorf("failed to ensure hook is executable: %w", err)
			}
		}

		utils.Registering("Recording hook manifest...")
		if err := claude.AddHook(scope, hookName, fileName, rule); err != nil {
			return err
		}

		utils.Working("Consolidating master hook...")
		if err := claude.ConsolidateHooks(scope); err != nil {
			return err
		}

		utils.Registering("Registering master hook in settings.json...")
		if err := claude.RegisterMasterHook(scope); err != nil {
			return err
		}

		utils.Saving("Appending rule to CLAUDE.md...")
		if err := claude.AppendRule(rule, scope); err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Registered hook: %s\nPath: %s\nScope: %s",
			hookName,
			targetPath,
			scope,
		)))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(registerCmd)
	registerCmd.Flags().StringVar(&registerRule, "rule", "", "Original rule text for manifest and CLAUDE.md")
	registerCmd.MarkFlagRequired("rule")
}
