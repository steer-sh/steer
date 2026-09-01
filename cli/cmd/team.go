package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/presets"
	"github.com/steer-sh/steer-cli/internal/styles"
)

// TeamLockfileName is the repo-committed team policy lockfile at project root.
const TeamLockfileName = "steer-team.lock.json"

const teamLockfileVersion = 1

var (
	teamApplyDryRun bool
	teamApplyForce  bool
	teamVerifyCI    bool
)

type teamLockedHook struct {
	Name          string   `json:"name"`
	File          string   `json:"file"`
	Source        string   `json:"source"`
	SourceID      string   `json:"source_id,omitempty"`
	SourceVersion string   `json:"source_version,omitempty"`
	PromptVersion string   `json:"prompt_version,omitempty"`
	PromptModules []string `json:"prompt_modules,omitempty"`
	HookVersion   string   `json:"hook_version,omitempty"`
	Rule          string   `json:"rule"`
	RuleHash      string   `json:"rule_hash"`
	Content       string   `json:"content"`
	ContentSHA256 string   `json:"content_sha256"`
}

type teamLockfile struct {
	LockfileVersion int              `json:"lockfile_version"`
	CatalogVersion  string           `json:"catalog_version"`
	Hooks           []teamLockedHook `json:"hooks"`
}

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Distribute steer policy through a repo-committed lockfile",
	Long: `Team manages steer policy as a git-committed lockfile.
The lockfile is the source of truth: lock snapshots local steer state,
apply reconciles a clone against it, and verify --ci fails on drift.`,
}

var teamLockCmd = &cobra.Command{
	Use:   "lock",
	Short: "Write the team lockfile from current steer state",
	Example: `  steer team lock
  git add steer-team.lock.json && git commit -m "steer: update team policy"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🔒 Team Lock"))
		fmt.Println()

		scope, err := requireTeamScope()
		if err != nil {
			return err
		}

		catalog, err := presets.LoadCatalog()
		if err != nil {
			return err
		}

		lockfile, err := buildTeamLockfile(scope, catalog)
		if err != nil {
			return err
		}

		if err := writeTeamLockfile(TeamLockfileName, lockfile); err != nil {
			return err
		}

		fmt.Println(styles.SuccessBox.Render(fmt.Sprintf(
			"Lockfile: %s\nHooks locked: %d\nCatalog version: %s",
			TeamLockfileName,
			len(lockfile.Hooks),
			lockfile.CatalogVersion,
		)))
		return nil
	},
}

var teamApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Reconcile local steer state against the team lockfile",
	Long: `Apply installs missing or drifted locked hooks and removes factory-managed
extras absent from the lockfile. Personal (generated/manual) hooks outside the
lockfile are untouched: team policy is a floor, not a ceiling.
--force switches to strict mode and removes all extras.`,
	Example: `  steer team apply
  steer team apply --dry-run
  steer team apply --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		fmt.Println(styles.MainHeader.Render("🤝 Team Apply"))
		fmt.Println()

		scope, err := requireTeamScope()
		if err != nil {
			return err
		}

		lockfile, err := readTeamLockfile(TeamLockfileName)
		if err != nil {
			return err
		}

		targets, err := currentRegistrars()
		if err != nil {
			return err
		}

		desired := desiredHooksFromLockfile(lockfile)
		result, err := applyTeamLockfile(scope, lockfile, teamApplyDryRun, teamApplyForce, targets...)
		if result == nil {
			result = &claude.ReconcileResult{}
		}

		upToDate := 0
		installed := 0
		removed := 0
		kept := 0
		for _, outcome := range result.Outcomes {
			switch outcome.Status {
			case claude.OutcomeUpToDate:
				upToDate++
			case claude.OutcomeInstalled:
				installed++
				fmt.Println(styles.SuccessText.Render(fmt.Sprintf("✓ install %s (%s)", outcome.HookName, driftLabel(outcome.Drift))))
			case claude.OutcomeRemoved:
				removed++
				fmt.Println(styles.WarningText.Render(fmt.Sprintf("- remove %s (extra)", outcome.HookName)))
			case claude.OutcomeKept:
				kept++
			}
		}

		summary := fmt.Sprintf(
			"Lockfile: %s\nLocked hooks: %d\nUp-to-date: %d\nInstalled: %d\nExtras removed: %d\nPersonal hooks kept: %d",
			TeamLockfileName,
			len(desired),
			upToDate,
			installed,
			removed,
			kept,
		)
		if teamApplyDryRun {
			summary += fmt.Sprintf("\nDry-run planned mutations: %d", result.Planned)
		}

		fmt.Println()
		if err != nil {
			fmt.Println(styles.WarningBox.Render(summary))
			return err
		}
		fmt.Println(styles.SuccessBox.Render(summary))
		return nil
	},
}

var teamVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Check local steer state against the team lockfile (read-only)",
	Example: `  steer team verify
  steer team verify --ci`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !teamVerifyCI {
			fmt.Println()
			fmt.Println(styles.MainHeader.Render("🔍 Team Verify"))
			fmt.Println()
		}

		scope, err := requireTeamScope()
		if err != nil {
			return err
		}

		lockfile, err := readTeamLockfile(TeamLockfileName)
		if err != nil {
			return err
		}

		targets, err := currentRegistrars()
		if err != nil {
			return err
		}

		findings, err := verifyAgainstLockfile(scope, lockfile, targets...)
		if err != nil {
			return err
		}

		for _, finding := range findings {
			fmt.Println("steer-team-verify: FAIL " + finding)
		}

		if len(findings) > 0 {
			if !teamVerifyCI {
				fmt.Println()
				fmt.Println(styles.WarningBox.Render(fmt.Sprintf("Drift findings: %d\nRun `steer team apply` to reconcile.", len(findings))))
			}
			return fmt.Errorf("team verify found %d drift findings", len(findings))
		}

		fmt.Println(fmt.Sprintf("steer-team-verify: OK hooks=%d lockfile=%s", len(lockfile.Hooks), TeamLockfileName))
		return nil
	},
}

func requireTeamScope() (string, error) {
	scope := currentScope()
	if scope != claude.ProjectScope {
		return "", fmt.Errorf("steer team commands are project-scope only (the lockfile is committed to the repo); remove --scope %s", scope)
	}
	return scope, nil
}

func driftLabel(reason string) string {
	if reason == claude.DriftMissingEntry {
		return "missing"
	}
	if strings.TrimSpace(reason) == "" {
		return "drift"
	}
	return reason
}

func buildTeamLockfile(scope string, catalog *presets.Catalog) (*teamLockfile, error) {
	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return nil, err
	}
	if len(manifest.Hooks) == 0 {
		return nil, fmt.Errorf("no steer hooks to lock (manifest is empty); enable presets or register hooks first")
	}

	hooksDir, err := claude.GetHooksDir(scope)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(manifest.Hooks))
	for name := range manifest.Hooks {
		names = append(names, name)
	}
	sort.Strings(names)

	locked := make([]teamLockedHook, 0, len(names))
	for _, name := range names {
		entry := manifest.Hooks[name]

		fileName := strings.TrimSpace(entry.File)
		if fileName == "" {
			fileName = name + ".py"
		}
		content, err := os.ReadFile(filepath.Join(hooksDir, fileName))
		if err != nil {
			return nil, fmt.Errorf("hook %s: cannot read %s (run `steer preset sync` to repair drifted state before locking): %w", name, fileName, err)
		}

		ruleHash := strings.TrimSpace(entry.RuleHash)
		if ruleHash == "" {
			ruleHash = claude.HashRule(entry.Rule)
		}

		locked = append(locked, teamLockedHook{
			Name:          name,
			File:          fileName,
			Source:        entry.Source,
			SourceID:      entry.SourceID,
			SourceVersion: entry.SourceVersion,
			PromptVersion: entry.PromptVersion,
			PromptModules: entry.PromptModules,
			HookVersion:   entry.HookVersion,
			Rule:          entry.Rule,
			RuleHash:      ruleHash,
			Content:       string(content),
			ContentSHA256: claude.ContentSHA256(content),
		})
	}

	return &teamLockfile{
		LockfileVersion: teamLockfileVersion,
		CatalogVersion:  catalog.CatalogVersion,
		Hooks:           locked,
	}, nil
}

func writeTeamLockfile(path string, lockfile *teamLockfile) error {
	data, err := json.MarshalIndent(lockfile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lockfile: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("failed to write lockfile: %w", err)
	}
	return nil
}

func readTeamLockfile(path string) (*teamLockfile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("lockfile not found: %s (run `steer team lock` and commit it)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read lockfile: %w", err)
	}

	lockfile := &teamLockfile{}
	if err := json.Unmarshal(data, lockfile); err != nil {
		return nil, fmt.Errorf("failed to parse lockfile %s: %w", path, err)
	}
	if lockfile.LockfileVersion != teamLockfileVersion {
		return nil, fmt.Errorf("unsupported lockfile_version %d (this steer supports %d)", lockfile.LockfileVersion, teamLockfileVersion)
	}
	if len(lockfile.Hooks) == 0 {
		return nil, fmt.Errorf("lockfile %s contains no hooks", path)
	}

	seen := map[string]struct{}{}
	for _, hook := range lockfile.Hooks {
		name := strings.TrimSpace(hook.Name)
		if name == "" {
			return nil, fmt.Errorf("lockfile entry with empty name")
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("lockfile has duplicate hook %s", name)
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(hook.Rule) == "" {
			return nil, fmt.Errorf("lockfile hook %s has empty rule", name)
		}
		if claude.HashRule(hook.Rule) != strings.TrimSpace(hook.RuleHash) {
			return nil, fmt.Errorf("lockfile integrity check failed for hook %s: rule_hash mismatch", name)
		}
		if claude.ContentSHA256([]byte(hook.Content)) != strings.TrimSpace(hook.ContentSHA256) {
			return nil, fmt.Errorf("lockfile integrity check failed for hook %s: content_sha256 mismatch", name)
		}
	}

	return lockfile, nil
}

func desiredHooksFromLockfile(lockfile *teamLockfile) []claude.DesiredHook {
	desired := make([]claude.DesiredHook, 0, len(lockfile.Hooks))
	for _, hook := range lockfile.Hooks {
		desired = append(desired, claude.DesiredHook{
			HookName: strings.TrimSpace(hook.Name),
			FileName: strings.TrimSpace(hook.File),
			Rule:     hook.Rule,
			Content:  []byte(hook.Content),
			Metadata: claude.HookInstallMetadata{
				Source:        hook.Source,
				SourceID:      hook.SourceID,
				SourceVersion: hook.SourceVersion,
				PromptVersion: hook.PromptVersion,
				PromptModules: hook.PromptModules,
				HookVersion:   hook.HookVersion,
			},
		})
	}
	return desired
}

func applyTeamLockfile(scope string, lockfile *teamLockfile, dryRun, force bool, targets ...harness.Registrar) (*claude.ReconcileResult, error) {
	return claude.Reconcile(scope, desiredHooksFromLockfile(lockfile), claude.ReconcileOptions{
		DryRun: dryRun,
		RemoveExtra: func(hookName string, entry claude.ManifestEntry) bool {
			if force {
				return true
			}
			return strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory)
		},
	}, targets...)
}

func verifyAgainstLockfile(scope string, lockfile *teamLockfile, targets ...harness.Registrar) ([]string, error) {
	findings := make([]string, 0)

	manifest, err := claude.LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	desired := desiredHooksFromLockfile(lockfile)
	lockedNames := make(map[string]struct{}, len(desired))
	for _, hook := range desired {
		lockedNames[hook.HookName] = struct{}{}
	}

	for _, hook := range desired {
		entry, hasEntry := manifest.Hooks[hook.HookName]
		drifted, reason, err := claude.DesiredHookDrift(scope, hook, entry, hasEntry, targets...)
		if err != nil {
			return nil, err
		}
		if drifted {
			findings = append(findings, fmt.Sprintf("drift hook=%s reason=%s", hook.HookName, driftLabel(reason)))
		}
	}

	extraNames := make([]string, 0)
	for name, entry := range manifest.Hooks {
		if _, locked := lockedNames[name]; locked {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(entry.Source), claude.SourceFactory) {
			extraNames = append(extraNames, name)
		}
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		findings = append(findings, fmt.Sprintf("extra hook=%s source=factory", name))
	}

	// One store, one lockfile, but each target harness carries its own rules
	// document and its own registration, so both are checked per harness.
	for _, target := range harness.Targets(targets, claude.Registrar{}) {
		entry := target.Harness()

		docPath, err := target.InstructionPath(scope)
		if err != nil {
			return nil, err
		}
		doc, err := os.ReadFile(docPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read %s: %w", entry.InstructionFile, err)
		}
		for _, hook := range lockfile.Hooks {
			if !strings.Contains(string(doc), strings.TrimSpace(hook.Rule)) {
				findings = append(findings, fmt.Sprintf("%s missing rule hook=%s harness=%s",
					entry.InstructionFile, strings.TrimSpace(hook.Name), entry.ID))
			}
		}

		registered, err := target.MasterRegistered(scope)
		if err != nil {
			return nil, err
		}
		if !registered {
			findings = append(findings, fmt.Sprintf("master hook not registered harness=%s", entry.ID))
		}
	}

	return findings, nil
}

func init() {
	rootCmd.AddCommand(teamCmd)

	teamCmd.AddCommand(teamLockCmd)
	teamCmd.AddCommand(teamApplyCmd)
	teamCmd.AddCommand(teamVerifyCmd)

	teamApplyCmd.Flags().BoolVar(&teamApplyDryRun, "dry-run", false, "Preview reconcile actions without mutating files")
	teamApplyCmd.Flags().BoolVar(&teamApplyForce, "force", false, "Strict mode: make local state exactly match the lockfile, removing all extras")

	teamVerifyCmd.Flags().BoolVar(&teamVerifyCI, "ci", false, "Machine-readable output for CI pipelines")
}
