package report

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/shellsyntax"
)

// runnerVerbs name the commands that EXECUTE a build, a test or a linter. The
// set is an allowlist because the alternative is unbounded: a command that only
// prints is anything else at all, and a reader is not only cat, sed and grep.
var runnerVerbs = map[string]bool{
	"go": true, "gofmt": true, "golangci-lint": true, "staticcheck": true,
	"cargo": true, "rustc": true,
	"npm": true, "npx": true, "pnpm": true, "yarn": true, "bun": true, "deno": true,
	"node": true, "tsc": true, "jest": true, "vitest": true, "eslint": true, "prettier": true,
	"python": true, "python3": true, "pytest": true, "tox": true, "uv": true, "poetry": true,
	"pip": true, "pip3": true, "ruff": true, "mypy": true, "flake8": true, "black": true, "pylint": true,
	"ruby": true, "rake": true, "rspec": true, "bundle": true,
	"php": true, "phpunit": true, "composer": true,
	"java": true, "javac": true, "mvn": true, "gradle": true, "dotnet": true, "swift": true,
	"make": true, "cmake": true, "ctest": true, "bazel": true, "ninja": true,
	"gcc": true, "g++": true, "clang": true, "clang++": true,
	"bash": true, "sh": true, "zsh": true, "shellcheck": true,
	"docker": true, "terraform": true,
}

// wrapperVerbs run another command and take its exit status. The verb that
// matters is the one after them.
var wrapperVerbs = map[string]bool{
	"sudo": true, "env": true, "time": true, "timeout": true, "nohup": true,
	"nice": true, "stdbuf": true, "command": true, "xargs": true, "watch": true,
}

// scriptSuffixes name a file that a shell executes directly.
var scriptSuffixes = []string{".sh", ".bash", ".py", ".rb", ".js", ".ts", ".pl"}

// shellRunsProgram reports whether a shell command runs a program, as opposed to
// printing text somebody else wrote. Only a runner can produce a lint, test or
// compile failure of its own; a reader that prints one is quoting a file. The
// answer is true when ANY segment of the command line is a runner, because a
// trailing pipe does not make `go test | tail` a read.
func shellRunsProgram(cmd string) bool {
	program, err := shellsyntax.ParsePartial(cmd)
	if err != nil {
		return false
	}
	for _, call := range program.Calls {
		if callRuns(call) {
			return true
		}
	}
	return false
}

// argFlags name, per wrapper, the options that take a value of their own. Only
// these consume the token after them: `sudo -u root go test` runs go, while
// `sudo -E cat deploy.sh` reads a file. A wrapper's own boolean flags are not
// listed, so the command after them decides.
var argFlags = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-C": true, "--user": true, "--group": true},
	"env":     {"-u": true, "-C": true, "-S": true, "--unset": true, "--chdir": true},
	"xargs":   {"-I": true, "-i": true, "-n": true, "-L": true, "-P": true, "-s": true, "-a": true, "-d": true, "-E": true},
	"nice":    {"-n": true},
	"timeout": {"-k": true, "-s": true, "--kill-after": true, "--signal": true},
	"watch":   {"-n": true, "--interval": true},
	"time":    {"-o": true, "-f": true, "--output": true, "--format": true},
	"stdbuf":  {"-i": true, "-o": true, "-e": true},
}

// callRuns answers shellRunsProgram for one parsed call: it finds the verb, past
// any leading variable assignment and any wrapper, and tests it. A wrapper takes
// options of its own (`sudo -u root go test`, `xargs -I {} pytest`), so past one
// an unrecognized token continues the search rather than answering it. Before
// one, the first token IS the verb and answers for the segment: scanning on
// would read the argument of `grep "go test"` as a command.
func callRuns(call shellsyntax.Call) bool {
	wrapped, afterFlag, wrapper := false, false, ""
	for _, word := range call.Args {
		if !word.Static {
			return false
		}
		verb := word.Value
		if isAssignment(verb) {
			continue
		}
		if isDuration(verb) {
			// A bare number is the wrapper's limit, and it is the argument any
			// preceding option was waiting for.
			afterFlag = false
			continue
		}
		if strings.HasPrefix(verb, "-") {
			afterFlag = argFlags[wrapper][verb] && !strings.Contains(verb, "=")
			continue
		}
		base := path.Base(verb)
		if wrapperVerbs[base] {
			wrapped, afterFlag, wrapper = true, false, base
			continue
		}
		if runnerVerbs[base] || isScript(verb, base) {
			return true
		}
		// Past a wrapper, a token that runs nothing can still belong to the
		// wrapper: the argument of its own option (`xargs -I {} pytest`). Any
		// other token is the command the wrapper runs, and it decides —
		// `sudo cat script.py` reads a file.
		if wrapped && afterFlag {
			afterFlag = false
			continue
		}
		return false
	}
	return false
}

// isDuration reports whether a token is a bare number, with or without a time
// unit. `timeout 60 go test` names its limit between the wrapper and the verb.
func isDuration(tok string) bool {
	t := strings.TrimRight(tok, "smh")
	digit := false
	for i := 0; i < len(t); i++ {
		switch {
		case t[i] >= '0' && t[i] <= '9':
			digit = true
		case t[i] == '.':
		default:
			return false
		}
	}
	return digit
}

// isAssignment reports whether a token is a leading `NAME=value` assignment
// rather than the verb.
func isAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	if i <= 0 || strings.ContainsAny(tok[:i], "/ '\"") {
		return false
	}
	return true
}

// isScript reports whether a token names a file the shell executes: a path
// relative to the working directory, or a name carrying a script suffix. An
// absolute path alone is NOT one: `/bin/cat` and `/usr/bin/grep` are the readers
// this whole file exists to exclude, and a script named absolutely still carries
// its suffix.
func isScript(tok, base string) bool {
	if strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") {
		return true
	}
	for _, s := range scriptSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return false
}

// failureMatch finds the first failurePattern match that has the SHAPE of a
// failure a command reported: it sits outside a quoted string. A failure echoed
// out of a source file is the text of the message it belongs to, inside the
// quotes that make it a string. A later match on the same output still counts,
// so a run that prints one source line before failing is not lost.
func failureMatch(text string) (int, bool) {
	for _, loc := range failurePattern.FindAllStringIndex(text, -1) {
		start := strings.LastIndexByte(text[:loc[0]], '\n') + 1
		if !insideQuotes(text[start:loc[0]]) {
			return loc[0], true
		}
	}
	return 0, false
}

// insideQuotes reports whether the text before a match on its line opens a quote
// it does not close.
func insideQuotes(prefix string) bool {
	var double, single, back bool
	for i := 0; i < len(prefix); i++ {
		switch prefix[i] {
		case '\\':
			i++
		case '"':
			if !single && !back {
				double = !double
			}
		case '\'':
			// An apostrophe between two letters is a contraction, not a quote:
			// `couldn't connect: build failed` reports a real failure. The test
			// comes first, or a contraction inside backticks would close them.
			if isLetter(byteAt(prefix, i-1)) && isLetter(byteAt(prefix, i+1)) {
				continue
			}
			// GNU tools quote a name as `name', so a closing apostrophe ends an
			// open backtick: "No rule to make target `test'." quotes nothing on.
			if back {
				back = false
				continue
			}
			if !double && !back {
				single = !single
			}
		case '`':
			if !double && !single {
				back = !back
			}
		}
	}
	return double || single || back
}

// sortedRunnerVerbs renders the allowlist for ParserInstrument, so adding a verb
// reads as instrument drift rather than as a moved failure rate.
func sortedRunnerVerbs() []string {
	out := make([]string, 0, len(runnerVerbs))
	for v := range runnerVerbs {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// byteAt returns the byte at i, or 0 when i is outside s.
func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

// isLetter reports whether b is an ASCII letter.
func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// tracebackOpener is the first line of a Python traceback. The line names no
// cause, so it is the failurePattern match that carries the least.
const tracebackOpener = "Traceback (most recent call last)"

// exceptionLine is the LAYOUT half of the shape test: a bare dotted identifier
// that ends the line or introduces a message. `--- FAIL:` opens with a dash and
// `FAILED tests/x.py` carries no colon, so neither reaches isExceptionName,
// which decides whether the identifier is Python's.
var exceptionLine = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*(?::|$)`)

// chainNotes are the sentences CPython prints between the tracebacks of one
// chain. An opener that follows one continues the chain; an opener that does
// not begins an unrelated crash.
var chainNotes = []string{
	"During handling of the above exception, another exception occurred:",
	"The above exception was the direct cause of the following exception:",
}

// tracebackException returns the exception line the Python traceback at the head
// of text ends on, or "" when it holds none. CPython prints every frame deeper
// than the opener and returns the exception to the opener's own column, so the
// exception is the next line at EXACTLY that depth. Reading depths relative to
// the opener lets a traceback indented as a whole — inside a test runner's
// output — read the same as a bare one, and leaves a shallower runner banner to
// the runner.
//
// A traceback cut off before its exception yields "": the line that lands at the
// opener's depth has to have an exception's shape, or nothing is claimed and the
// caller keeps the opener.
//
// A chain reports the outermost exception last, so a later link replaces an
// earlier one. An opener arriving with no chain note before it is a separate
// crash and ends the read — whether or not the first traceback yielded
// anything, because the caller's match is the failure being keyed and a later
// unrelated crash is not it.
func tracebackException(text string) string {
	var found string
	indent := -1
	seen := false
	chained := true
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		body := strings.TrimLeft(trimmed, " \t")
		if body == "" {
			continue
		}
		depth := len(trimmed) - len(body)
		if strings.HasPrefix(body, tracebackOpener) {
			if seen && !chained {
				return found
			}
			indent = depth
			seen = true
			chained = false
			continue
		}
		if isChainNote(body) {
			chained = true
			continue
		}
		if indent < 0 {
			continue
		}
		// Frames sit deeper and the exception sits level. A shallower line is
		// outside the block, so the traceback ended without one.
		if depth < indent {
			indent = -1
			continue
		}
		if depth > indent {
			continue
		}
		indent = -1
		if exceptionLine.MatchString(body) && isExceptionName(body) {
			found = body
		}
	}
	return found
}

func isChainNote(body string) bool {
	for _, note := range chainNotes {
		if strings.HasPrefix(body, note) {
			return true
		}
	}
	return false
}

// isExceptionName reports whether the line's leading identifier names a Python
// exception type. Every test runs on the TAIL — the segment after the last dot
// — because the module prefix is not the type: `logger.ERROR:` carries lower
// case in `logger` while naming no exception at all.
//
// An exception type is CamelCase, so a tail with no lower-case letter is a
// status marker (`FAIL:`, `ERROR:`) or a file extension (`TestFoo.C:10:`) and
// never Python's. A leading underscore is part of a private type's name, not
// part of its case. The lower-case types the standard library ships all carry
// `error` in the tail — `re.error`, `socket.gaierror`, `ftplib.error_perm` —
// and are qualified by their module, which is what keeps `test_foo.py:10:` out.
func isExceptionName(body string) bool {
	name := body
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	tail := name
	dotted := false
	if i := strings.LastIndexByte(tail, '.'); i >= 0 {
		tail = tail[i+1:]
		dotted = true
	}
	tail = strings.TrimLeft(tail, "_")
	if tail == "" || !strings.ContainsAny(tail, "abcdefghijklmnopqrstuvwxyz") {
		return false
	}
	if tail[0] >= 'A' && tail[0] <= 'Z' {
		return true
	}
	return dotted && strings.Contains(tail, "error")
}
