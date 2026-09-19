// Package shellword encodes and decodes the one piece of shell text a hook
// registration carries: the path of the program the harness runs, followed by its
// arguments.
//
// A registration is a command string, and both harnesses run it through a shell.
// The path in it therefore has to survive a shell's word splitting going out and
// name the same file coming back in. The encoder and the decoder here are the
// pair that holds for the forms this package writes; they are not a shell.
package shellword

import (
	"runtime"
	"strings"
)

// Quote renders a path as one shell word. Both harnesses run a hook command
// through a shell, so a path holding a space needs quoting. Windows runs it
// through cmd.exe, which reads a backslash as a path separator and knows no
// single quote, so there a path is bare or double quoted and never anything else.
func Quote(path string) string {
	if runtime.GOOS == "windows" {
		if path != "" && !strings.ContainsAny(path, " \t\"") {
			return path
		}
		return `"` + strings.ReplaceAll(path, `"`, "") + `"`
	}
	if path != "" && !strings.ContainsAny(path, " \t'\"$`\\*?") {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// Split separates a command's first word from its arguments, honouring the single
// quotes Quote writes so a home directory with a space stays one word. Both
// quoting forms are read on every platform, so a settings.json copied between
// machines still parses.
func Split(command string) (string, string) {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		head, rest, ok := strings.Cut(command[1:], `"`)
		if !ok {
			return "", ""
		}
		return head, strings.TrimSpace(rest)
	}
	if !strings.HasPrefix(command, "'") {
		head, rest, _ := strings.Cut(command, " ")
		return head, strings.TrimSpace(rest)
	}
	var head strings.Builder
	for i := 1; i < len(command); {
		if command[i] != '\'' {
			head.WriteByte(command[i])
			i++
			continue
		}
		if strings.HasPrefix(command[i:], `'\''`) {
			head.WriteByte('\'')
			i += 4
			continue
		}
		return head.String(), strings.TrimSpace(command[i+1:])
	}
	return "", ""
}
