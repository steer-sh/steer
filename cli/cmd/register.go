package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/guard/fixture"
	"github.com/steer-sh/steer-cli/internal/styles"
	"github.com/steer-sh/steer-cli/internal/utils"
)

var registerRule string
var registerSource string
var registerPromptVersion string
var registerHookVersion string
var registerPromptModules string
var registerModel string
var registerSkillVersion string
var registerSelfTest string
var registerFixtures string

var registerCmd = &cobra.Command{
	Use:   "register <hook-path>",
	Short: "Register an individual hook and rebuild steer master hook",
	Long: `Register installs an individual hook into the selected scope and keeps all
steer-managed artifacts in sync:
- updates steer-manifest.json
- rebuilds .steer/hooks/steer-hooks.py
- upserts master hook command in settings.json (preserving non-steer hooks)
- appends the provided rule to CLAUDE.md

Optional provenance flags can be used to persist generation metadata.`,
	Example: `  steer register .steer/hooks/no-var-hook.py --rule "never use var | use const or let"
  steer register /tmp/my-hook.py --rule "block TODO in prod files" --scope global
  steer register .steer/hooks/sql-hook.py --rule "block SQL f-strings" --source generated --prompt-version v1 --hook-version h1`,
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

		content, err := os.ReadFile(sourceAbs)
		if err != nil {
			return fmt.Errorf("failed to read source hook file: %w", err)
		}

		modules := parseCommaList(registerPromptModules)
		selfTest, err := parseSelfTestSummary(registerSelfTest)
		if err != nil {
			return err
		}
		fixtures, err := readFixtureFile(registerFixtures, hookNameFor(sourceAbs))
		if err != nil {
			return err
		}

		targets, err := currentRegistrars()
		if err != nil {
			return err
		}

		fileName := filepath.Base(sourceAbs)
		hookName := strings.TrimSuffix(fileName, ".py")
		utils.Registering("Installing hook and syncing managed artifacts...")
		result, err := claude.InstallManagedHook(scope, claude.InstallManagedHookInput{
			HookName: hookName,
			FileName: fileName,
			Rule:     rule,
			Content:  content,
			Metadata: claude.HookInstallMetadata{
				Source:        registerSource,
				PromptVersion: registerPromptVersion,
				HookVersion:   registerHookVersion,
				PromptModules: modules,
				Model:         registerModel,
				SkillVersion:  registerSkillVersion,
				SelfTest:      selfTest,
			},
			Fixtures: fixtures,
		}, targets...)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Registered hook: %s\nPath: %s\nScope: %s\nHarness: %s",
			hookName,
			result.HookPath,
			scope,
			harnessNames(targets),
		)))
		printFixtureVerification(result.Verification)
		return nil
	},
}

func parseCommaList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseSelfTestSummary(raw string) (*claude.SelfTestSummary, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var summary claude.SelfTestSummary
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &summary); err != nil {
			return nil, fmt.Errorf("invalid --self-test JSON: %w", err)
		}
		return &summary, nil
	}

	parts := strings.Split(raw, ",")
	if len(parts) == 5 {
		passed, err := parseCompactBool(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid --self-test compact form: %w", err)
		}
		positivePass, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid --self-test positive_pass: %w", err)
		}
		positiveFail, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil {
			return nil, fmt.Errorf("invalid --self-test positive_fail: %w", err)
		}
		negativePass, err := strconv.Atoi(strings.TrimSpace(parts[3]))
		if err != nil {
			return nil, fmt.Errorf("invalid --self-test negative_pass: %w", err)
		}
		negativeFail, err := strconv.Atoi(strings.TrimSpace(parts[4]))
		if err != nil {
			return nil, fmt.Errorf("invalid --self-test negative_fail: %w", err)
		}
		summary.Passed = passed
		summary.PositivePass = positivePass
		summary.PositiveFail = positiveFail
		summary.NegativePass = negativePass
		summary.NegativeFail = negativeFail
		return &summary, nil
	}

	values := map[string]string{}
	for _, token := range parts {
		kv := strings.SplitN(strings.TrimSpace(token), "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid --self-test format: expected JSON, passed,pp,pf,np,nf, or key=value pairs")
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.TrimSpace(kv[1])
		values[key] = value
	}

	if passedValue, ok := values["passed"]; ok {
		passed, err := parseCompactBool(passedValue)
		if err != nil {
			return nil, fmt.Errorf("invalid --self-test passed: %w", err)
		}
		summary.Passed = passed
	}
	for key, target := range map[string]*int{
		"positive_pass": &summary.PositivePass,
		"positive_fail": &summary.PositiveFail,
		"negative_pass": &summary.NegativePass,
		"negative_fail": &summary.NegativeFail,
	} {
		value, ok := values[key]
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid --self-test %s: %w", key, err)
		}
		*target = parsed
	}
	if _, ok := values["passed"]; !ok {
		summary.Passed = summary.PositiveFail == 0 && summary.NegativeFail == 0
	}

	return &summary, nil
}

func parseCompactBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y":
		return true, nil
	case "0", "false", "f", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("expected boolean value, got %q", raw)
	}
}

func init() {
	rootCmd.AddCommand(registerCmd)
	registerCmd.Flags().StringVar(&registerRule, "rule", "", "Original rule text for manifest and CLAUDE.md")
	registerCmd.MarkFlagRequired("rule")
	registerCmd.Flags().StringVar(&registerSource, "source", claude.SourceManual, "Hook source metadata (manual|generated|factory)")
	registerCmd.Flags().StringVar(&registerPromptVersion, "prompt-version", claude.DefaultPromptVersion, "Prompt/instruction version used to produce this hook")
	registerCmd.Flags().StringVar(&registerHookVersion, "hook-version", "", "Hook implementation revision (optional; defaults to prompt-version for source=generated, otherwise manual)")
	registerCmd.Flags().StringVar(&registerPromptModules, "prompt-modules", "", "Comma-separated prompt module names")
	registerCmd.Flags().StringVar(&registerModel, "model", "", "Model used for generation (if applicable)")
	registerCmd.Flags().StringVar(&registerSkillVersion, "skill-version", "", "Skill/package version used for generation")
	registerCmd.Flags().StringVar(&registerFixtures, "fixtures", "", "Path to a fixture file storing this guard's replayable cases")
	registerCmd.Flags().StringVar(&registerSelfTest, "self-test", "", "Self-test summary JSON or compact form: passed,positive_pass,positive_fail,negative_pass,negative_fail")
}

// hookNameFor derives the manifest name a hook file registers under.
func hookNameFor(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".py")
}

// readFixtureFile loads the cases a registration stores beside the guard. The
// document's hook name is filled from the file being registered when it names
// none, so a mined candidate set needs no hand edit to be storable.
func readFixtureFile(path, hook string) (*fixture.File, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read fixtures: %w", err)
	}
	var raw fixture.File
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse fixtures: %w", err)
	}
	if raw.Version == 0 {
		raw.Version = fixture.Version
	}
	if strings.TrimSpace(raw.Hook) == "" {
		raw.Hook = hook
	}
	if err := raw.Validate(); err != nil {
		return nil, err
	}
	return &raw, nil
}

// printFixtureVerification reports the rerun that followed consolidation. It is
// a report and never a gate: the install already happened.
func printFixtureVerification(v *claude.FixtureVerification) {
	if v == nil || len(v.Reports) == 0 {
		return
	}
	for _, report := range v.Reports {
		if report.Tested() {
			fmt.Printf("fixtures: %s — %d case(s) intercepted", report.Hook, len(report.Cases))
			if n := report.Shadowed(); n > 0 {
				fmt.Printf(", %d shadowed by another guard and not verified through the master", n)
			}
			fmt.Println()
			continue
		}
		fmt.Printf("fixtures: %s — %s\n", report.Hook, report.NotVerified("master"))
	}
}
