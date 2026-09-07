package status

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// These tests refuse a machine word — intervention, class, chain, verdict,
// corpus, ledger — in anything a person reads. The stored records, the JSON
// contract and the Go symbols keep those words; the display surface does not.
//
// The check reads string literals, so it sees a word an author TYPED and never
// one that arrives as data. UnitName translates the stored unit for that reason.

// displayFiles are the files whose string literals reach a person. Each is
// display-only, or renders through fmt and the styles package, so a literal
// carrying a sentence in one of them is something a reader sees. The dashboard's
// panels are read off the directory, never listed, so a new panel is covered
// without anybody remembering to add it.
func displayFiles(t *testing.T) []string {
	t.Helper()
	files := []string{"action.go", "format.go", "evidence.go", "problem.go", "readiness.go", "../../cmd/status.go"}
	panels, err := filepath.Glob("../tui/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range panels {
		if !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
	}
	return files
}

// machineWords are the words with a display replacement. Each names a thing in
// the measurement system that a reader has no word for: say review sequence, not
// chain; recurring problem, not class; result, not verdict; fix, not
// intervention; decision, not adjudication; built-in guard, not preset.
var machineWords = regexp.MustCompile(`(?i)\b(class|classes|intervention|interventions|adjudicate|adjudicated|adjudication|adjudications|corpus|corpora|chain|chains|verdict|verdicts|confound|confounds|matcher|matchers|incidence|denominator|ledger|ledgers|firing|firings|candidate|candidates|instrument|instruments|preset|presets|master|masters|resolver|resolvers|posture|postures)\b`)

// allowedPhrases are the literals that legitimately carry a machine word. Keep
// this list short and justified: every entry is a place a reader meets the
// machine's vocabulary, and each one needs a reason.
var allowedPhrases = map[string]bool{
	// Executable text. A reader who types one of these needs it spelled the way
	// the CLI accepts, so no display rename may move it.
	"%s is not a built-in guard; remove it with: steer hooks remove %s": true,
}

// isPhrase says a literal is something a reader is shown: one carrying
// whitespace, and one starting with a capital. Every machine value in these files
// is lowercase by construction — a map key, a flag name, a JSON token — so
// "Classes" is display text and "adjudicate" is not. The whitespace is NOT
// trimmed first: `"ledger "` is a label built for concatenation.
func isPhrase(s string) bool {
	if strings.ContainsAny(s, " \t\n") {
		return true
	}
	r := []rune(s)
	return len(r) > 0 && unicode.IsUpper(r[0])
}

// sourceLiterals returns the string literals a reader could meet in one display
// file. Struct tags name the wire contract, so they stay out of this surface.
func sourceLiterals(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	tags := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if f, ok := n.(*ast.Field); ok && f.Tag != nil {
			tags[f.Tag.Pos()] = true
		}
		return true
	})
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || tags[lit.Pos()] {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err == nil {
			out = append(out, text)
		}
		return true
	})
	return out
}

func TestNoMachineWordReachesADisplayString(t *testing.T) {
	for _, path := range displayFiles(t) {
		for _, text := range sourceLiterals(t, path) {
			if !isPhrase(text) || allowedPhrases[text] {
				continue
			}
			if w := machineWords.FindString(text); w != "" {
				t.Errorf("%s: a reader is shown the machine word %q: %q",
					path, w, text)
			}
		}
	}
}

// These phrases belonged to the dashboard vocabulary before its current five
// panel contract. They are checked separately because they are ordinary English,
// so a machine-word lint cannot identify them as stale.
func TestStatusDoesNotUseRetiredDashboardVocabulary(t *testing.T) {
	retired := []string{
		"Posture",
		"Attention Required",
		"  Denials      ",
		"  Resolver     ",
		"Guard master ",
		"not armed",
		"registered but inert",
		"read it back from the coding agent's transcript",
		"action(s) blocked",
		"no fixes recorded on this machine",
		"review sequence(s)",
		"hits, %d sessions",
	}
	for _, path := range []string{"../../cmd/status.go", "readiness.go"} {
		for _, text := range sourceLiterals(t, path) {
			for _, word := range retired {
				if strings.Contains(text, word) {
					t.Errorf("%s: status still uses retired dashboard wording %q in %q", path, word, text)
				}
			}
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
	// A one-word navigation label carries no whitespace and is still display text.
	for _, s := range []string{"Classes", "Problems", "Diagnostics"} {
		if !isPhrase(s) {
			t.Errorf("the check skips the navigation label %q", s)
		}
	}
	// Lowercase sentence fragments are display text when they carry whitespace;
	// this pins the readiness wording that starts with "the" or "no".
	for _, s := range []string{"the matcher runs for no commit", "no commit gate is registered"} {
		if !isPhrase(s) {
			t.Errorf("the check skips the lowercase display phrase %q", s)
		}
	}
}
