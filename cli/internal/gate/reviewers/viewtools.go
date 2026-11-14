package reviewers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Caps on what the tools return. Every cap holds inside the review timeout, which
// the context carries.
const (
	// maxCallBytes bounds one tool result.
	maxCallBytes = 24 << 10
	// maxRunToolBytes bounds the tool results of one run together.
	maxRunToolBytes = 192 << 10
	// maxGrepFiles bounds the files one grep scans.
	maxGrepFiles = 2000
	// maxGrepMatches bounds the lines one grep returns.
	maxGrepMatches = 80
	// maxGrepPattern bounds a grep pattern's length.
	maxGrepPattern = 200
	// maxListPaths bounds the paths one list_files returns.
	maxListPaths = 400
	// maxScanFileBytes skips a larger file in grep.
	maxScanFileBytes = 1 << 20
	// maxReadFileBytes refuses a larger file in read_file.
	maxReadFileBytes = 8 << 20
	// maxLineChars bounds one grep match line.
	maxLineChars = 200
)

// toolSpecs are the functions the chat request offers, in the chat-completions
// `tools` shape.
var toolSpecs = []map[string]any{
	toolSpec("read_file",
		"Read a file as this commit lands it. Lines are numbered from 1. Give start_line and end_line to read part of a long file.",
		map[string]any{
			"path":       map[string]any{"type": "string", "description": "Path from the repository root"},
			"start_line": map[string]any{"type": "integer", "description": "First line to read, from 1"},
			"end_line":   map[string]any{"type": "integer", "description": "Last line to read, inclusive"},
		}, "path"),
	toolSpec("grep",
		"Find the lines that hold a fixed string (not a regular expression, case-sensitive) in the files this commit lands.",
		map[string]any{
			"pattern":   map[string]any{"type": "string", "description": "The exact text to find"},
			"path_glob": map[string]any{"type": "string", "description": globHelp},
		}, "pattern"),
	toolSpec("list_files",
		"List the files this commit lands.",
		map[string]any{
			"path_glob": map[string]any{"type": "string", "description": globHelp},
		}),
}

const globHelp = "Optional. A directory ending in / (cli/internal/), a glob on the whole path " +
	"(cli/*/x.go), or a glob on the file name (*.go). Empty means every file."

func toolSpec(name, description string, properties map[string]any, required ...string) map[string]any {
	params := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		params["required"] = required
	}
	return map[string]any{"type": "function",
		"function": map[string]any{"name": name, "description": description, "parameters": params}}
}

// viewTools runs the tool calls of one review run against the commit view. One run
// owns one viewTools, so its counters need no lock.
type viewTools struct {
	view *gate.CommitView
	// bytes is the tool output this run has returned so far.
	bytes int
}

// spent reports whether the run's output budget is gone: a remainder that cannot
// hold the cut note counts as spent.
func (t *viewTools) spent() bool {
	return maxRunToolBytes-t.bytes <= 2*cutNoteBytes
}

// toolErrorPrefix opens a result that reports a failed call.
const toolErrorPrefix = "error: "

// cutNoteBytes is the room a result leaves for the note that says where it stopped,
// so the note survives the cut at the limit.
const cutNoteBytes = 96

// errToolBudget is a call refused because the run's tool output budget is spent.
var errToolBudget = errors.New("the tool output budget of this review is spent; give the review now")

// call runs one tool call. A refused call returns its reason as the result text, so
// the model reads it; refused is true when the run's budget refused it.
func (t *viewTools) call(ctx context.Context, name string, rawArgs string) (result string, refused bool) {
	remaining := maxRunToolBytes - t.bytes
	if t.spent() {
		return errToolBudget.Error(), true
	}
	limit := min(maxCallBytes, remaining)
	text, err := t.run(ctx, name, rawArgs, limit-cutNoteBytes)
	if err != nil {
		text = toolErrorPrefix + err.Error()
	}
	if len(text) > limit {
		note := fmt.Sprintf("\n[cut at %d bytes]", limit)
		text = cutUTF8(text, limit-len(note)) + note
	}
	t.bytes += len(text)
	return text, false
}

// run executes one call; limit is the room its result has, which read_file fills
// by whole lines.
func (t *viewTools) run(ctx context.Context, name, rawArgs string, limit int) (string, error) {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		Pattern   string `json:"pattern"`
		PathGlob  string `json:"path_glob"`
	}
	if strings.TrimSpace(rawArgs) != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return "", fmt.Errorf("the arguments are not a JSON object: %v", err)
		}
	}
	switch name {
	case "read_file":
		return t.readFile(ctx, args.Path, args.StartLine, args.EndLine, limit)
	case "grep":
		return t.grep(ctx, args.Pattern, args.PathGlob, limit)
	case "list_files":
		return t.listFiles(args.PathGlob, limit)
	}
	return "", fmt.Errorf("no tool named %q; the tools are read_file, grep and list_files", name)
}

func (t *viewTools) readFile(ctx context.Context, p string, start, end, limit int) (string, error) {
	p = t.resolvePath(p)
	if !t.view.Has(p) {
		return "", fmt.Errorf("%s is not a file in this commit", p)
	}
	var data []byte
	size := int64(-1)
	err := t.view.Scan(ctx, []string{p}, maxReadFileBytes, func(_ string, d []byte, n int64) bool {
		data, size = d, n
		return false
	})
	if err != nil || (data == nil && size < 0) {
		return "", fmt.Errorf("%s could not be read", p)
	}
	if data == nil {
		return "", fmt.Errorf("%s is %d bytes, over the %d MiB read_file limit; the tools cannot read it", p, size, maxReadFileBytes>>20)
	}
	if isBinary(data) {
		return fmt.Sprintf("%s is a binary file of %d bytes", p, len(data)), nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)
	if start < 1 {
		start = 1
	}
	if end < 1 || end > total {
		end = total
	}
	if start > total {
		return fmt.Sprintf("%s has %d lines", p, total), nil
	}
	if start > end {
		return "", fmt.Errorf("start_line %d is after end_line %d", start, end)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s, lines %d-%d of %d\n", p, start, end, total)
	for i := start; i <= end; i++ {
		line := fmt.Sprintf("%6d\t%s\n", i, strings.TrimSuffix(lines[i-1], "\n"))
		if b.Len()+len(line) > limit {
			if i == start {
				// One line longer than the room: its start, and the size of the rest.
				room := max(limit-b.Len(), 0)
				fmt.Fprintf(&b, "%s\n[line %d is %d bytes; the rest of it is not shown]\n",
					cutUTF8(line, room), i, len(strings.TrimSuffix(lines[i-1], "\n")))
				if i < end {
					fmt.Fprintf(&b, "[read on with start_line=%d]\n", i+1)
				}
			} else {
				fmt.Fprintf(&b, "[stopped before line %d; read on with start_line=%d]\n", i, i)
			}
			break
		}
		b.WriteString(line)
	}
	return b.String(), nil
}

// grepNoteBytes is the room grep keeps after its matches for the notes that say how
// complete the search was.
const grepNoteBytes = 1024

// maxUnreadNames and maxUnreadName bound the unreadable paths grep names, so they
// fit in grepNoteBytes beside the other notes.
const (
	maxUnreadNames = 5
	maxUnreadName  = 150
)

func (t *viewTools) grep(ctx context.Context, pattern, glob string, limit int) (string, error) {
	if pattern == "" {
		return "", errors.New("pattern is empty")
	}
	if len(pattern) > maxGrepPattern || strings.Contains(pattern, "\n") {
		return "", fmt.Errorf("pattern must be one line of at most %d bytes", maxGrepPattern)
	}
	paths, err := matching(t.view.Paths(), glob)
	if err != nil {
		return "", err
	}
	stoppedAtFiles := len(paths) > maxGrepFiles
	if stoppedAtFiles {
		paths = paths[:maxGrepFiles]
	}
	needle := []byte(pattern)
	var b strings.Builder
	matches, skipped := 0, 0
	var unreadable []string
	stoppedAtMatches, stoppedAtBytes := false, false
	room := limit - grepNoteBytes
	err = t.view.Scan(ctx, paths, maxScanFileBytes, func(p string, data []byte, size int64) bool {
		if data == nil && size < 0 {
			unreadable = append(unreadable, p)
			return true
		}
		if data == nil || isBinary(data) {
			skipped++
			return true
		}
		if !bytes.Contains(data, needle) {
			return true
		}
		for i, line := range bytes.Split(data, []byte("\n")) {
			if !bytes.Contains(line, needle) {
				continue
			}
			if matches == maxGrepMatches {
				stoppedAtMatches = true
				return false
			}
			entry := fmt.Sprintf("%s:%d: %s\n", p, i+1, matchWindow(line, needle))
			if b.Len()+len(entry) > room {
				stoppedAtBytes = true
				return false
			}
			b.WriteString(entry)
			matches++
		}
		return true
	})
	if err != nil {
		return "", fmt.Errorf("the search stopped: %v", err)
	}
	if matches == 0 && !stoppedAtBytes {
		fmt.Fprintf(&b, "no line holds %q in %d files\n", pattern, len(paths))
	}
	if stoppedAtBytes {
		fmt.Fprintf(&b, "[stopped at the output limit after %d matches; narrow path_glob]\n", matches)
	}
	if stoppedAtMatches {
		fmt.Fprintf(&b, "[stopped at %d matches; narrow path_glob]\n", maxGrepMatches)
	}
	if stoppedAtFiles {
		fmt.Fprintf(&b, "[scanned only the first %d files; narrow path_glob]\n", maxGrepFiles)
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "[skipped %d binary or large files]\n", skipped)
	}
	if n := len(unreadable); n > 0 {
		var names []string
		for _, p := range unreadable[:min(n, maxUnreadNames)] {
			names = append(names, cutUTF8(p, maxUnreadName))
		}
		named := strings.Join(names, ", ")
		if n > maxUnreadNames {
			named += fmt.Sprintf(" and %d more", n-maxUnreadNames)
		}
		fmt.Fprintf(&b, "[could not read %d files, not searched: %s]\n", n, named)
	}
	return b.String(), nil
}

// matchWindow is a line of at most about maxLineChars bytes that shows the match,
// with … where it cut.
func matchWindow(line, needle []byte) string {
	if len(line) <= maxLineChars {
		return string(line)
	}
	at := bytes.Index(line, needle)
	start := max(0, at-maxLineChars/3)
	end := min(len(line), max(start+maxLineChars, at+len(needle)))
	text := strings.ToValidUTF8(string(line[start:end]), "")
	if start > 0 {
		text = "…" + text
	}
	if end < len(line) {
		text += "…"
	}
	return text
}

func (t *viewTools) listFiles(glob string, limit int) (string, error) {
	paths, err := matching(t.view.Paths(), glob)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "no file matches", nil
	}
	var b strings.Builder
	for i, p := range paths {
		if i == maxListPaths || b.Len()+len(p)+1 > limit-cutNoteBytes {
			fmt.Fprintf(&b, "[and %d more; narrow path_glob]\n", len(paths)-i)
			break
		}
		b.WriteString(p)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// matching filters sorted paths by a path_glob: a directory ending in /, a glob on
// the whole path when it holds a /, otherwise a glob on the file name.
func matching(paths []string, glob string) ([]string, error) {
	glob = strings.TrimPrefix(glob, "./")
	if glob == "" {
		return paths, nil
	}
	if _, err := path.Match(glob, ""); err != nil {
		return nil, fmt.Errorf("path_glob %q is not a valid glob", glob)
	}
	var out []string
	for _, p := range paths {
		var ok bool
		switch {
		case strings.HasSuffix(glob, "/"):
			ok = strings.HasPrefix(p, glob)
		case strings.Contains(glob, "/"):
			ok, _ = path.Match(glob, p)
		default:
			ok, _ = path.Match(glob, path.Base(p))
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// resolvePath takes the path as given when the view holds it, since a real name may
// begin or end with a space; otherwise the form without surrounding space or "./".
func (t *viewTools) resolvePath(p string) string {
	if t.view.Has(p) {
		return p
	}
	return strings.TrimPrefix(strings.TrimSpace(p), "./")
}

// isBinary reads a NUL byte in the first 8000 bytes as binary, as git does.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
