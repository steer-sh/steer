// Package shellsyntax is the shared structural boundary for shell command text.
package shellsyntax

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Instrument identifies the parser and the normalized representation consumers read.
const Instrument = "mvdan-sh-v3.13.1-calls-v2"

// Word is one shell word. Static is false when evaluating it requires expansion;
// a static Value is the word after quote removal, as the command receives it.
// Splits is true when an expansion can make the word several words.
// Start and End are the word's byte range in the source; a word built by a caller
// rather than the parser leaves both zero.
type Word struct {
	Value  string
	Raw    string
	Static bool
	Splits bool
	Start  int
	End    int
}

// Redirect is one redirection attached to a simple command. HeredocStart and
// HeredocEnd are the heredoc body's byte range in the source.
type Redirect struct {
	Op            syntax.RedirOperator
	FD            string
	Target        Word
	Heredoc       string
	HeredocStatic bool
	HeredocStart  int
	HeredocEnd    int
}

// Call is one simple command, including calls nested in substitutions and subshells.
type Call struct {
	Args      []Word
	Redirects []Redirect
	Start     int
	End       int
	Scope     int
	Scopes    []int
}

// Program is a shell file normalized into source-ordered simple commands.
type Program struct {
	Source string
	Calls  []Call
}

type scopeNode struct {
	start, end int
	id         int
}

// Parse parses Bash syntax exactly. A caller chooses its own failure posture.
func Parse(source string) (*Program, error) {
	return parse(source, syntax.NewParser())
}

// ParsePartial recovers a bounded number of missing closing tokens. Transcript
// targets can contain only the invocation line of a heredoc while still naming the
// executable exactly; enforcement callers use Parse instead.
func ParsePartial(source string) (*Program, error) {
	p, err := parse(source, syntax.NewParser(syntax.RecoverErrors(4)))
	if err == nil {
		return p, nil
	}
	matches := heredocHead.FindAllStringSubmatch(source, -1)
	if len(matches) == 0 {
		return nil, err
	}
	last := matches[len(matches)-1]
	tag := last[1] + last[2] + last[3]
	return parse(source+"\n"+tag+"\n", syntax.NewParser(syntax.RecoverErrors(4)))
}

var heredocHead = regexp.MustCompile(`<<-?[ \t]*(?:'([^']+)'|"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))`)

func parse(source string, parser *syntax.Parser) (*Program, error) {
	file, err := parser.Parse(strings.NewReader(source), "command")
	if err != nil {
		return nil, err
	}

	var scopes []scopeNode
	var calls []*syntax.CallExpr
	var stmts []*syntax.Stmt
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Subshell:
			scopes = append(scopes, scopeNode{offset(n.Pos()), offset(n.End()), len(scopes) + 1})
		case *syntax.CmdSubst:
			scopes = append(scopes, scopeNode{offset(n.Pos()), offset(n.End()), len(scopes) + 1})
		case *syntax.CallExpr:
			calls = append(calls, n)
		case *syntax.Stmt:
			stmts = append(stmts, n)
		}
		return true
	})

	p := &Program{Source: source}
	for _, expr := range calls {
		if len(expr.Args) == 0 {
			continue
		}
		call := Call{Start: offset(expr.Pos()), End: offset(expr.End())}
		for _, word := range expr.Args {
			call.Args = append(call.Args, normalizeWord(source, word))
		}
		var enclosing []scopeNode
		for _, scope := range scopes {
			if scope.start < call.Start && call.End <= scope.end {
				enclosing = append(enclosing, scope)
			}
		}
		sort.SliceStable(enclosing, func(i, j int) bool {
			return enclosing[i].end-enclosing[i].start > enclosing[j].end-enclosing[j].start
		})
		for _, scope := range enclosing {
			call.Scopes = append(call.Scopes, scope.id)
		}
		if len(call.Scopes) > 0 {
			call.Scope = call.Scopes[len(call.Scopes)-1]
		}
		for _, stmt := range stmts {
			if stmt.Cmd != expr {
				continue
			}
			for _, redir := range stmt.Redirs {
				r := Redirect{Op: redir.Op}
				if redir.N != nil {
					r.FD = redir.N.Value
				}
				if redir.Word != nil {
					r.Target = normalizeWord(source, redir.Word)
				}
				if redir.Hdoc != nil {
					r.Heredoc, r.HeredocStatic = heredocBody(redir.Hdoc)
					r.HeredocStart, r.HeredocEnd = offset(redir.Hdoc.Pos()), offset(redir.Hdoc.End())
				}
				call.Redirects = append(call.Redirects, r)
			}
			break
		}
		p.Calls = append(p.Calls, call)
	}
	sort.SliceStable(p.Calls, func(i, j int) bool { return p.Calls[i].Start < p.Calls[j].Start })
	return p, nil
}

func offset(pos syntax.Pos) int { return int(pos.Offset()) }

func normalizeWord(source string, word *syntax.Word) Word {
	value, static := staticWord(word)
	start, end := offset(word.Pos()), offset(word.End())
	raw := ""
	if start >= 0 && end >= start && end <= len(source) {
		raw = source[start:end]
	}
	return Word{Value: value, Raw: raw, Static: static, Splits: splits(word.Parts), Start: start, End: end}
}

// splits reports an unquoted parameter, command or arithmetic expansion, whose
// result the shell splits into fields, or a double-quoted `@` expansion such as
// "$@", "${a[@]}" or "${!prefix@}", which gives one word per element.
func splits(parts []syntax.WordPart) bool {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp:
			return true
		case *syntax.DblQuoted:
			found := false
			syntax.Walk(p, func(node syntax.Node) bool {
				if exp, ok := node.(*syntax.ParamExp); ok && elementWords(exp) {
					found = true
				}
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
}

func elementWords(exp *syntax.ParamExp) bool {
	if exp.Length {
		return false
	}
	index, _ := exp.Index.(*syntax.Word)
	return exp.Param.Value == "@" || (index != nil && index.Lit() == "@") ||
		exp.Names == syntax.NamesPrefixWords
}

// StaticWord returns a word's value when it holds only literals and quotes.
func StaticWord(word *syntax.Word) (string, bool) { return staticWord(word) }

func staticWord(word *syntax.Word) (string, bool) { return wordValue(word, false) }

// heredocBody returns a heredoc body that needs no expansion, with its backslashes
// kept as written.
func heredocBody(word *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range word.Parts {
		if !staticPart(&b, part, inHeredoc, false) {
			return "", false
		}
	}
	return b.String(), true
}

// FixedValue returns a word's value when running no command but cat is needed to know
// it: a static word, or one whose double-quoted parts include `$(cat <<'TAG' ... TAG)`,
// whose output is the heredoc body. Static stays false for the second kind, so a
// reader of Static alone sees the normalized representation Instrument names.
func FixedValue(w Word) (string, bool) {
	if w.Static {
		return w.Value, true
	}
	file, err := syntax.NewParser().Parse(strings.NewReader("x "+w.Raw), "")
	if err != nil || len(file.Stmts) != 1 {
		return "", false
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) != 2 {
		return "", false
	}
	return wordValue(call.Args[1], true)
}

func wordValue(word *syntax.Word, catHeredocs bool) (string, bool) {
	var b strings.Builder
	for _, part := range word.Parts {
		if !staticPart(&b, part, bare, catHeredocs) {
			return "", false
		}
	}
	return b.String(), true
}

// quoting is where a literal sits, which decides the backslashes quote removal drops.
type quoting int

const (
	bare quoting = iota
	inDouble
	inHeredoc
)

func staticPart(b *strings.Builder, part syntax.WordPart, q quoting, catHeredocs bool) bool {
	switch p := part.(type) {
	case *syntax.Lit:
		writeLit(b, p.Value, q)
	case *syntax.SglQuoted:
		if !p.Dollar {
			b.WriteString(p.Value)
			break
		}
		if !plainEscapes(p.Value) {
			return false
		}
		value, err := expand.Literal(nil, &syntax.Word{Parts: []syntax.WordPart{p}})
		if err != nil {
			return false
		}
		b.WriteString(value)
	case *syntax.DblQuoted:
		for _, nested := range p.Parts {
			if subst, ok := nested.(*syntax.CmdSubst); ok && catHeredocs {
				body, ok := catHeredoc(subst)
				if !ok {
					return false
				}
				b.WriteString(body)
				continue
			}
			if !staticPart(b, nested, inDouble, catHeredocs) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

// plainEscapes reports that every escape in a $'...' body is one that
// expand.Literal decodes as bash does. Octal, hex, Unicode and control escapes
// are left out: expand.Literal and bash disagree on some of them.
func plainEscapes(body string) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		i++
		if i >= len(body) || strings.IndexByte(`abeEfnrtv\'"?`, body[i]) < 0 {
			return false
		}
	}
	return true
}

// writeLit writes a literal after quote removal. Unquoted, a backslash quotes the
// next character; in double quotes, only `$`, a backtick, `"` and `\`. The parser
// has already removed line continuations. A heredoc body keeps its backslashes.
func writeLit(b *strings.Builder, value string, q quoting) {
	if q == inHeredoc {
		b.WriteString(value)
		return
	}
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) &&
			(q == bare || strings.IndexByte("$`\"\\", value[i+1]) >= 0) {
			i++
		}
		b.WriteByte(value[i])
	}
}

// catHeredoc returns the value of a double-quoted `$(cat <<'TAG' ... TAG)`: the
// heredoc body less its trailing newlines, as command substitution strips them. The
// tag must be quoted, so the body does not expand. Unquoted, the substitution's
// output would be split into words, so only staticPart's DblQuoted case calls this.
func catHeredoc(subst *syntax.CmdSubst) (string, bool) {
	if len(subst.Stmts) != 1 {
		return "", false
	}
	stmt := subst.Stmts[0]
	if stmt.Negated || stmt.Background || stmt.Coprocess || len(stmt.Redirs) != 1 {
		return "", false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) != 1 {
		return "", false
	}
	if name, ok := staticWord(call.Args[0]); !ok || name != "cat" {
		return "", false
	}
	redir := stmt.Redirs[0]
	if redir.Op != syntax.Hdoc || redir.N != nil || redir.Hdoc == nil || !quotedTag(redir.Word) {
		return "", false
	}
	body, ok := heredocBody(redir.Hdoc)
	if !ok {
		return "", false
	}
	return strings.TrimRight(body, "\n"), true
}

// quotedTag reports a heredoc tag written with quotes, which stops the body expanding.
func quotedTag(tag *syntax.Word) bool {
	if tag == nil {
		return false
	}
	for _, part := range tag.Parts {
		switch part.(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		}
	}
	return false
}

// TildePrefix reports a static word written as `~` or `~/...`, whose `~` the shell
// expands to the home directory. Any quoting before the first `/` stops it; a
// line continuation does not.
func (w Word) TildePrefix() bool {
	raw := strings.ReplaceAll(w.Raw, "\\\n", "")
	return w.Static && (raw == "~" || strings.HasPrefix(raw, "~/"))
}

// Base returns the executable basename when a word is statically known.
func (w Word) Base() string {
	if !w.Static {
		return ""
	}
	return filepath.Base(w.Value)
}

// Key is a comparison form for paths. It preserves dynamic expressions while
// removing a single pair of shell quotes around the complete word.
func (w Word) Key() string {
	if w.Static {
		return w.Value
	}
	raw := w.Raw
	if len(raw) >= 2 && ((raw[0] == '\'' && raw[len(raw)-1] == '\'') ||
		(raw[0] == '"' && raw[len(raw)-1] == '"')) {
		return raw[1 : len(raw)-1]
	}
	return raw
}
