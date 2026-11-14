package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/mods"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// validateTimeout bounds one `claude plugin validate` run.
const validateTimeout = 60 * time.Second

var modsCmd = &cobra.Command{
	Use:   "mods",
	Short: "Read which Claude Code mods can skip or override Steer's hooks",
	Long: `A Claude Code mod is a plugin with a JavaScript hooks module. Its tool.call
handler runs before every PreToolUse hook a settings file registers, Steer's guards
and commit gate among them, and can answer a call so those hooks never run. Its
tool.check handler can approve a call such a hook blocked.

steer mods inspect asks Claude Code's own validator which events each enabled mod
handles and caches the answer. steer status reads that cache and names each mod
that handles a tool event, or that no current inspection covers.`,
}

var modsInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Run Claude Code's validator on each enabled mod and cache which events it handles",
	Long: `Inspect lists the enabled Claude Code plugins whose hooks/hooks.json names a
hooks module, for user settings and for this repository's project and local
settings. It runs ` + "`claude plugin validate <dir> --json`" + ` on each one, which reads the
module without running it, and updates the cache steer status reads.

Built-in mods and plugins loaded with --plugin-dir have no install record, so
inspect cannot see them. A mod listed as handling no tool event can still act
through other events; this reading covers only the hooks that see a tool call.`,
	Args: cobra.NoArgs,
	RunE: runModsInspect,
}

var modsJSON bool

func runModsInspect(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	h, ok := harness.Get(harness.ClaudeCode)
	if !ok {
		return fmt.Errorf("the Claude Code harness is not registered in this build")
	}
	configDir, err := h.ConfigDir(harness.GlobalScope, "")
	if err != nil {
		return err
	}
	root, _ := steerpath.ProjectRoot()
	candidates := mods.Candidates(configDir, root)
	var rows []mods.Inspection
	if len(candidates) > 0 {
		claudeBin, err := exec.LookPath("claude")
		if err != nil {
			return fmt.Errorf("claude is not on PATH; inspect runs Claude Code's own validator")
		}
		rows = mods.Inspect(cmd.Context(), candidates, validatorFor(claudeBin), claudeVersion(cmd.Context(), claudeBin))
	}
	dir, err := statestore.Prepare(mods.Component)
	if err != nil {
		return err
	}
	if err := mods.UpdateCache(dir, rows); err != nil {
		return fmt.Errorf("failed to write the inspection cache: %w", err)
	}
	exposures := mods.Read(candidates, dir)
	if modsJSON {
		data, err := json.MarshalIndent(exposures, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	if len(exposures) == 0 {
		fmt.Fprintln(out, "No enabled Claude Code plugin carries a mod.")
		return nil
	}
	for _, e := range exposures {
		switch e.State {
		case mods.StateToolHooks:
			fmt.Fprintf(out, "%s: handles %s — it can skip or override Steer's hooks\n", e.ID, strings.Join(e.Handlers, ", "))
		case mods.StateNoToolHooks:
			fmt.Fprintf(out, "%s: handles no tool event\n", e.ID)
		case mods.StateFailed:
			fmt.Fprintf(out, "%s: inspection failed: %s\n", e.ID, e.Error)
		default:
			fmt.Fprintf(out, "%s: %s\n", e.ID, e.State)
		}
	}
	return nil
}

func validatorFor(claudeBin string) mods.Validator {
	return func(ctx context.Context, dir string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, validateTimeout)
		defer cancel()
		var stdout, stderr bytes.Buffer
		c := exec.CommandContext(ctx, claudeBin, "plugin", "validate", dir, "--json")
		c.Stdout, c.Stderr = &stdout, &stderr
		err := c.Run()
		if stdout.Len() > 0 {
			// The validator exits non-zero for a plugin it refuses and still prints the
			// report that says why.
			return stdout.Bytes(), nil
		}
		if err != nil {
			return nil, fmt.Errorf("claude plugin validate: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("claude plugin validate printed nothing")
	}
}

func claudeVersion(ctx context.Context, claudeBin string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, claudeBin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func init() {
	rootCmd.AddCommand(modsCmd)
	modsCmd.AddCommand(modsInspectCmd)
	modsInspectCmd.Flags().BoolVar(&modsJSON, "json", false, "emit the readings as JSON")
}
