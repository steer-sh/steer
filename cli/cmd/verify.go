package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/guard"
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
		unscrubbed := false

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

			results, err := verifyAgainstMaster(masterHookPath, targets)
			if err != nil {
				return fmt.Errorf("scope %s: %w", verifyScope, err)
			}
			diagnostics := map[string]bool{}
			for i, result := range results {
				target := targets[i]
				for _, line := range strings.Split(result.Stderr, "\n") {
					if line = strings.TrimSpace(line); line != "" && !diagnostics[line] {
						diagnostics[line] = true
						utils.Warning(fmt.Sprintf("[%s] %s", verifyScope, line))
					}
				}
				if result.Err != nil {
					scopeErrors++
					totalErrors++
					fmt.Println(styles.ErrorText.Render(fmt.Sprintf("✗ [%s] %s: %s", verifyScope, target.Path, result.Err)))
					continue
				}
				if result.Blocked {
					scopeBlocked++
					totalBlocked++
					reason := result.Reason
					if strings.TrimSpace(reason) == "" {
						reason = "policy violation"
					}
					if !result.FiringRemoved {
						unscrubbed = true
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

		if unscrubbed {
			utils.Warning("These firings are temporary: verification discards its audit rows, so do not run a contest command shown above.")
		}

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
	paths, err := gitChangedFiles("diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR", "-M")
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
	paths, err := gitChangedFiles("diff", "--name-only", "-z", "--diff-filter=ACMR", "-M", base, head)
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

// verifyResult is one file's outcome under a scope's master.
type verifyResult struct {
	Blocked bool
	// Reason is the block's reason without the lines that name its firing.
	Reason string
	// FiringRemoved reports that the reason's firing lines were found and removed.
	FiringRemoved bool
	// Stderr is the master's diagnostics, such as a guard it could not load.
	Stderr string
	Err    error
}

// verifyAgainstMaster runs every target through a copy of the master at
// masterHookPath, so its audit rows land in a discarded store. The rest of the
// store is mirrored beside the copy, less the audit log and payload directory;
// symlinks are recreated, never read through. Paths outside the store are not
// mirrored. A failed copy is an error; nothing falls back to the live store.
func verifyAgainstMaster(masterHookPath string, targets []verifyTarget) ([]verifyResult, error) {
	store, err := guard.NewSandbox("steer-verify-")
	if err != nil {
		return nil, err
	}
	defer store.Close()

	masterName := filepath.Base(masterHookPath)
	masterCopy, err := store.Copy(masterHookPath, masterName)
	if err != nil {
		return nil, err
	}
	live, err := filepath.EvalSymlinks(filepath.Dir(masterHookPath))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the guard store: %w", err)
	}
	err = filepath.WalkDir(live, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(live, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		switch {
		case name == guard.PayloadsDirName && entry.IsDir():
			return filepath.SkipDir
		case name == "." || name == masterName || name == guard.AuditLogName:
			return nil
		case entry.IsDir():
			return store.Mkdir(name)
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) {
				// Not filepath.Join: cleaning "link/.." lexically names another file.
				target = filepath.Dir(path) + string(filepath.Separator) + target
			}
			return store.Link(target, name)
		case !entry.Type().IsRegular():
			return nil
		}
		_, err = store.Copy(path, name)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to copy the guard store: %w", err)
	}

	results := make([]verifyResult, 0, len(targets))
	for _, target := range targets {
		results = append(results, runMasterHookVerification(masterCopy, target))
	}
	return results, nil
}

// runMasterHookVerification runs the master at masterHookPath on one target. It
// clears that master's audit log first, so the firing it reads back is this run's.
func runMasterHookVerification(masterHookPath string, target verifyTarget) verifyResult {
	payload := map[string]any{
		"tool_name": "Write",
		"tool_input": map[string]any{
			"file_path": target.Path,
			"content":   target.Content,
		},
	}

	inputJSON, err := json.Marshal(payload)
	if err != nil {
		return verifyResult{Err: fmt.Errorf("failed to marshal hook payload: %w", err)}
	}

	auditPath := filepath.Join(filepath.Dir(masterHookPath), guard.AuditLogName)
	if err := os.Remove(auditPath); err != nil && !os.IsNotExist(err) {
		return verifyResult{Err: fmt.Errorf("failed to clear the verification audit log: %w", err)}
	}

	var stdout, stderr strings.Builder
	execCmd := exec.Command("python3", masterHookPath)
	execCmd.Stdin = strings.NewReader(string(inputJSON))
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr
	err = execCmd.Run()
	result := verifyResult{Stderr: strings.TrimSpace(stderr.String())}

	blocked, reason := parseHookDecision(strings.TrimSpace(stdout.String()))
	if err != nil && !blocked {
		output := strings.TrimSpace(stdout.String() + "\n" + result.Stderr)
		result.Err = fmt.Errorf("hook execution failed: %w: %s", err, output)
		return result
	}
	result.Blocked = blocked
	result.Reason = reason
	if blocked {
		result.Reason, result.FiringRemoved = withoutFiring(reason, recordedFiringID(auditPath))
	}
	return result
}

// recordedFiringID is the firing_id of the last row in the audit log at path, or
// "" when there is no row or no well-formed identifier.
func recordedFiringID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var row struct {
		FiringID string `json:"firing_id"`
	}
	if json.Unmarshal([]byte(lines[len(lines)-1]), &row) != nil || !adjudication.ValidFiringID(row.FiringID) {
		return ""
	}
	return row.FiringID
}

// withoutFiring drops every line of reason that names firingID, which a discarded
// verification store cannot answer for, and keeps the rest. It reports whether
// any line was dropped.
func withoutFiring(reason, firingID string) (string, bool) {
	if firingID == "" {
		return reason, false
	}
	lines := strings.Split(reason, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, firingID) {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(lines) {
		return reason, false
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), true
}

func init() {
	rootCmd.AddCommand(verifyCmd)
	verifyCmd.Flags().BoolVar(&verifyStaged, "staged", false, "Verify staged/index file content")
	verifyCmd.Flags().BoolVar(&verifyChanged, "changed", false, "Verify files changed between --base and --head")
	verifyCmd.Flags().StringVar(&verifyBase, "base", "", "Base git ref/sha for --changed mode")
	verifyCmd.Flags().StringVar(&verifyHead, "head", "", "Head git ref/sha for --changed mode")
	verifyCmd.Flags().BoolVar(&verifyIncludeGlobal, "include-global", false, "Also verify global hooks in addition to selected scope")
}
