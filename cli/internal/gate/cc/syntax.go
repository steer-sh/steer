package cc

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/steer-sh/steer-cli/internal/shellsyntax"
	"github.com/steer-sh/steer-cli/internal/steerpath"
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
		if !ok || call.Args[sub].Value != "commit" || commitDryRun(call.Args[sub+1:]) {
			continue
		}
		multiple := found != nil
		found = &parsedGit{program: program, call: call, callIndex: i, gitArg: gitArg, baseCWD: cwd,
			subcommand: sub, gitCWD: gitCWD, workTree: workTree, gitDirOnly: gitDirOnly, multiple: multiple}
	}
	return found, nil
}

func gitExecutable(args []shellsyntax.Word) (int, bool) {
	i, _, ok := gitInvocation(args)
	return i, ok
}

// gitInvocation also names an env directory change the gate does not reproduce.
func gitInvocation(args []shellsyntax.Word) (int, string, bool) {
	location := ""
	for i := 0; i < len(args); {
		if !args[i].Static {
			return 0, "", false
		}
		switch args[i].Base() {
		case "git":
			return i, location, true
		case "command":
			i++
			for i < len(args) && args[i].Static && strings.HasPrefix(args[i].Value, "-") {
				if args[i].Value == "-v" || args[i].Value == "-V" {
					return 0, "", false
				}
				i++
			}
		case "env":
			i++
			for i < len(args) {
				if !args[i].Static {
					return 0, "", false
				}
				v := args[i].Value
				if v == "-C" || v == "--chdir" || strings.HasPrefix(v, "--chdir=") ||
					strings.HasPrefix(v, "-C") && len(v) > 2 {
					location = "env --chdir/-C"
				}
				if v == "--" {
					i++
					break
				}
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
			return 0, "", false
		}
	}
	return 0, "", false
}

// gitLocationOption checks git calls before or on the commit, including a bundled add.
func (p *parsedGit) gitLocationOption() string {
	for _, call := range p.program.Calls {
		if call.Start >= p.call.End || !scopeAffects(call.Scopes, p.call.Scopes) {
			continue
		}
		gitArg, location, ok := gitInvocation(call.Args)
		if !ok {
			continue
		}
		if location != "" {
			return location
		}
		sub, _, _, _, ok := gitSubcommand(call.Args, gitArg, p.baseCWD)
		if !ok {
			continue
		}
		for i := gitArg + 1; i < sub; i++ {
			name, _, attached := strings.Cut(call.Args[i].Value, "=")
			switch name {
			case "--git-dir", "--work-tree", "--bare":
				return name
			}
			if takesGitValue(name) && !attached {
				i++
			}
		}
	}
	return ""
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
		// The shell leaves a `~` after an option's `=` as it is.
		name, value, attached := strings.Cut(token, "=")
		tilde := false
		switch name {
		case "-C", "-c", "--config-env", "--git-dir", "--work-tree", "--namespace":
			if !attached {
				i++
				if i >= len(args) || !args[i].Static {
					return 0, "", "", false, false
				}
				value, tilde = args[i].Value, args[i].TildePrefix()
			}
			switch name {
			case "-C":
				gitCWD = resolveDir(value, tilde, gitCWD)
			case "--work-tree":
				workTree = resolveDir(value, tilde, gitCWD)
			case "--git-dir":
				gitDir = true
			}
		}
	}
	return 0, "", "", false, false
}

// commitDryRun reports that a commit's own options leave --dry-run in effect for the
// whole call: the flag is set, no later --no-dry-run (or an abbreviation of it) clears
// it, and no free-standing dynamic word stands among the options, since one could
// clear it or be a value option that takes `--dry-run` as its value.
func commitDryRun(args []shellsyntax.Word) bool {
	dry := false
	for i := 0; i < len(args); i++ {
		if !args[i].Static {
			return false
		}
		token := args[i].Value
		if token == "--" {
			break
		}
		name, _, attached := strings.Cut(token, "=")
		switch {
		case token == "--dry-run":
			dry = true
		case strings.HasPrefix(name, "--no-d"):
			dry = false
		case commitOptionTakesValue(name) && !attached:
			i++
		case strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--"):
			kind, value := shortCommitValue(token)
			if strings.ContainsRune("mFCct", rune(kind)) && value == "" {
				i++
			}
		}
	}
	return dry
}

// commitCall is one parsed `git commit` call, located in the source so the loose scan
// can tell its own match from text elsewhere.
type commitCall struct {
	dryRun      bool
	commitStart int // byte range of the subcommand word
	commitEnd   int
	inert       [][2]int // fixed arguments and static heredoc bodies the call reads as data
}

// commitCalls returns every parsed commit call, real or dry run.
func commitCalls(program *shellsyntax.Program) []commitCall {
	var out []commitCall
	for _, call := range program.Calls {
		gitArg, ok := gitExecutable(call.Args)
		if !ok {
			continue
		}
		sub, _, _, _, ok := gitSubcommand(call.Args, gitArg, "")
		if !ok || call.Args[sub].Value != "commit" {
			continue
		}
		found := commitCall{dryRun: commitDryRun(call.Args[sub+1:]),
			commitStart: call.Args[sub].Start, commitEnd: call.Args[sub].End}
		for _, arg := range call.Args[sub+1:] {
			if _, fixed := shellsyntax.FixedValue(arg); fixed {
				found.inert = append(found.inert, [2]int{arg.Start, arg.End})
			}
		}
		for _, redir := range call.Redirects {
			if redir.HeredocStatic {
				found.inert = append(found.inert, [2]int{redir.HeredocStart, redir.HeredocEnd})
			}
		}
		out = append(out, found)
	}
	return out
}

// owns reports that a loose match ends at this call's own subcommand word, or lies
// inside one of its static arguments or static heredoc bodies. The match may start at
// an earlier `git` than the call's, as in `GIT_DIR=/r/.git git`, so only its end
// identifies the call.
func (c commitCall) owns(span Span) bool {
	if span.End-len("commit") == c.commitStart && span.End == c.commitEnd {
		return true
	}
	for _, r := range c.inert {
		if span.Git >= r[0] && span.End <= r[1] {
			return true
		}
	}
	return false
}

// commitHasFlag reports a long flag among the commit's own arguments. It walks the
// option grammar instead of searching the text, so the same spelling inside a -m
// message or a pathspec is not read as a flag.
func commitHasFlag(args []shellsyntax.Word, flag string) bool {
	for i := 0; i < len(args); i++ {
		if !args[i].Static {
			continue
		}
		token := args[i].Value
		if token == "--" {
			return false
		}
		if token == flag {
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
		base = resolveDir(path.Value, path.TildePrefix(), base)
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

func cdPath(args []shellsyntax.Word) (shellsyntax.Word, bool) {
	for _, arg := range args {
		if !arg.Static {
			return shellsyntax.Word{}, false
		}
		if arg.Value == "--" || strings.HasPrefix(arg.Value, "-") {
			continue
		}
		return arg, true
	}
	return shellsyntax.Word{}, false
}

// knownDir reports a directory that does not depend on the tool's cwd.
func knownDir(value string, tilde bool) bool {
	return filepath.IsAbs(expandToken(value, tilde)) || (tilde && strings.HasPrefix(value, "~/"))
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
		known = known || knownDir(path.Value, path.TildePrefix())
	}
	for i := p.gitArg + 1; i < p.subcommand; i++ {
		if !p.call.Args[i].Static {
			return false
		}
		token := p.call.Args[i].Value
		name, value, attached := strings.Cut(token, "=")
		tilde := false
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
			value, tilde = p.call.Args[i].Value, p.call.Args[i].TildePrefix()
		}
		known = known || knownDir(value, tilde)
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
			if i >= len(args) {
				return "", false
			}
			value, fixed := shellsyntax.FixedValue(args[i])
			if !fixed {
				return "", false
			}
			messages = append(messages, value)
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
				value, fixed := shellsyntax.FixedValue(word)
				if !fixed {
					return "", false
				}
				messages = append(messages, value)
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
	return outputOp(redir.Op)
}

func outputOp(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.ClbOut, syntax.RdrInOut:
		return true
	}
	return false
}

// writesTarget reports a redirect that opens its word as a file for output. A `>&` to
// a word other than a descriptor number or `-` sends stdout and stderr to that file.
func writesTarget(redir *syntax.Redirect) bool {
	if outputOp(redir.Op) {
		return true
	}
	if redir.Op != syntax.DplOut || redir.Word == nil {
		return false
	}
	word, ok := shellsyntax.StaticWord(redir.Word)
	fd := strings.TrimSuffix(word, "-")
	return !ok || (fd != "" && strings.Trim(fd, "0123456789") != "")
}

// Steps that may run before or beside the commit in the same command. None of them
// writes a file except through a redirect, which stepBeforeCommit and memberStep judge
// on their own.
var stepsBeforeCommit = map[string]bool{
	"cd": true, "pwd": true, "echo": true, "printf": true, "cat": true, "head": true,
	"tail": true, "grep": true, "wc": true, "true": true, ":": true, "set": true,
	"unset": true,
}

var gitStepsBeforeCommit = map[string]bool{
	"add": true, "stage": true, "status": true, "log": true, "diff": true, "show": true,
	"rev-parse": true, "push": true, "fetch": true,
}

// Directories an output redirect before the commit may write to, outside the
// commit's repository: a message file there changes no tree git reads.
var scratchRoots = []string{"/tmp", "/private/tmp", "/var/folders", os.TempDir()}

// stepBeforeCommit returns the source of the first step that runs before git
// commits and may change the tree: a call outside the allowlist, or an output
// redirect outside a scratch directory. Empty means no such step.
func (p *parsedGit) stepBeforeCommit() string {
	file, err := syntax.NewParser().Parse(strings.NewReader(p.program.Source), "command")
	if err != nil {
		return ""
	}
	commitStmt := p.commitStatement(file)
	runsFirst := p.firstRegion(commitStmt)
	var step string
	syntax.Walk(file, func(node syntax.Node) bool {
		stmt, ok := node.(*syntax.Stmt)
		if !ok || step != "" || stmt == commitStmt || !runsFirst(int(stmt.Pos().Offset())) {
			return step == ""
		}
		for _, redir := range stmt.Redirs {
			if writesTarget(redir) && !p.scratchTarget(redir.Word) {
				step = p.program.Source[stmt.Pos().Offset():stmt.End().Offset()]
				return false
			}
		}
		return true
	})
	if step != "" {
		return step
	}
	for i, call := range p.program.Calls {
		if i == p.callIndex || !runsFirst(call.Start) || p.allowedBeforeCommit(call) {
			continue
		}
		return p.program.Source[call.Start:call.End]
	}
	return ""
}

// commitRedirect returns the source of the first redirect on the commit's own
// statement that writes outside a scratch directory. The shell opens that file before
// git reads the tree, so `git commit -a > f` lands f empty. Empty means none.
func (p *parsedGit) commitRedirect() string {
	file, err := syntax.NewParser().Parse(strings.NewReader(p.program.Source), "command")
	if err != nil {
		return ""
	}
	stmt := p.commitStatement(file)
	if stmt == nil {
		return ""
	}
	for _, redir := range stmt.Redirs {
		if writesTarget(redir) && !p.scratchTarget(redir.Word) {
			return p.program.Source[redir.Pos().Offset():redir.End().Offset()]
		}
	}
	return ""
}

// firstRegion reports whether a source offset runs before git commits: everything up
// to the end of the commit's statement, plus its heredoc bodies, because a
// substitution there runs before git does.
func (p *parsedGit) firstRegion(commitStmt *syntax.Stmt) func(int) bool {
	end := p.call.End
	var heredocs [][2]int
	if commitStmt != nil {
		// A Stmt's own end runs past its heredoc bodies, which the region holds
		// separately, so the statement ends at its last redirect word.
		for _, redir := range commitStmt.Redirs {
			if redir.Word != nil {
				end = max(end, int(redir.Word.End().Offset()))
			}
			if redir.Hdoc != nil {
				heredocs = append(heredocs, [2]int{int(redir.Hdoc.Pos().Offset()), int(redir.Hdoc.End().Offset())})
			}
		}
	}
	return func(start int) bool {
		if start < end {
			return true
		}
		for _, body := range heredocs {
			if start >= body[0] && start < body[1] {
				return true
			}
		}
		return false
	}
}

// concurrentStep returns the source of the first step that may run while git commits
// and change the tree it reads: an asynchronous statement that starts before the
// commit ends or holds it, or a step in a member of a pipeline that holds the commit.
// Empty means no such step.
func (p *parsedGit) concurrentStep() string {
	file, err := syntax.NewParser().Parse(strings.NewReader(p.program.Source), "command")
	if err != nil {
		return ""
	}
	commitStmt := p.commitStatement(file)
	if commitStmt == nil {
		return ""
	}
	runsFirst := p.firstRegion(commitStmt)
	var step string
	syntax.Walk(file, func(node syntax.Node) bool {
		if step != "" {
			return false
		}
		switch node := node.(type) {
		case *syntax.Stmt:
			if asynchronous(node) && runsFirst(int(node.Pos().Offset())) {
				step = p.source(node)
			}
		case *syntax.BinaryCmd:
			if node.Op != syntax.Pipe && node.Op != syntax.PipeAll {
				break
			}
			if holds(node.X, commitStmt) {
				step = p.memberStep(node.Y)
			} else if holds(node.Y, commitStmt) {
				step = p.memberStep(node.X)
			}
		}
		return step == ""
	})
	return step
}

// memberStep returns the source of the first step in a pipeline member beside the
// commit that may change the tree while git reads it: a call that is a git call or
// outside the read-only steps, or a redirect outside a scratch directory.
func (p *parsedGit) memberStep(member *syntax.Stmt) string {
	var step string
	syntax.Walk(member, func(node syntax.Node) bool {
		if step != "" {
			return false
		}
		switch node := node.(type) {
		case *syntax.Stmt:
			for _, redir := range node.Redirs {
				if writesTarget(redir) && !p.scratchTarget(redir.Word) {
					step = p.source(node)
				}
			}
		case *syntax.CallExpr:
			if len(node.Args) > 0 && !p.filterCall(int(node.Pos().Offset())) {
				step = p.source(node)
			}
		}
		return step == ""
	})
	return step
}

// filterCall reports whether the call at a source offset is a read-only step and
// not a git call, so it may run beside the commit.
func (p *parsedGit) filterCall(start int) bool {
	for _, call := range p.program.Calls {
		if call.Start == start {
			_, isGit := gitExecutable(call.Args)
			return !isGit && readOnlyStep(call)
		}
	}
	return false
}

// asynchronous reports a statement the shell does not wait for: one sent to the
// background, a coprocess, or a disowned job.
func asynchronous(stmt *syntax.Stmt) bool {
	_, coproc := stmt.Cmd.(*syntax.CoprocClause)
	return stmt.Background || stmt.Coprocess || stmt.Disown || coproc
}

// holds reports whether target is node or lies inside it.
func holds(node syntax.Node, target *syntax.Stmt) bool {
	found := false
	syntax.Walk(node, func(n syntax.Node) bool {
		found = found || n == target
		return !found
	})
	return found
}

func (p *parsedGit) source(node syntax.Node) string {
	return p.program.Source[node.Pos().Offset():node.End().Offset()]
}

// commitStatement returns the statement whose call is the commit, or nil.
func (p *parsedGit) commitStatement(file *syntax.File) *syntax.Stmt {
	var found *syntax.Stmt
	syntax.Walk(file, func(node syntax.Node) bool {
		if stmt, ok := node.(*syntax.Stmt); ok {
			if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && int(call.Pos().Offset()) == p.call.Start {
				found = stmt
			}
		}
		return found == nil
	})
	return found
}

func (p *parsedGit) allowedBeforeCommit(call shellsyntax.Call) bool {
	if gitArg, ok := gitExecutable(call.Args); ok {
		sub, _, _, _, ok := gitSubcommand(call.Args, gitArg, p.baseCWD)
		if !ok || !gitStepsBeforeCommit[call.Args[sub].Value] {
			return false
		}
		for _, arg := range call.Args[sub+1:] {
			if !arg.Static || strings.HasPrefix(arg.Value, "--output") {
				return false
			}
		}
		return true
	}
	return readOnlyStep(call)
}

func readOnlyStep(call shellsyntax.Call) bool {
	return call.Args[0].Static && stepsBeforeCommit[call.Args[0].Value]
}

// gitEnvAllowed names the GIT_* variables a commit command may set. Each changes who or
// when, never which repository, index or tree git commits from.
var gitEnvAllowed = map[string]bool{
	"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
	"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true, "GIT_COMMITTER_DATE": true,
	"GIT_EDITOR": true, "GIT_PAGER": true, "GIT_TERMINAL_PROMPT": true,
}

// gitEnvironment returns each GIT_* variable outside gitEnvAllowed that the command sets
// before or on its commit: a prefix, a bare or exported assignment, or an env argument
// to a git call. The gate's own git calls inherit the hook's environment, not these.
func (p *parsedGit) gitEnvironment() []string {
	file, err := syntax.NewParser().Parse(strings.NewReader(p.program.Source), "command")
	if err != nil {
		return nil
	}
	var names []string
	add := func(name string) {
		if strings.HasPrefix(name, "GIT_") && !gitEnvAllowed[name] && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	// Program.Calls holds neither a bare assignment nor export/declare, so the tree is
	// walked for every assignment instead.
	syntax.Walk(file, func(node syntax.Node) bool {
		if assign, ok := node.(*syntax.Assign); ok && assign.Name != nil &&
			int(assign.Pos().Offset()) < p.call.End {
			add(assign.Name.Value)
		}
		return true
	})
	for _, call := range p.program.Calls {
		if call.Start >= p.call.End {
			continue
		}
		gitArg, ok := gitExecutable(call.Args)
		if !ok {
			continue
		}
		for _, arg := range call.Args[:gitArg] {
			if name, _, ok := strings.Cut(arg.Value, "="); ok && !strings.HasPrefix(arg.Value, "-") {
				add(name)
			}
		}
	}
	return names
}

// scratchTarget reports whether a redirect writes /dev/null, or a static absolute
// path that lies, with its links resolved, under a scratch root and outside the top
// level of the commit's repository. A link that does not resolve lies nowhere, so it
// is never scratch: a write through it creates its target.
func (p *parsedGit) scratchTarget(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	target, ok := shellsyntax.StaticWord(word)
	if !ok {
		return false
	}
	if target == "/dev/null" {
		return true
	}
	if !filepath.IsAbs(target) {
		return false
	}
	dir, ok := p.repoDir()
	if !ok {
		return false
	}
	repo, err := steerpath.ProjectRootOf(dir)
	if err != nil || steerpath.Within(repo, target) {
		return false
	}
	for _, root := range scratchRoots {
		if steerpath.Within(root, target) {
			return true
		}
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

// amends reports `git commit --amend`. The commit that lands is then HEAD^..new, not
// HEAD..new, so the tree the reviewers must read is a different one.
func (p *parsedGit) amends() bool {
	return commitHasFlag(p.call.Args[p.subcommand+1:], "--amend")
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
	adds := parsedAddPathspecs(program, commit, false)
	var paths []string
	for _, specs := range adds {
		paths = append(paths, specs...)
	}
	return paths
}

// parsedAddPathspecs preserves the selection of forced adds independently of ordinary adds.
func parsedAddPathspecs(program *shellsyntax.Program, commit *parsedGit, forcedOnly bool) [][]string {
	wide := func() [][]string {
		if forcedOnly {
			return [][]string{{":/"}}
		}
		return nil
	}
	commitCWD, commitTree, ok := commit.resolvedDirs()
	if !ok {
		return wide()
	}
	var out [][]string
	for _, add := range parsedGitCalls(program, commit.baseCWD, "add", "stage") {
		if add.call.Start >= commit.call.Start || forcedOnly && !add.forcesAdd() {
			continue
		}
		if add.gitDirOnly {
			return wide()
		}
		addCWD, addTree, ok := add.resolvedDirs()
		if !ok || addCWD != commitCWD || addTree != commitTree {
			return wide()
		}
		literal := false
		var specs []string
		for _, arg := range add.call.Args[add.subcommand+1:] {
			if !arg.Static {
				return wide()
			}
			if !literal && arg.Value == "--" {
				literal = true
				continue
			}
			if !literal && strings.HasPrefix(arg.Value, "-") {
				if strings.HasPrefix(arg.Value, "--pathspec-from-file") {
					return wide()
				}
				continue
			}
			specs = append(specs, arg.Value)
		}
		if len(specs) == 0 {
			return wide()
		}
		out = append(out, specs)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// forcesAdd reads the last force option before a literal path delimiter.
func (p *parsedGit) forcesAdd() bool {
	force := false
	for _, arg := range p.call.Args[p.subcommand+1:] {
		if !arg.Static {
			continue
		}
		if arg.Value == "--" {
			break
		}
		switch arg.Value {
		case "--force":
			force = true
		case "--no-force":
			force = false
		default:
			if strings.HasPrefix(arg.Value, "-") && !strings.HasPrefix(arg.Value, "--") && strings.Contains(arg.Value[1:], "f") {
				force = true
			}
		}
	}
	return force
}

// shellAmbiguity names why the gate cannot establish a parsed commit's repository or
// message. A command with no parsed commit is DetectionDeny's to judge.
func shellAmbiguity(cmd, cwd string) string {
	p, err := parseCommit(cmd, cwd)
	if err != nil || p == nil {
		return ""
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

// heredocCommitExample is a commit form the gate reviews that keeps a message body.
const heredocCommitExample = "git -C /absolute/path/to/repo commit -F - <<'MSG'\n" +
	"subject\n\nbody\nMSG"

const ambiguousShellReason = "[commit-gate] BLOCKED: %s, so the gate cannot establish " +
	"the repository and commit message safely. Re-run the commit as a plain command " +
	"with an absolute repository and a literal message. This form keeps a multi-line " +
	"message whole:\n\n" + heredocCommitExample

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
