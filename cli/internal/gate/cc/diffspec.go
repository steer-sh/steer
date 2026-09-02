package cc

import (
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// DiffSpecFor is the one owner of the tree the reviewers read for a Bash command.
// Both harness adapters call it, so the staging simulation cannot answer one of them
// differently from the other.
func DiffSpecFor(cmd string) gate.DiffSpec {
	stagedOnly, includeUntracked := DiffSources(cmd)
	spec := gate.DiffSpec{StagedOnly: stagedOnly, IncludeUntracked: includeUntracked}
	if includeUntracked {
		spec.UntrackedPathspecs = StagedPathspecs(cmd)
	}
	return spec
}

// StagedPathspecs returns the paths the command's own `git add` names. An empty return
// means the command stages the whole repo, or names its paths in a form this cannot
// read.
func StagedPathspecs(cmd string) []string {
	scan := maskDocumentHeredocs(cmd)
	var out []string
	for _, span := range addSpans(scan) {
		// `git -C <dir> add x` resolves x against a directory this does not track.
		if strings.Contains(scan[span.Start:span.End], "-C") {
			return nil
		}
		specs, ok := addPathspecs(scan[span.End:])
		if !ok || len(specs) == 0 {
			return nil
		}
		out = append(out, specs...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// addPathspecs reads the arguments of one `git add` up to the next command boundary.
// ok=false means a token this cannot resolve to a path.
func addPathspecs(tail string) (specs []string, ok bool) {
	literal := false
	for pos := 0; pos < len(tail); {
		pos = skipSpace(tail, pos)
		if pos >= len(tail) || tail[pos] == ';' || tail[pos] == '&' || tail[pos] == '|' {
			break
		}
		token, next := readToken(tail, pos)
		pos = next
		if !literal {
			if token == "--" {
				literal = true
				continue
			}
			if strings.HasPrefix(token, "-") {
				// The file this option names holds the pathspecs, and reading it here
				// would be a second guess at what the shell will do.
				if strings.HasPrefix(token, "--pathspec-from-file") {
					return nil, false
				}
				continue
			}
		}
		path := strings.Trim(token, `'"`)
		// A substitution or a variable expands to text this does not have.
		if path == "" || strings.ContainsAny(path, "$`") {
			return nil, false
		}
		specs = append(specs, path)
	}
	return specs, true
}

// readToken reads one shell word, honouring a quoted run so a quoted path holding a
// space or an operator stays whole.
func readToken(text string, pos int) (string, int) {
	start := pos
	var quote byte
	for pos < len(text) {
		c := text[pos]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			pos++
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			pos++
			continue
		}
		if isSeparator(c) {
			break
		}
		pos++
	}
	return text[start:pos], pos
}

// addSpans finds every `git ... add` / `git ... stage` invocation. Span.End sits just
// past the subcommand word, so the arguments follow it.
func addSpans(cmd string) []Span {
	var spans []Span
	for i := 0; i+3 <= len(cmd); i++ {
		if cmd[i:i+3] != "git" || !atCommandBoundary(cmd, i) {
			continue
		}
		if end, ok := scanToSubcommand(cmd, i+3, "add", "stage"); ok {
			spans = append(spans, Span{Start: i, End: end})
			i = end - 1
		}
	}
	return spans
}

// scanToSubcommand walks the option tokens after `git` and returns the index just past
// one of `names`. It is scanToCommit's rule over a wider set.
func scanToSubcommand(cmd string, pos int, names ...string) (int, bool) {
	if pos >= len(cmd) || !isSpace(cmd[pos]) {
		return 0, false
	}
	for {
		pos = skipSpace(cmd, pos)
		for _, name := range names {
			if !strings.HasPrefix(cmd[pos:], name) {
				continue
			}
			after := pos + len(name)
			if after >= len(cmd) || (!isWordByte(cmd[after]) && cmd[after] != '.') {
				return after, true
			}
		}
		start := pos
		for pos < len(cmd) && !isSeparator(cmd[pos]) {
			pos++
		}
		if pos == start || pos >= len(cmd) || !isSpace(cmd[pos]) {
			return 0, false
		}
	}
}
