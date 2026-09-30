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
type Word struct {
	Value  string
	Raw    string
	Static bool
}

// Redirect is one redirection attached to a simple command.
type Redirect struct {
	Op            syntax.RedirOperator
	FD            string
	Target        Word
	Heredoc       string
	HeredocStatic bool
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
	return Word{Value: value, Raw: raw, Static: static}
}

// StaticWord returns a word's value when it holds only literals and quotes.
func StaticWord(word *syntax.Word) (string, bool) { return staticWord(word) }

func staticWord(word *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range word.Parts {
		if !staticPart(&b, part) {
			return "", false
		}
	}
	return b.String(), true
}

func staticPart(b *strings.Builder, part syntax.WordPart) bool {
	switch p := part.(type) {
	case *syntax.Lit:
		b.WriteString(p.Value)
	case *syntax.SglQuoted:
		b.WriteString(p.Value)
	case *syntax.DblQuoted:
		for _, nested := range p.Parts {
			if !staticPart(b, nested) {
				return false
			}
		}
	default:
		return false
	}
	return true
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
