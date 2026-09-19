// Package shellword encodes and decodes the one piece of shell text a hook
// registration carries: the path of the program the harness runs, followed by
// its arguments.
//
// Quote and Split are exact inverses over every path Encodable accepts, and
// over the forms Quote writes and nothing else. They are not a shell.
package shellword

import (
	"fmt"
	"runtime"
	"strings"
)

// Encodable reports whether Quote can render path as one shell word without
// changing it. It is Quote's precondition: Quote does not validate its input.
func Encodable(path string) error { return encodableOn(runtime.GOOS, path) }

func encodableOn(goos, path string) error {
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf("path %q holds a NUL byte", path)
	}
	// cmd.exe ends a double-quoted word at the next double quote and offers no
	// escape for one inside it, so such a path has no single-word form. Windows
	// itself rejects the character in a path name; one reaches here only from a
	// malformed USERPROFILE, which os.UserHomeDir returns without validating.
	if goos == "windows" && strings.Contains(path, `"`) {
		return fmt.Errorf(`path %q holds a double quote, which no Windows command can quote`, path)
	}
	return nil
}

// Quote renders a path as one shell word. Both harnesses run a hook command
// through a shell, so a path holding a space needs quoting. Windows runs it
// through cmd.exe, which reads a backslash as a path separator and knows no
// single quote, so there a path is bare or double quoted and never anything else.
//
// The caller passes a path Encodable accepts; Quote does not check.
func Quote(path string) string { return quoteOn(runtime.GOOS, path) }

func quoteOn(goos, path string) string {
	if goos == "windows" {
		// A bare word may not begin with a quote character: Split reads the first
		// character to choose a form, and an apostrophe is a legal Windows path name.
		if path != "" && !strings.ContainsAny(path, " \t\"") && !strings.HasPrefix(path, "'") {
			return path
		}
		return `"` + path + `"`
	}
	if path != "" && !strings.ContainsAny(path, " \t'\"$`\\*?") {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// Split separates a command's first word from its arguments, reading both the
// forms Quote writes on every platform. ok is false, and head and rest are
// empty, when the command holds no word Quote could have written: an
// unterminated quote, or text joined to a closing quote.
func Split(command string) (head, rest string, ok bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", "", false
	}
	if strings.HasPrefix(command, `"`) {
		word, tail, closed := strings.Cut(command[1:], `"`)
		if !closed || !separated(tail) {
			return "", "", false
		}
		return word, strings.TrimSpace(tail), true
	}
	if !strings.HasPrefix(command, "'") {
		word, tail, _ := strings.Cut(command, " ")
		return word, strings.TrimSpace(tail), true
	}
	var word strings.Builder
	for i := 1; i < len(command); {
		if command[i] != '\'' {
			word.WriteByte(command[i])
			i++
			continue
		}
		if strings.HasPrefix(command[i:], `'\''`) {
			word.WriteByte('\'')
			i += 4
			continue
		}
		tail := command[i+1:]
		if !separated(tail) {
			return "", "", false
		}
		return word.String(), strings.TrimSpace(tail), true
	}
	return "", "", false
}

// separated reports whether a quoted word ends where it closes. Anything but
// whitespace after the closing quote is concatenation, which Quote never writes.
func separated(tail string) bool {
	return tail == "" || strings.HasPrefix(tail, " ") || strings.HasPrefix(tail, "\t")
}
