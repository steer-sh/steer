package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// toFile is the --to-file flag: stdout goes to a new file and only its path is printed,
// so an agent reads a long output with its file tool and never names the file itself.
var toFile bool

// dump is the file stdout is redirected into, and stdout the stream it replaced.
var dump struct {
	file   *os.File
	stdout *os.File
}

// startDump redirects stdout into a new file under the temporary directory and writes
// the file's path to out first, so a command that exits directly still names its
// dump. The name is unique, so concurrent sessions never share a dump.
func startDump(out io.Writer) error {
	f, err := os.CreateTemp("", "steer-*.txt")
	if err != nil {
		return fmt.Errorf("--to-file: %w", err)
	}
	fmt.Fprintln(out, f.Name())
	dump.file, dump.stdout = f, os.Stdout
	os.Stdout = f
	return nil
}

// finishDump restores stdout and closes the dump. It does nothing when no dump was
// started.
func finishDump() {
	if dump.file == nil {
		return
	}
	os.Stdout = dump.stdout
	if err := dump.file.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "--to-file:", err)
	}
	dump.file, dump.stdout = nil, nil
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&toFile, "to-file", false,
		"Write the output to a new file under the temporary directory and print only its path")
	// OnInitialize runs after flag parsing for every command that runs, including one
	// that declares its own PersistentPreRunE; --help prints before it, to stdout.
	cobra.OnInitialize(func() {
		if toFile && dump.file == nil {
			if err := startDump(os.Stdout); err != nil {
				// Output on stdout in place of a path would be read as a path.
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	})
}
