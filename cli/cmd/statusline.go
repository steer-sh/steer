package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/statusline"
)

// wrappedTimeout is the wall clock a wrapped command gets. Claude Code cancels an
// in-flight script when the next update arrives, so a slow child costs a repaint.
const wrappedTimeout = 3 * time.Second

var (
	statusLineWrapB64 string
	statusLineWrap    bool
	statusLineReplace bool
	statusLineScope   string
)

var statusLineCmd = &cobra.Command{
	Use:   "statusline",
	Short: "Print steer's status line row",
	Long: `statusline prints one row for the Claude Code status line: whether a commit
here will be reviewed, the reviewer that would run it, and the one command that
changes the answer.

With no flags it writes the row to stdout and nothing else, so a status line built
from several sources can call it and place the row itself. Claude Code renders each
line of a script's output as its own row.

The command always exits 0. A script that fails or prints nothing blanks the whole
status line, and in --wrap-b64 form steer owns the operator's rows too.`,
	Example: `  steer statusline
  steer statusline install --scope global
  steer statusline install --scope global --wrap`,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Stdin is read only in wrap mode, where the wrapped command needs the same
		// payload. Reading it unconditionally hangs an operator who types the
		// command in a terminal, waiting on an EOF that never arrives.
		if statusLineWrapB64 != "" {
			payload, _ := io.ReadAll(os.Stdin)
			printWrapped(statusLineWrapB64, payload)
		}
		fmt.Println(statusline.Render(statusline.Collect()))
		return nil
	},
}

// printWrapped runs the wrapped command on the same payload and prints its output.
// It never reports a failure: the operator's rows and steer's row fail independently.
func printWrapped(encoded string, payload []byte) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), wrappedTimeout)
	defer cancel()

	child := exec.CommandContext(ctx, "sh", "-c", string(decoded))
	child.Stdin = strings.NewReader(string(payload))
	child.Env = os.Environ()
	out, err := child.Output()
	if err != nil && len(out) == 0 {
		return
	}
	os.Stdout.Write(out)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		fmt.Println()
	}
}

var statusLineInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Register steer's row as the status line",
	Long: `install writes steer's status line into settings.json.

The status line is one slot per scope, so an occupied slot is never taken silently.
With a status line already registered, install writes nothing and names the two
flags:

  --wrap     steer runs the existing command on the same payload, prints its rows,
             and adds its own row underneath. remove restores the command exactly.
  --replace  steer takes the slot and the existing command is discarded.

Both keep the entry's other keys — padding, refreshInterval, hideVimModeIndicator
are the operator's, not the command's.

Two conditions leave the row invisible through no fault of the install: a workspace
whose trust dialog has not been accepted, and an organization that sets
allowManagedHooksOnly in managed settings.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if statusLineWrap && statusLineReplace {
			return fmt.Errorf("--wrap and --replace choose different fates for the " +
				"existing command: pass one")
		}
		if err := claude.ValidateScope(statusLineScope); err != nil {
			return err
		}
		settings, err := claude.LoadSettings(statusLineScope)
		if err != nil {
			return err
		}

		command := steerHookCommand("statusline")
		existing, present := settings.StatusLine()
		mine := present && claude.IsStatusLineCommand(existing.Command)

		if present && !mine && !statusLineWrap && !statusLineReplace {
			fmt.Printf("A status line is already registered in %s scope:\n\n  %s\n\n",
				statusLineScope, existing.Command)
			fmt.Println("steer wrote nothing. Choose what happens to it:")
			fmt.Println("  steer statusline install --scope " + statusLineScope + " --wrap      keep it, add steer's row below")
			fmt.Println("  steer statusline install --scope " + statusLineScope + " --replace   discard it")
			fmt.Println("\nOr leave the slot alone and call `steer statusline` from your own script.")
			return nil
		}
		if target, ok := statusLineWrapTarget(existing.Command, mine, statusLineWrap, statusLineReplace); ok {
			command += " " + claude.EncodeWrapped(target)
		}

		if err := claude.RegisterStatusLine(statusLineScope, command); err != nil {
			return err
		}
		fmt.Printf("Status line registered in %s scope.\n", statusLineScope)
		fmt.Println("  " + statusline.Render(statusline.Collect()))
		return nil
	},
}

// statusLineWrapTarget returns the command a new registration must wrap, and whether
// it wraps one at all. --replace discards whatever is there, steer's own entry keeps
// what it already wraps, and --wrap takes over a foreign command.
func statusLineWrapTarget(existing string, mine, wrap, replace bool) (string, bool) {
	if replace || existing == "" {
		return "", false
	}
	if mine {
		return claude.WrappedCommand(existing)
	}
	if wrap {
		return existing, true
	}
	return "", false
}

var statusLineRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Take steer's row out of the status line",
	Long: `remove clears steer's status line. An entry that wraps a command restores that
command in place, byte for byte. An entry steer did not install is left alone.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := claude.ValidateScope(statusLineScope); err != nil {
			return err
		}
		restored, err := claude.RemoveStatusLine(statusLineScope)
		if err != nil {
			return err
		}
		if restored != "" {
			fmt.Printf("Restored the wrapped status line in %s scope:\n  %s\n",
				statusLineScope, restored)
			return nil
		}
		fmt.Printf("Status line cleared in %s scope.\n", statusLineScope)
		return nil
	},
}

var statusLineStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Report the registered status line and the row it renders",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, scope := range []string{claude.ProjectScope, claude.GlobalScope} {
			entry, present, mine := claude.StatusLineArmed(scope)
			switch {
			case !present:
				fmt.Printf("%-8s no status line registered\n", scope)
			case mine:
				fmt.Printf("%-8s steer\n", scope)
				if wrapped, ok := claude.WrappedCommand(entry.Command); ok {
					fmt.Printf("%-8s   wrapping: %s\n", "", wrapped)
				}
			default:
				fmt.Printf("%-8s another command: %s\n", scope, entry.Command)
			}
		}
		fmt.Println("\nrow:")
		fmt.Println("  " + statusline.Render(statusline.Collect()))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusLineCmd)
	statusLineCmd.AddCommand(statusLineInstallCmd, statusLineRemoveCmd, statusLineStatusCmd)

	statusLineCmd.Flags().StringVar(&statusLineWrapB64, "wrap-b64", "",
		"base64 of a command to run and print above steer's row")
	statusLineInstallCmd.Flags().BoolVar(&statusLineWrap, "wrap", false,
		"keep the existing status line and add steer's row below it")
	statusLineInstallCmd.Flags().BoolVar(&statusLineReplace, "replace", false,
		"discard the existing status line and take the slot")
	for _, c := range []*cobra.Command{statusLineInstallCmd, statusLineRemoveCmd} {
		c.Flags().StringVar(&statusLineScope, "scope", claude.GlobalScope,
			"project or global")
	}
}
