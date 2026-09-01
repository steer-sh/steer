// Package cc is the Claude Code adapter for the commit gate. It exists because
// PreToolUse fires BEFORE the command runs, so every question the git adapter answers
// from the index has to be answered here by simulating the shell text.
package cc

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A shell path token: single-quoted, double-quoted, or bare up to whitespace or the
// next shell operator — `cd /tmp/r; git commit` must not capture the semicolon.
const pathToken = `(?P<path>'[^']*'|"[^"]*"|[^\s;&|]+)`

// dashCRe is searched only INSIDE the matched `git ... commit` invocation, where
// everything between `git` and the subcommand is by construction a git option. That
// placement, not the pattern, is what excludes `git commit -C HEAD` (reuse a commit
// message) and a -C sitting inside `-m "..."` — both fall after `commit`, outside the
// span.
var dashCRe = regexp.MustCompile(`(?:^|\s)-C\s+` + pathToken)

// cdRe finds a `cd` that relocates the repo. `[(]` so a subshell
// `(cd /r && git commit)` is seen; the leading-option skip is for `cd -P /r` and
// `cd -- /r`.
var cdRe = regexp.MustCompile(`(?:^|[;&|(]\s*)cd\s+(?:-\S+\s+)*` + pathToken)

// commitsAllTracked reports `git commit -a` / --all, where the diff is not yet staged.
var commitsAllTrackedRe = regexp.MustCompile(`\bcommit\b.*?(\s-\w*a\w*|\s--all\b)`)

// stagesRe errs toward matching, like IsCommitCommand: a `git add` appearing only
// inside a commit message flips the diff source to `git diff HEAD`, which reviews a
// superset of what lands. Reviewing too much is the tolerable direction.
var stagesRe = regexp.MustCompile(`(^|[;&|]\s*|\s)git\s+(?:\S+\s+)*?(?:add|stage)\b`)

// Span is a half-open byte range in the command text.
type Span struct{ Start, End int }

// commitSpans finds every `git ...` invocation of the subcommand, hand-rolled because
// the pattern needs a negative lookahead RE2 does not have.
//
// Two guards: the subcommand must not be followed by a word character or a dot, and the
// option tokens before it may not contain `;`, `&` or `|`. The `git` must sit at a
// command boundary — start of text, whitespace, or one of those three operators.
func commitSpans(cmd string) []Span {
	cmd = maskDocumentHeredocs(cmd)
	var spans []Span
	for i := 0; i+3 <= len(cmd); i++ {
		if cmd[i:i+3] != "git" || !atCommandBoundary(cmd, i) {
			continue
		}
		if end, ok := scanToCommit(cmd, i+3); ok {
			spans = append(spans, Span{Start: boundaryStart(cmd, i), End: end})
			// Matches are non-overlapping, left to right. `git git commit` is ONE
			// invocation whose first token happens to be `git`, not two: resuming
			// inside it would report the inner `git` as a second commit and move the
			// span the cd scan is cut at.
			i = end - 1
		}
	}
	return spans
}

// boundaryStart backs the span up over the command boundary that precedes `git`, so
// the span covers what the Python pattern's own boundary alternation consumed. The
// leftmost such start wins, exactly as the regex engine chose it: an operator plus any
// following whitespace beats the single-whitespace alternative.
func boundaryStart(cmd string, i int) int {
	if i == 0 {
		return 0
	}
	k := i
	for k > 0 && isSpace(cmd[k-1]) {
		k--
	}
	if k > 0 {
		if prev := cmd[k-1]; prev == ';' || prev == '&' || prev == '|' {
			return k - 1
		}
	}
	if k < i {
		return i - 1
	}
	return i
}

func atCommandBoundary(cmd string, i int) bool {
	if i == 0 {
		return true
	}
	prev := cmd[i-1]
	return isSpace(prev) || prev == ';' || prev == '&' || prev == '|'
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

func isSeparator(b byte) bool { return isSpace(b) || b == ';' || b == '&' || b == '|' }

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// scanToCommit walks the option tokens after `git` and returns the index just past
// the `commit` subcommand.
func scanToCommit(cmd string, pos int) (int, bool) {
	if pos >= len(cmd) || !isSpace(cmd[pos]) {
		return 0, false // `git\s+` demands at least one space
	}
	for {
		pos = skipSpace(cmd, pos)
		if strings.HasPrefix(cmd[pos:], "commit") {
			after := pos + len("commit")
			if after >= len(cmd) || (!isWordByte(cmd[after]) && cmd[after] != '.') {
				return after, true
			}
		}
		// Not the subcommand, so this must be an option token followed by whitespace.
		start := pos
		for pos < len(cmd) && !isSeparator(cmd[pos]) {
			pos++
		}
		if pos == start || pos >= len(cmd) || !isSpace(cmd[pos]) {
			return 0, false
		}
	}
}

func skipSpace(cmd string, pos int) int {
	for pos < len(cmd) && isSpace(cmd[pos]) {
		pos++
	}
	return pos
}

// CommitSpan is the span of the LAST such invocation in the command, or an empty span at
// the end. Last, not first: the real invocation is the one the chain ends on.
func CommitSpan(cmd string) Span {
	spans := commitSpans(cmd)
	if len(spans) == 0 {
		return Span{Start: len(cmd), End: len(cmd)}
	}
	return spans[len(spans)-1]
}

// IsCommitCommand reports whether this Bash command runs `git commit` for real.
//
// Deliberately permissive on shape (compound commands, heredocs, leading env) and
// conservative on intent: --dry-run rehearsals are not commits. A command this misses
// is a commit that ships ungated, which is the failure the gate exists to prevent, so
// it errs toward matching.
func IsCommitCommand(cmd string) bool {
	if len(commitSpans(cmd)) == 0 {
		return false
	}
	return !strings.Contains(cmd, "--dry-run")
}

// RepoDir resolves which repo this command writes to, honouring both `-C <path>` and a
// preceding `cd`.
//
// The shell's own resolution order is the spec: the LAST `cd` before the invocation sets
// the base, and a `-C` path then resolves against that base rather than the tool's cwd.
func RepoDir(cmd, cwd string) string {
	span := CommitSpan(cmd)
	base := cwd
	scan := maskDocumentHeredocs(cmd)
	for _, m := range cdRe.FindAllStringSubmatch(scan[:span.Start], -1) {
		base = resolveDir(m[1], base) // last cd wins, as the shell does
	}
	if m := dashCRe.FindStringSubmatch(scan[span.Start:span.End]); m != nil {
		return resolveDir(m[1], base)
	}
	return base
}

func resolveDir(token, base string) string {
	path := strings.Trim(token, `'"`)
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else if strings.HasPrefix(path, "~/") {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

// CommitsAllTracked reports `git commit -a` / --all, where the diff is not yet staged.
func CommitsAllTracked(cmd string) bool {
	return commitsAllTrackedRe.MatchString(cmd)
}

// StagesBeforeCommit reports that the command stages its own files, so the index at hook
// time is not yet the tree that lands.
//
// PreToolUse fires BEFORE the command runs, so a command that stages and then commits has
// an empty `--cached` diff when the gate reads it. Errs toward matching: reviewing a
// superset of what lands is the tolerable direction.
func StagesBeforeCommit(cmd string) bool {
	return stagesRe.MatchString(cmd)
}

// DiffNotYetStaged reports that `git diff HEAD`, not --cached, is the honest source.
func DiffNotYetStaged(cmd string) bool {
	return CommitsAllTracked(cmd) || StagesBeforeCommit(cmd)
}

// DiffSources returns the honest diff source for this command.
//
// The two predicates split here rather than sharing DiffNotYetStaged's answer: the -a form
// stages tracked files only, so untracked paths would be code that never lands, while
// `git add` may stage anything. Only the add form opts into untracked files.
func DiffSources(cmd string) (stagedOnly, includeUntracked bool) {
	stages := StagesBeforeCommit(cmd)
	return !(CommitsAllTracked(cmd) || stages), stages
}

// heredocStartRe matches only the fixed head of a heredoc, `<<` through the tag. The
// closer needs a backreference to the tag, which RE2 cannot express at all, so the
// body scan below is hand-rolled.
var heredocStartRe = regexp.MustCompile(`^<<(-?)[ \t]*(['"]?)(\w+)`)

// writesHeredocRe is the first half of WritesAFile: a heredoc operator. RE2 handles
// this half unchanged.
var writesHeredocRe = regexp.MustCompile(`<<-?\s*['"]?\w`)

// WritesAFile reports a heredoc or a `>`/`>>` redirect in the command.
//
// The redirect half is hand-rolled: it needs `(?<![0-9<>&=-])>{1,2}(?!&)`, and RE2
// has neither lookbehind nor lookahead. The guards drop `2>&1` and the `->` that
// ordinary prose puts in a `-m` message. A false positive costs one paragraph, so the
// scan errs toward matching.
func WritesAFile(cmd string) bool {
	if writesHeredocRe.MatchString(cmd) {
		return true
	}
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '>' {
			continue
		}
		run := 0
		for i+run < len(cmd) && cmd[i+run] == '>' {
			run++
		}
		if !redirectPrevOK(cmd, i) {
			i += run - 1
			continue
		}
		// A run of two or more always matches: the two-char form may be blocked by a
		// following `&`, but the one-char form then sees a `>` next, which is not `&`.
		if run >= 2 || i+1 >= len(cmd) || cmd[i+1] != '&' {
			return true
		}
		i += run - 1
	}
	return false
}

func redirectPrevOK(cmd string, i int) bool {
	if i == 0 {
		return true
	}
	return !strings.ContainsRune("0123456789<>&=-", rune(cmd[i-1]))
}

// heredoc is one `<<TAG ... TAG` block found in the command text.
type heredoc struct {
	start  int // index of the `<<`
	body   string
	quoted bool // the tag was written `'TAG'` or `"TAG"`, so the body does not expand
	from   int  // body start index in the command text
	to     int  // body end index, exclusive
}

// findHeredocs is a hand-rolled lexer, because matching a closing tag needs a
// backreference and RE2 has none.
//
// The closer is anchored to its own line, so a body line merely STARTING with the tag does
// not end the body. A `<<-` heredoc closed by an INDENTED tag is deliberately left
// unmatched, which fails toward "no message" — the safe direction.
func findHeredocs(cmd string) []heredoc {
	var out []heredoc
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] != '<' || cmd[i+1] != '<' {
			continue
		}
		m := heredocStartRe.FindStringSubmatchIndex(cmd[i:])
		if m == nil {
			continue
		}
		quote, tag := cmd[i+m[4]:i+m[5]], cmd[i+m[6]:i+m[7]]
		pos := i + m[1]
		if quote != "" {
			if pos >= len(cmd) || cmd[pos] != quote[0] {
				continue // an unbalanced quote is not a heredoc tag
			}
			pos++
		}
		// `[^\n]*\n`: the redirect may sit on either side of the operator, as in
		// `cat <<'MSG' > msg.txt`.
		nl := strings.IndexByte(cmd[pos:], '\n')
		if nl < 0 {
			continue
		}
		bodyStart := pos + nl + 1
		closer := findCloser(cmd, bodyStart, tag)
		if closer < 0 {
			continue
		}
		out = append(out, heredoc{
			start:  i,
			body:   cmd[bodyStart:closer],
			quoted: quote != "",
			from:   bodyStart,
			to:     closer,
		})
		// Matches are non-overlapping, as the regex engine's scan was: resume past
		// this heredoc's closing tag.
		i = closer + 1 + len(tag) - 1
	}
	return out
}

// findCloser returns the index of the newline that precedes the closing tag, or -1.
// The tag must own its whole line: nothing before it, and a newline or end of text
// after it.
func findCloser(cmd string, from int, tag string) int {
	for pos := from; pos < len(cmd); pos++ {
		nl := strings.IndexByte(cmd[pos:], '\n')
		if nl < 0 {
			return -1
		}
		at := pos + nl
		rest := cmd[at+1:]
		if strings.HasPrefix(rest, tag) {
			after := at + 1 + len(tag)
			if after >= len(cmd) || cmd[after] == '\n' {
				return at
			}
		}
		pos = at
	}
	return -1
}

// WrittenHeredoc returns the heredoc body this command redirects into `token`.
//
// The redirect must sit in the heredoc's OWN command segment, not merely on the same line:
// in `echo x > msg.txt && python3 - <<PY ... PY` the write belongs to `echo`.
func WrittenHeredoc(cmd, token string) (string, bool) {
	redirect := regexp.MustCompile(`>\s*` + regexp.QuoteMeta(token) + `(\s|$)`)
	for _, doc := range findHeredocs(cmd) {
		if redirect.MatchString(segment(cmd, doc.start)) {
			return doc.body, true
		}
	}
	return "", false
}

var segmentOpRe = regexp.MustCompile(`&&|\|\||;|\|`)

// segment is the one shell command around `pos`, cut at the operators bounding it.
func segment(cmd string, pos int) string {
	start, end := segmentBounds(cmd, pos)
	return cmd[start:end]
}

// segmentBounds is that same command as a byte range, so a caller can also read the
// operator the segment was cut at.
func segmentBounds(cmd string, pos int) (int, int) {
	lineStart := strings.LastIndexByte(cmd[:pos], '\n') + 1
	lineEnd := len(cmd)
	if nl := strings.IndexByte(cmd[pos:], '\n'); nl >= 0 {
		lineEnd = pos + nl
	}
	line := cmd[lineStart:lineEnd]
	at := pos - lineStart
	start, end := 0, len(line)
	for _, op := range segmentOpRe.FindAllStringIndex(line, -1) {
		if op[1] <= at {
			start = op[1]
		} else if op[0] >= at {
			end = op[0]
			break
		}
	}
	return lineStart + start, lineStart + end
}

const messageNotFound = "(not recoverable from the command line — judge the diff alone)"

var (
	dashFStdinRe = regexp.MustCompile(`-F\s+-|--file[= ]-`)
	dashFPathRe  = regexp.MustCompile(`-F\s+(\S+)`)
)

// ExtractMessage recovers the commit message, best effort. Absence is not fatal — the
// diff carries the review. Order matters: -F and -m may both appear, and a heredoc
// body is the convention in this repo's own commits.
func ExtractMessage(cmd, cwd string) string {
	if body, ok := dashMMessage(cmd); ok {
		return body
	}
	if dashFStdinRe.MatchString(cmd) {
		// From the commit onward: an earlier `python3 - <<PY` in the same command
		// belongs to that command, not to the message git reads from stdin.
		from := CommitSpan(cmd).Start
		for _, doc := range findHeredocs(cmd) {
			if doc.start >= from {
				return doc.body
			}
		}
	}
	if m := dashFPathRe.FindStringSubmatch(cmd); m != nil {
		token := strings.Trim(m[1], `'"`)
		// The heredoc beats the file: a command that writes the path it is about to
		// commit leaves nothing on disk to read when a deny stops it.
		if body, ok := WrittenHeredoc(cmd, token); ok {
			return body
		}
		if raw, err := os.ReadFile(filepath.Join(cwd, token)); err == nil {
			text := string(raw)
			if len(text) > 4000 {
				text = text[:4000]
			}
			return text
		}
	}
	return messageNotFound
}

// dashMMessage is `-m\s+(['"])(.*?)\1` hand-rolled: the closing quote is a
// backreference to the opening one, which RE2 cannot express. Leftmost `-m` whose
// quote actually closes wins, matching the regex engine's own backtracking.
func dashMMessage(cmd string) (string, bool) {
	for i := 0; i+2 < len(cmd); i++ {
		if cmd[i] != '-' || cmd[i+1] != 'm' {
			continue
		}
		pos := i + 2
		if pos >= len(cmd) || !isSpace(cmd[pos]) {
			continue
		}
		pos = skipSpace(cmd, pos)
		if pos >= len(cmd) || (cmd[pos] != '\'' && cmd[pos] != '"') {
			continue
		}
		quote := cmd[pos]
		body := cmd[pos+1:]
		if end := strings.IndexByte(body, quote); end >= 0 {
			return body[:end], true
		}
	}
	return "", false
}

// docSinkRe is a `>`/`>>` redirect to a path. The excluded previous bytes drop `2>&1`
// and the `->` an ordinary prose message contains; `1>` is the same redirect written
// with its descriptor, so it is admitted by name. The target may not open with `&`,
// which names a file descriptor rather than a file.
var docSinkRe = regexp.MustCompile(`(?:^|[^0-9<>&=-]|(?:^|[\s;&|(])1)>>?[ \t]*[^\s&;|<>]`)

// maskDocumentHeredocs blanks the bodies of heredocs that only write a file, so a
// `git commit` quoted inside documentation is text rather than an invocation. The body
// is replaced byte for byte, so every index into the result still names the same byte
// of the original command.
//
// Three conditions, and all of them are needed. The tag must be quoted, or the shell
// expands the body and the text is no longer inert. The segment's stdout may not be
// piped, because `cat <<TAG | bash` runs the body. And the segment must be a document
// write: an interpreter reading a heredoc is running code, and `bash <<TAG > log.txt`
// redirects to a file while still executing, so the redirect alone would open a gate
// bypass.
func maskDocumentHeredocs(cmd string) string {
	var out []byte
	for _, doc := range findHeredocs(cmd) {
		start, end := segmentBounds(cmd, doc.start)
		if !doc.quoted || pipedOnward(cmd, end) || !writesDocument(cmd[start:end]) {
			continue
		}
		if out == nil {
			out = []byte(cmd)
		}
		for i := doc.from; i < doc.to; i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	if out == nil {
		return cmd
	}
	return string(out)
}

// pipedOnward reports that the segment ending at `end` feeds its stdout to another
// command. `tee` mirrors its stdin to stdout and `cat` emits it, so a piped segment
// can hand the body to an interpreter and is never a document write. `||` is an
// or-list, not a pipe.
func pipedOnward(cmd string, end int) bool {
	if end >= len(cmd) || cmd[end] != '|' {
		return false
	}
	return end+1 >= len(cmd) || cmd[end+1] != '|'
}

// writesDocument reports that this one command segment writes its stdin to a file.
//
// `cat` needs the redirect, because its own arguments are files it READS. `tee` names
// the file it writes as an argument, so either form counts there.
func writesDocument(seg string) bool {
	fields := strings.Fields(seg)
	at := -1
	for i, f := range fields {
		if strings.ContainsRune(f, '=') && !strings.HasPrefix(f, "-") {
			continue // a leading `VAR=value` assignment
		}
		at = i
		break
	}
	if at < 0 {
		return false
	}
	switch filepath.Base(strings.Trim(fields[at], `'"`)) {
	case "cat":
		return docSinkRe.MatchString(seg)
	case "tee":
		return docSinkRe.MatchString(seg) || namesAPath(fields[at+1:])
	}
	return false
}

// namesAPath reports a non-option argument among `args`. A redirect operator and the
// heredoc tag standing beside it are syntax, not arguments.
func namesAPath(args []string) bool {
	skip := false
	for _, f := range args {
		if skip {
			skip = false
			continue
		}
		if isRedirectOp(f) {
			skip = true
			continue
		}
		if strings.HasPrefix(f, "-") || strings.ContainsAny(f, "<>") {
			continue
		}
		return true
	}
	return false
}

// isRedirectOp reports a bare redirect operator, whose following token is its target
// or its heredoc tag rather than an argument to the command.
func isRedirectOp(f string) bool {
	switch f {
	case "<", "<<", "<<-", ">", ">>", "1>", "1>>", "2>", "2>>":
		return true
	}
	return false
}
