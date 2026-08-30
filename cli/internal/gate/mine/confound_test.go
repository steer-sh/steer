package mine

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func model(r gate.Row, m string) gate.Row { r["author_model"] = m; return r }

// chain builds a one-round chain from a deny row carrying metadata plus an adjudicated
// retry row, which is the shape every measurement reads.
func chain(t *testing.T, ts int, deny gate.Row) Chain {
	t.Helper()
	built := BuildChains([]gate.Row{deny, concerns(row(ts+1, "retry"), "round", c("x"))}, false)
	if len(built) != 1 {
		t.Fatalf("expected one chain, got %d", len(built))
	}
	return built[0]
}

func TestPathKind(t *testing.T) {
	check(t, "extension is lowercased", PathKind("cli/Main.GO"), "go")
	check(t, "a dotted directory does not leak", PathKind("a.b/main.go"), "go")
	check(t, "no extension", PathKind("Makefile"), "(none)")
	check(t, "a dotfile is its own kind", PathKind(".gitignore"), "gitignore")
}

func TestChainCarriesAuthorModel(t *testing.T) {
	ch := chain(t, 1000, model(row(1000, "deny"), "claude-opus-5"))
	check(t, "the model is recorded", ch.AuthorModels, []string{"claude-opus-5"})
	check(t, "the model is known", ch.AuthorModelKnown, true)

	bare := chain(t, 2000, row(2000, "deny"))
	check(t, "an unstamped chain knows no model", bare.AuthorModelKnown, false)
	if bare.AuthorModels != nil {
		t.Errorf("an unstamped chain lists no model, got %#v", bare.AuthorModels)
	}
}

func TestConfoundsDisjointSupportsAreHard(t *testing.T) {
	before := []Chain{chain(t, 1000, paths(model(row(1000, "deny"), "claude-opus-4-8"), "hooks/mine.py"))}
	after := []Chain{chain(t, 2000, paths(model(row(2000, "deny"), "claude-opus-5"), "cli/mine.go"))}

	hard, soft := Confounds(before, after, "")
	got := map[string]bool{}
	for _, h := range hard {
		got[h.Category] = true
	}
	if !got["author_model"] {
		t.Errorf("a disjoint author-model support is a hard confound, got %#v", hard)
	}
	if !got["path_kind"] {
		t.Errorf("a py-to-go path-kind shift is a hard confound, got %#v", hard)
	}
	if len(soft) != 0 {
		t.Errorf("a disjoint support is never also a soft warning, got %#v", soft)
	}
}

func TestConfoundsOverlapIsSoft(t *testing.T) {
	before := []Chain{chain(t, 1000, paths(row(1000, "deny"), "cli/a.go"))}
	after := []Chain{
		chain(t, 2000, paths(row(2000, "deny"), "cli/b.go")),
		chain(t, 3000, paths(row(3000, "deny"), "README.md")),
	}
	hard, soft := Confounds(before, after, "")
	if len(hard) != 0 {
		t.Errorf("an overlapping support withholds nothing, got %#v", hard)
	}
	if len(soft) != 1 || soft[0].Category != "path_kind" {
		t.Fatalf("the widened path-kind mix is one soft warning, got %#v", soft)
	}
	check(t, "the soft warning carries both supports", soft[0].After, []string{"go", "md"})
}

func TestConfoundsUnknownSupportIsNotAShift(t *testing.T) {
	before := []Chain{chain(t, 1000, paths(row(1000, "deny"), "cli/a.go"))}
	after := []Chain{chain(t, 2000, row(2000, "deny"))} // no path stamp at all
	hard, soft := Confounds(before, after, "")
	if len(hard) != 0 || len(soft) != 0 {
		t.Errorf("an empty support says nothing about a shift, got hard=%#v soft=%#v", hard, soft)
	}
}

func TestConfoundsInstrumentDriftIsHard(t *testing.T) {
	after := []Chain{chain(t, 2000, instrument(row(2000, "deny"), "gemini/3/abc/go"))}
	hard, _ := Confounds(nil, after, "gemini/3/abc/py")
	if len(hard) != 1 || hard[0].Category != "instrument" {
		t.Fatalf("an after-window instrument differing from the baseline is hard, got %#v", hard)
	}
	same, _ := Confounds(nil, after, "gemini/3/abc/go")
	if len(same) != 0 {
		t.Errorf("a matching instrument confounds nothing, got %#v", same)
	}
	none, _ := Confounds(nil, after, "")
	if len(none) != 0 {
		t.Errorf("a record predating the instrument field confounds nothing, got %#v", none)
	}
}
