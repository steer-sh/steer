package mine

import (
	"reflect"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func model(r gate.Row, m string) gate.Row       { r["author_model"] = m; return r }
func withHarness(r gate.Row, h string) gate.Row { r["harness"] = h; return r }

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

func TestChainCarriesHarness(t *testing.T) {
	ch := chain(t, 1000, withHarness(row(1000, "deny"), "codex"))
	check(t, "the harness is recorded", ch.Harness, "codex")
	check(t, "the harness is known", ch.HarnessKnown, true)

	bare := chain(t, 2000, row(2000, "deny"))
	check(t, "an unstamped chain knows no harness", bare.HarnessKnown, false)
	check(t, "an unstamped chain names no harness", bare.Harness, "")
}

// A release row joins no chain: the chains, their rows and their end times read the
// same with and without one.
func TestReleaseRowsJoinNoChain(t *testing.T) {
	rows := func(released bool) []gate.Row {
		out := []gate.Row{
			withHarness(gate.Row{"ts": 1000, "phase": "deny", "firing": 1, "files": 1, "chain_id": "aaaa0001"}, "codex"),
			concerns(gate.Row{"ts": 1010, "phase": "retry", "firing": 1, "files": 1, "chain_id": "aaaa0001"}, "chain", c("x")),
		}
		if released {
			out = append(out, withHarness(gate.Row{"ts": 1011, "phase": gate.PhaseChainEnd, "chain_id": "aaaa0001",
				"blocks": 1, "session_id": "s1", "repo": "/elsewhere"}, "claude-code"))
		}
		return append(out, withHarness(gate.Row{"ts": 2000, "phase": "clean", "firing": 1, "files": 1, "chain_id": "bbbb0002"}, "codex"))
	}
	plain, withRelease := BuildChains(rows(false), false), BuildChains(rows(true), false)
	if !reflect.DeepEqual(plain, withRelease) {
		t.Errorf("a release row changed the chains:\n without %#v\n with    %#v", plain, withRelease)
	}
}
