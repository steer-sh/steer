package cc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/shellsyntax"
	"mvdan.cc/sh/v3/syntax"
)

type parsedGit struct {
	program    *shellsyntax.Program
	call       shellsyntax.Call
	callIndex  int
	gitArg     int
	subcommand int
	baseCWD    string
	gitCWD     string
	workTree   string
	gitDirOnly bool
	multiple   bool
}

func parseCommit(cmd, cwd string) (*parsedGit, error) {
	program, err := shellsyntax.Parse(cmd)
	if err != nil {
		return nil, err
	}
	var found *parsedGit
	for i, call := range program.Calls {
		gitArg, ok := gitExecutable(call.Args)
		if !ok {
			continue
		}
		sub, gitCWD, workTree, gitDirOnly, ok := gitSubcommand(call.Args, gitArg, cwd)
		if !ok || call.Args[sub].Value != "commit" || commitHasDryRun(call.Args[sub+1:]) {
			continue
		}
		multiple := found != nil
		found = &parsedGit{program: program, call: call, callIndex: i, gitArg: gitArg, baseCWD: cwd,
			subcommand: sub, gitCWD: gitCWD, workTree: workTree, gitDirOnly: gitDirOnly, multiple: multiple}
	}
	return found, nil
}

func gitExecutable(args []shellsyntax.Word) (int, bool) {
	for i := 0; i < len(args); {
		if !args[i].Static {
			return 0, false
		}
		switch args[i].Base() {
		case "git":
			return i, true
		case "command":
			i++
			for i < len(args) && args[i].Static && strings.HasPrefix(args[i].Value, "-") {
				if args[i].Value == "-v" || args[i].Value == "-V" {
					return 0, false
				}
				i++
			}
		case "env":
			i++
			for i < len(args) {
				if !args[i].Static {
					return 0, false
				}
				v := args[i].Value
				if v == "-u" || v == "-C" || v == "-S" || v == "--unset" || v == "--chdir" {
					i += 2
					continue
				}
				if strings.HasPrefix(v, "-") || strings.Contains(v, "=") {
					i++
					continue
				}
				break
			}
		case "sudo":
			i++
			for i < len(args) && args[i].Static && strings.HasPrefix(args[i].Value, "-") {
				v := args[i].Value
				i++
				if v == "-u" || v == "-g" || v == "-C" || v == "--user" || v == "--group" {
					i++
				}
			}
		case "nohup":
			i++
		default:
			return 0, false
		}
	}
	return 0, false
}

func gitSubcommand(args []shellsyntax.Word, gitArg int, cwd string) (sub int, gitCWD, workTree string, gitDirOnly, ok bool) {
	gitCWD = cwd
	var gitDir bool
	for i := gitArg + 1; i < len(args); i++ {
		if !args[i].Static {
			return 0, "", "", false, false
		}
		token := args[i].Value
		if token == "--" {
			if i+1 >= len(args) || !args[i+1].Static {
				return 0, "", "", false, false
			}
			return i + 1, gitCWD, workTree, gitDir && workTree == "", true
		}
		if !strings.HasPrefix(token, "-") {
			return i, gitCWD, workTree, gitDir && workTree == "", true
		}
		name, value, attached := strings.Cut(token, "=")
		switch name {
		case "-C", "-c", "--config-env", "--git-dir", "--work-tree", "--namespace":
			if !attached {
				i++
				if i >= len(args) || !args[i].Static {
					return 0, "", "", false, false
				}
				value = args[i].Value
			}
			switch name {
			case "-C":
				gitCWD = resolveDir(value, gitCWD)
			case "--work-tree":
				workTree = resolveDir(value, gitCWD)
			case "--git-dir":
				gitDir = true
			}
		}
	}
	return 0, "", "", false, false
}

func commitHasDryRun(args []shellsyntax.Word) bool {
	for i := 0; i < len(args); i++ {
		if !args[i].Static {
			continue
		}
		token := args[i].Value
		if token == "--" {
			return false
		}
		if token == "--dry-run" {
			return true
		}
		name, _, attached := strings.Cut(token, "=")
		if commitOptionTakesValue(name) && !attached {
			i++
			continue
		}
		if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") {
			kind, value := shortCommitValue(token)
			if strings.ContainsRune("mFCct", rune(kind)) && value == "" {
				i++
			}
		}
	}
	return false
}

func commitOptionTakesValue(name string) bool {
	switch name {
	case "-m", "--message", "-F", "--file", "-C", "--reuse-message", "-c", "--reedit-message",
		"--fixup", "--squash", "--cleanup", "--author", "--date", "-t", "--template",
		"--trailer", "--pathspec-from-file":
		return true
	}
	return false
}

func (p *parsedGit) repoDir() (string, bool) {
	if p.gitDirOnly {
		return "", false
	}
	gitCWD, workTree, ok := p.resolvedDirs()
	if !ok {
		return "", false
	}
	if workTree != "" {
		return workTree, true
	}
	return gitCWD, true
}

func (p *parsedGit) resolvedDirs() (gitCWD, workTree string, ok bool) {
	base := p.baseCWD
	for _, call := range p.program.Calls[:p.callIndex] {
		if !scopeAffects(call.Scopes, p.call.Scopes) || len(call.Args) == 0 || call.Args[0].Base() != "cd" {
			continue
		}
		path, ok := cdPath(call.Args[1:])
		if !ok {
			return "", "", false
		}
		base = resolveDir(path, base)
	}
	// Git applies -C after the shell has applied cd. Re-evaluate it from that base.
	_, gitCWD, workTree, gitDirOnly, ok := gitSubcommand(p.call.Args, p.gitArg, base)
	if !ok || gitDirOnly {
		return "", "", false
	}
	return gitCWD, workTree, true
}

func scopeAffects(candidate, target []int) bool {
	if len(candidate) > len(target) {
		return false
	}
	for i := range candidate {
		if candidate[i] != target[i] {
			return false
		}
	}
	return true
}

func cdPath(args []shellsyntax.Word) (string, bool) {
	for _, arg := range args {
		if !arg.Static {
			return "", false
		}
		if arg.Value == "--" || strings.HasPrefix(arg.Value, "-") {
			continue
		}
		return arg.Value, true
	}
	return "", false
}

func (p *parsedGit) explicitlySelectsRepo() bool {
	if p.gitDirOnly {
		return false
	}
	known := false
	for _, call := range p.program.Calls[:p.callIndex] {
		if !scopeAffects(call.Scopes, p.call.Scopes) || len(call.Args) == 0 || call.Args[0].Base() != "cd" {
			continue
		}
		path, ok := cdPath(call.Args[1:])
		if !ok {
			return false
		}
		known = known || filepath.IsAbs(expandToken(path)) || strings.HasPrefix(path, "~/")
	}
	for i := p.gitArg + 1; i < p.subcommand; i++ {
		if !p.call.Args[i].Static {
			return false
		}
		token := p.call.Args[i].Value
		name, value, attached := strings.Cut(token, "=")
		if name != "-C" && name != "--work-tree" {
			if takesGitValue(name) && !attached {
				i++
			}
			continue
		}
		if !attached {
			i++
			if i >= p.subcommand || !p.call.Args[i].Static {
				return false
			}
			value = p.call.Args[i].Value
		}
		known = known || filepath.IsAbs(expandToken(value)) || strings.HasPrefix(value, "~/")
	}
	return known
}

func takesGitValue(name string) bool {
	switch name {
	case "-C", "-c", "--config-env", "--git-dir", "--work-tree", "--namespace":
		return true
	}
	return false
}

func (p *parsedGit) message() (string, bool) {
	gitCWD, _, ok := p.resolvedDirs()
	if !ok {
		return "", false
	}
	args := p.call.Args[p.subcommand+1:]
	var messages []string
	for i := 0; i < len(args); i++ {
		if !args[i].Static {
			raw := args[i].Raw
			if strings.HasPrefix(raw, "-m") || strings.HasPrefix(raw, "--message=") ||
				strings.HasPrefix(raw, "-F") || strings.HasPrefix(raw, "--file=") {
				return "", false
			}
			continue
		}
		token := args[i].Value
		switch {
		case token == "--":
			i = len(args)
		case token == "-m" || token == "--message":
			i++
			if i >= len(args) || !args[i].Static {
				return "", false
			}
			messages = append(messages, args[i].Value)
		case strings.HasPrefix(token, "--message="):
			messages = append(messages, strings.TrimPrefix(token, "--message="))
		case token == "-F" || token == "--file":
			i++
			if i >= len(args) {
				return "", false
			}
			message, ok := p.messageFile(args[i], gitCWD)
			if !ok {
				return "", false
			}
			messages = append(messages, message)
		case strings.HasPrefix(token, "--file="):
			path := strings.TrimPrefix(token, "--file=")
			message, ok := p.messageFile(shellsyntax.Word{Value: path, Raw: path, Static: true}, gitCWD)
			if !ok {
				return "", false
			}
			messages = append(messages, message)
		case strings.HasPrefix(token, "--"):
			name, _, attached := strings.Cut(token, "=")
			if commitOptionTakesValue(name) && !attached {
				i++
			}
		case strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--"):
			kind, attached := shortCommitValue(token)
			if kind == 0 {
				continue
			}
			var word shellsyntax.Word
			if attached != "" {
				word = shellsyntax.Word{Value: attached, Raw: attached, Static: true}
			} else if kind != 'S' {
				i++
				if i >= len(args) {
					return "", false
				}
				word = args[i]
			}
			if kind == 'm' {
				if !word.Static {
					return "", false
				}
				messages = append(messages, word.Value)
			} else if kind == 'F' {
				message, ok := p.messageFile(word, gitCWD)
				if !ok {
					return "", false
				}
				messages = append(messages, message)
			}
		}
	}
	if len(messages) > 0 {
		return strings.Join(messages, "\n\n"), true
	}
	return messageNotFound, true
}

func shortCommitValue(token string) (kind byte, attached string) {
	for i := 1; i < len(token); i++ {
		switch token[i] {
		case 'm', 'F', 'C', 'c', 't':
			return token[i], token[i+1:]
		case 'S':
			return 'S', token[i+1:]
		}
	}
	return 0, ""
}

func (p *parsedGit) messageFile(word shellsyntax.Word, repo string) (string, bool) {
	if !word.Static {
		if body, ok := p.writtenHeredoc(word.Key()); ok {
			return body, true
		}
		return "", false
	}
	if word.Value == "-" {
		for _, redir := range p.call.Redirects {
			if (redir.Op == syntax.Hdoc || redir.Op == syntax.DashHdoc) && redir.HeredocStatic {
				return trimHeredoc(redir.Heredoc), true
			}
		}
		return "", false
	}
	if body, ok := p.writtenHeredoc(word.Key()); ok {
		return body, true
	}
	return p.messagePath(word.Value, repo)
}

func (p *parsedGit) messagePath(path, repo string) (string, bool) {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(repo, resolved)
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return messageNotFound, true
	}
	if len(raw) > 4000 {
		raw = raw[:4000]
	}
	return string(raw), true
}

func (p *parsedGit) writtenHeredoc(target string) (string, bool) {
	for _, call := range p.program.Calls[:p.callIndex] {
		var body string
		for _, redir := range call.Redirects {
			if (redir.Op == syntax.Hdoc || redir.Op == syntax.DashHdoc) && redir.HeredocStatic {
				body = trimHeredoc(redir.Heredoc)
			}
		}
		if body == "" {
			continue
		}
		for _, redir := range call.Redirects {
			if outputRedirect(redir) && redir.Target.Key() == target {
				return body, true
			}
		}
	}
	return "", false
}

func trimHeredoc(body string) string { return strings.TrimSuffix(body, "\n") }

func outputRedirect(redir shellsyntax.Redirect) bool {
	switch redir.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.ClbOut, syntax.RdrInOut:
		return true
	}
	return false
}

func parsedWritesFile(program *shellsyntax.Program) bool {
	for _, call := range program.Calls {
		for _, redir := range call.Redirects {
			if outputRedirect(redir) || redir.Op == syntax.Hdoc || redir.Op == syntax.DashHdoc {
				return true
			}
		}
	}
	return false
}

func (p *parsedGit) commitsAllTracked() bool {
	args := p.call.Args[p.subcommand+1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !arg.Static {
			continue
		}
		token := arg.Value
		if token == "--" {
			break
		}
		if token == "--all" {
			return true
		}
		if strings.HasPrefix(token, "--") {
			name, _, attached := strings.Cut(token, "=")
			if commitOptionTakesValue(name) && !attached {
				i++
			}
			continue
		}
		if !strings.HasPrefix(token, "-") {
			continue
		}
		for j := 1; j < len(token); j++ {
			if token[j] == 'a' {
				return true
			}
			if strings.ContainsRune("mFCctSu", rune(token[j])) {
				if j+1 == len(token) && !strings.ContainsRune("Su", rune(token[j])) {
					i++
				}
				break
			}
		}
	}
	return false
}

// selectsPaths reports a commit that chooses its own paths: a pathspec, --only,
// --include or --pathspec-from-file. A dynamic positional word counts, since it may
// be a path.
func (p *parsedGit) selectsPaths() bool {
	args := p.call.Args[p.subcommand+1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// A dynamic word is judged by its literal option text, and any value in it
		// is attached.
		token, whole := arg.Value, arg.Static
		if !whole {
			token = literalOptionPrefix(arg.Raw)
			if strings.TrimLeft(token, "-") == "" {
				return true
			}
		}
		if whole && token == "--" {
			return i+1 < len(args)
		}
		if !strings.HasPrefix(token, "-") || token == "-" {
			return true
		}
		if strings.HasPrefix(token, "--") {
			name, _, attached := strings.Cut(token, "=")
			switch name {
			case "--only", "--include", "--pathspec-from-file":
				return true
			}
			if commitOptionTakesValue(name) && !attached && whole {
				i++
			}
			continue
		}
		for j := 1; j < len(token); j++ {
			if token[j] == 'o' || token[j] == 'i' {
				return true
			}
			if strings.ContainsRune("mFCctSu", rune(token[j])) {
				if j+1 == len(token) && whole && !strings.ContainsRune("Su", rune(token[j])) {
					i++
				}
				break
			}
		}
	}
	return false
}

// literalOptionPrefix returns the option text a raw shell word starts with, up to its
// first quote or expansion. Empty when the word does not start with a literal `-`.
func literalOptionPrefix(raw string) string {
	if !strings.HasPrefix(raw, "-") {
		return ""
	}
	end := strings.IndexFunc(raw, func(r rune) bool {
		return !(r == '-' || r == '=' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
	if end < 0 {
		return raw
	}
	return raw[:end]
}

func parsedGitCalls(program *shellsyntax.Program, cwd string, names ...string) []parsedGit {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	var out []parsedGit
	for i, call := range program.Calls {
		gitArg, ok := gitExecutable(call.Args)
		if !ok {
			continue
		}
		sub, gitCWD, workTree, gitDirOnly, ok := gitSubcommand(call.Args, gitArg, cwd)
		if !ok || !wanted[call.Args[sub].Value] {
			continue
		}
		out = append(out, parsedGit{program: program, call: call, callIndex: i, baseCWD: cwd,
			gitArg: gitArg, subcommand: sub, gitCWD: gitCWD, workTree: workTree, gitDirOnly: gitDirOnly})
	}
	return out
}

func parsedStagesBefore(program *shellsyntax.Program, commit *parsedGit) bool {
	for _, add := range parsedGitCalls(program, "", "add", "stage") {
		if add.call.Start < commit.call.Start {
			return true
		}
	}
	return false
}

// parsedStagedPathspecs returns the paths the adds before the commit name. An add's
// paths resolve against its own directory and the diff runs in the commit's, so an
// add whose directory, after cd and -C, differs from the commit's yields nil.
func parsedStagedPathspecs(program *shellsyntax.Program, commit *parsedGit) []string {
	commitCWD, commitTree, ok := commit.resolvedDirs()
	if !ok {
		return nil
	}
	var out []string
	for _, add := range parsedGitCalls(program, commit.baseCWD, "add", "stage") {
		if add.call.Start >= commit.call.Start {
			continue
		}
		if add.gitDirOnly {
			return nil
		}
		addCWD, addTree, ok := add.resolvedDirs()
		if !ok || addCWD != commitCWD || addTree != commitTree {
			return nil
		}
		literal := false
		var specs []string
		for _, arg := range add.call.Args[add.subcommand+1:] {
			if !arg.Static {
				return nil
			}
			if !literal && arg.Value == "--" {
				literal = true
				continue
			}
			if !literal && strings.HasPrefix(arg.Value, "-") {
				if strings.HasPrefix(arg.Value, "--pathspec-from-file") {
					return nil
				}
				continue
			}
			specs = append(specs, arg.Value)
		}
		if len(specs) == 0 {
			return nil
		}
		out = append(out, specs...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func shellAmbiguity(cmd, cwd string) string {
	p, err := parseCommit(cmd, cwd)
	if err != nil {
		return fmt.Sprintf("the shell syntax could not be parsed (%v)", err)
	}
	if p == nil {
		return "only the conservative detector recognized a commit"
	}
	if p.multiple {
		return "the shell command contains multiple commits"
	}
	if _, ok := p.repoDir(); !ok {
		return "the commit's repository is dynamic or selected only by --git-dir"
	}
	if _, ok := p.message(); !ok {
		return "the commit message path or value is dynamic"
	}
	return ""
}

const ambiguousShellReason = "[commit-gate] BLOCKED: %s, so the gate cannot establish " +
	"the repository and commit message safely. Re-run the commit as a plain command " +
	"with an absolute repository and a literal message, for example " +
	"`git -C /absolute/path/to/repo commit -m 'subject'`."

func AmbiguousShellReason(cmd, cwd string) string {
	why := shellAmbiguity(cmd, cwd)
	if why == "" {
		return ""
	}
	return fmt.Sprintf(ambiguousShellReason, why)
}

// AmbiguousShellReceipt states that conservative detection blocked before review.
const AmbiguousShellReceipt = "[commit-gate] refused an ambiguous shell commit. " +
	"No review ran and no adjudication row was written."
