package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// uncommittedReport is the repository and the Steer-owned paths in it that
// differ from HEAD, untracked ones included, repository-relative.
type uncommittedReport struct {
	Repo  string   `json:"repo"`
	Paths []string `json:"paths"`
}

var uncommittedCmd = &cobra.Command{
	Use:   "uncommitted",
	Short: "List the files Steer writes in this repository that hold uncommitted changes",
	Long: `Lists the files Steer writes in this repository that differ from HEAD,
untracked ones included: the .steer directory (guard store, checks and their
cases), the guard store a repository still keeps in .claude/hooks, each agent's
project instruction document and hook registration, and check.sh.

It reads and writes nothing else. Outside a git repository it fails.`,
	Example: `  steer uncommitted
  steer -C /path/to/repo uncommitted --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		report, err := steerOwnedChanges(cwd)
		if err != nil {
			return err
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		if len(report.Paths) == 0 {
			fmt.Printf("No file Steer writes in %s holds an uncommitted change.\n", report.Repo)
			return nil
		}
		for _, p := range report.Paths {
			fmt.Println(p)
		}
		return nil
	},
}

// steerOwnedPathspecs names, relative to root, every path Steer writes in a
// repository.
func steerOwnedPathspecs(root string) []string {
	stores := steerpath.ProjectHooksRel()
	specs := []string{filepath.ToSlash(filepath.Dir(stores[0])), checks.OracleName}
	if store, err := hookstore.ResolveIn(claude.ProjectScope, root); err == nil {
		if rel, err := filepath.Rel(root, store); err == nil && rel == stores[1] {
			specs = append(specs, filepath.ToSlash(rel))
		}
	}
	for _, h := range harness.All() {
		specs = append(specs, h.InstructionFile)
	}
	claudeCode, _ := harness.Get(harness.ClaudeCode)
	codex, _ := harness.Get(harness.Codex)
	pi, _ := harness.Get(harness.Pi)
	specs = append(specs,
		claudeCode.ConfigDirName+"/"+claude.SettingsName,
		codex.ConfigDirName+"/hooks.json",
		pi.ConfigDirName+"/extensions/steer-*.ts",
	)
	seen := map[string]bool{}
	out := specs[:0]
	for _, s := range specs {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// steerOwnedChanges reads the repository holding dir and returns the paths Steer
// writes there that git reports as changed or untracked.
func steerOwnedChanges(dir string) (uncommittedReport, error) {
	root, err := checks.RepoRootOf(dir)
	if err != nil {
		return uncommittedReport{}, err
	}
	args := append([]string{"-C", root, "status", "--porcelain=v1", "-z", "-uall", "--"}, steerOwnedPathspecs(root)...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return uncommittedReport{}, fmt.Errorf("git status in %s: %w", root, err)
	}
	entries := strings.Split(string(out), "\x00")
	paths := []string{}
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		paths = append(paths, entry[3:])
		if entry[0] == 'R' || entry[0] == 'C' {
			// A rename's source follows as its own entry.
			if i+1 < len(entries) && entries[i+1] != "" {
				paths = append(paths, entries[i+1])
			}
			i++
		}
	}
	sort.Strings(paths)
	return uncommittedReport{Repo: root, Paths: paths}, nil
}

func init() {
	uncommittedCmd.Flags().Bool("json", false, "print the repository and the paths as JSON")
	rootCmd.AddCommand(uncommittedCmd)
}
