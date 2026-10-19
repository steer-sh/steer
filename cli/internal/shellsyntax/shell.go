// Package shellsyntax is the shared structural boundary for shell command text.
package shellsyntax

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Instrument identifies the parser and the normalized representation consumers read.
const Instrument = "mvdan-sh-v3.13.1-calls-v1"

// Word is one shell word. Static is false when evaluating it requires expansion.
// Start and End are the word's byte range in the source; a word built by a caller
// rather than the parser leaves both zero.
type Word struct {
	Value  string
	Raw    string
	Static bool
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
					r.Heredoc, r.HeredocStatic = staticWord(redir.Hdoc)
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
	return Word{Value: value, Raw: raw, Static: static, Start: start, End: end}
}

// StaticWord returns a word's value when it holds only literals and quotes.
func StaticWord(word *syntax.Word) (string, bool) { return staticWord(word) }

func staticWord(word *syntax.Word) (string, bool) { return wordValue(word, false) }

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
		if !staticPart(&b, part, catHeredocs) {
			return "", false
		}
	}
	return b.String(), true
}

func staticPart(b *strings.Builder, part syntax.WordPart, catHeredocs bool) bool {
	switch p := part.(type) {
	case *syntax.Lit:
		b.WriteString(p.Value)
	case *syntax.SglQuoted:
		b.WriteString(p.Value)
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
			if !staticPart(b, nested, catHeredocs) {
				return false
			}
		}
	default:
		return false
	}
	return true
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
	body, ok := staticWord(redir.Hdoc)
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
