package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var testCmd = &cobra.Command{
	Use:   "test <hook-name-or-path>",
	Short: "Run an individual hook against sample code",
	Long: `Test replays a hook's stored fixtures, or runs it against an ad-hoc payload.

With no --code or --file, it reruns the cases stored beside the guard, against
the guard alone and through the generated master. With either flag it runs the
individual hook once against a payload that mimics Claude Code PreToolUse JSON
input.`,
	Example: `  steer test no-var-hook
  steer test no-var-hook --code "var x = 1"
  steer test no-var-hook --file ./sample.js
  steer test no-var-hook --code "print('x')" --extension py`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🧪 Test Hook"))
		fmt.Println()

		scope := currentScope()
		hookIdentifier := args[0]
		codeSnippet, _ := cmd.Flags().GetString("code")
		filePath, _ := cmd.Flags().GetString("file")
		extension, _ := cmd.Flags().GetString("extension")

		if codeSnippet != "" && filePath != "" {
			return fmt.Errorf("provide exactly one of --code or --file")
		}
		if codeSnippet == "" && filePath == "" {
			// With no ad-hoc payload, the stored fixtures are the test.
			return runStoredFixtures(scope, hookIdentifier)
		}

		hook, err := claude.ResolveHook(scope, hookIdentifier)
		if err != nil {
			return err
		}

		content := codeSnippet
		testPath := "/test/sample.js"
		if filePath != "" {
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %w", filePath, err)
			}
			content = string(data)
			testPath = filePath
		} else if extension != "" {
			if !strings.HasPrefix(extension, ".") {
				extension = "." + extension
			}
			testPath = "/test/sample" + extension
		}

		fmt.Println(styles.InfoBox.Render(fmt.Sprintf("Hook: %s\nSource: %s", hook.Name, filepath.Base(testPath))))
		fmt.Println()

		payload := map[string]any{
			"tool_name": "Edit",
			"tool_input": map[string]any{
				"file_path":  testPath,
				"old_string": "",
				"new_string": content,
			},
		}

		inputJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal hook input: %w", err)
		}

		utils.Working("Executing hook...")
		execCmd := exec.Command("python3", hook.Path)
		execCmd.Stdin = strings.NewReader(string(inputJSON))
		output, err := execCmd.CombinedOutput()
		outputStr := strings.TrimSpace(string(output))

		blocked, reason := parseHookDecision(outputStr)
		if err != nil && !blocked {
			return fmt.Errorf("hook execution failed: %w\nOutput: %s", err, outputStr)
		}

		fmt.Println(styles.SectionHeader.Render("Result"))
		fmt.Println()
		if blocked {
			fmt.Println(styles.ErrorBox.Render("Hook decision: BLOCK"))
			if reason != "" {
				fmt.Println(styles.InfoBox.Render("Reason: " + reason))
			}
		} else {
			fmt.Println(styles.SuccessBox.Render("Hook decision: ALLOW"))
		}
		if outputStr != "" {
			fmt.Println()
			fmt.Println(styles.SectionHeader.Render("Raw Output"))
			fmt.Println(styles.CodeBlock.Render(outputStr))
		}

		return nil
	},
}

func parseHookDecision(output string) (bool, string) {
	if strings.TrimSpace(output) == "" {
		return false, ""
	}

	var parsed struct {
		Decision           string `json:"decision"`
		StopReason         string `json:"stopReason"`
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err == nil {
		if parsed.Decision == "block" {
			return true, parsed.StopReason
		}
		// The master renders a block as a PreToolUse deny.
		if parsed.HookSpecificOutput.PermissionDecision == "deny" {
			return true, parsed.HookSpecificOutput.PermissionDecisionReason
		}
		return false, ""
	}

	compact := strings.Join(strings.Fields(output), "")
	if strings.Contains(compact, `"decision":"block"`) {
		return true, ""
	}

	return false, ""
}

func init() {
	rootCmd.AddCommand(testCmd)
	testCmd.Flags().String("code", "", "Code snippet to test")
	testCmd.Flags().String("file", "", "Path to a file to test")
	testCmd.Flags().String("extension", "", "File extension override when using --code")
}
