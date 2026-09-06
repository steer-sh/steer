package status

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These tests refuse a machine word — intervention, class, chain, verdict,
// corpus, ledger — in anything a person reads. The stored records, the JSON
// contract and the Go symbols keep those words; the display surface does not.
//
// The check reads string literals, so it sees a word an author TYPED and never
// one that arrives as data. UnitName translates the stored unit for that reason.

// displayFiles are the files whose string literals reach a person. Each is
// display-only, or renders through fmt and the styles package, so a literal
// carrying a sentence in one of them is something a reader sees.
var displayFiles = []string{
	"action.go",
	"format.go",
	"evidence.go",
	"../../cmd/status.go",
	// Every panel of the dashboard, not just the one that renders results: the
	// channels and health panels print sentences too, and a leak there reaches
	// the same reader.
	"../tui/candidates.go",
	"../tui/channels.go",
	"../tui/classes.go",
	"../tui/dashboard.go",
	"../tui/health.go",
	"../tui/presets.go",
	"../tui/render.go",
	"../tui/reviewer.go",
}

// machineWords are the words with a display replacement. Each names a thing in
// the measurement system that a reader has no word for: say review sequence, not
// chain; recurring problem, not class; result, not verdict; fix, not
// intervention; decision, not adjudication.
var machineWords = regexp.MustCompile(`(?i)\b(class|classes|intervention|interventions|adjudicate|adjudicated|adjudication|adjudications|corpus|corpora|chain|chains|verdict|verdicts|confound|confounds|matcher|matchers|incidence|denominator|ledger|ledgers|firing|firings)\b`)

// allowedPhrases are the literals that legitimately carry a machine word. Keep
// this list short and justified: every entry is a place a reader meets the
// machine's vocabulary, and each one needs a reason.
var allowedPhrases = map[string]bool{}

// A literal carrying whitespace is something a reader is shown: a sentence, or a
// fragment padded for concatenation. A literal with none is a token, a flag name
// or a map key — a machine value by construction. The whitespace is NOT trimmed
// first: `"ledger "` is a label built for concatenation, and trimming made it
// read as a bare token and skipped the one leak this check exists for.
func isPhrase(s string) bool { return strings.ContainsAny(s, " \t\n") }

func TestNoMachineWordReachesADisplayString(t *testing.T) {
	for _, path := range displayFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		// A struct tag is a string literal too, and a multi-key tag carries a
		// space. Tags name the wire, which is where the machine's words belong.
		tags := map[token.Pos]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if f, ok := n.(*ast.Field); ok && f.Tag != nil {
				tags[f.Tag.Pos()] = true
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil || tags[lit.Pos()] || !isPhrase(text) || allowedPhrases[text] {
				return true
			}
			if w := machineWords.FindString(text); w != "" {
				t.Errorf("%s: a reader is shown the machine word %q: %q",
					fset.Position(lit.Pos()), w, text)
			}
			return true
		})
	}
}

// styleBlock and templateAction are the parts of the page a reader never sees.
var (
	styleBlock     = regexp.MustCompile(`(?s)<style>.*?</style>`)
	templateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	textNode       = regexp.MustCompile(`>([^<>]+)<`)
)

// The HTML page is checked on its text nodes only: an attribute value and a CSS
// class name are markup, and the verdict token legitimately survives in the
// `v-<token>` pill class that colors the badge.
func TestNoMachineWordReachesThePage(t *testing.T) {
	raw, err := os.ReadFile("templates/dashboard.html.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	page := templateAction.ReplaceAllString(styleBlock.ReplaceAllString(string(raw), ""), "")
	for _, m := range textNode.FindAllStringSubmatch(page, -1) {
		text := strings.TrimSpace(m[1])
		if text == "" || allowedPhrases[text] {
			continue
		}
		if w := machineWords.FindString(text); w != "" {
			t.Errorf("the page shows the machine word %q: %q", w, text)
		}
	}
}

// The check's own discriminator, pinned. A fragment padded for concatenation is
// display text and a bare token is not, and the difference is one call to
// TrimSpace that once hid a real leak.
func TestTheCheckSeesAPaddedFragment(t *testing.T) {
	for _, s := range []string{"ledger ", " chain", "review chains %d\n", "a clean window"} {
		if !isPhrase(s) {
			t.Errorf("the check skips the display fragment %q", s)
		}
	}
	for _, s := range []string{"adjudicate", "chain", "evidence-incomplete", "scope-harness"} {
		if isPhrase(s) {
			t.Errorf("the check reads the machine value %q as display text", s)
		}
	}
}
