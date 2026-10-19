package mine

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTermsDropWhatNamesNoClass(t *testing.T) {
	got := Terms("The parser DROPPED a flag; the parser saw 42 in tmp/x.go")
	// "the" is a stopword, "42" is digits alone, "a" and "x" are too short to
	// name anything, and a term repeated in one text is listed once.
	want := []string{"parser", "dropped", "flag", "saw", "tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestRankTermsCountAChainOnce(t *testing.T) {
	groups := []TermGroup{
		{Repo: "widget", Findings: []Finding{
			{Text: "the parser drops a flag"},
			{Text: "the parser drops another flag"},
		}},
		{Repo: "widget", Findings: []Finding{{Text: "the parser is fine"}}},
	}
	ranked := RankTerms(groups, groups, 2)
	for _, term := range ranked {
		if term.Term == "parser" {
			if term.Chains != 2 {
				t.Errorf("a term repeated inside one chain counts once; got %d", term.Chains)
			}
			return
		}
	}
	t.Errorf("parser is missing from %v", ranked)
}

func TestRankTermsReportTheCorpusCount(t *testing.T) {
	corpus := []TermGroup{
		{Findings: []Finding{{Text: "widget parser drops a flag"}}},
		{Findings: []Finding{{Text: "widget parser drops a flag"}}},
		{Findings: []Finding{{Text: "widget cache is stale"}}},
	}
	ranked := RankTerms(corpus[:2], corpus, 2)
	for _, term := range ranked {
		if term.Term != "widget" {
			continue
		}
		if term.Chains != 2 || term.CorpusChains != 3 {
			t.Errorf("want 2 of 3 chains for widget; got %d of %d", term.Chains, term.CorpusChains)
		}
		return
	}
	t.Errorf("widget is missing from %v", ranked)
}

func TestRankTermsDropASingleChainTerm(t *testing.T) {
	// minChains is the floor, so the term on both chains is listed and the term on
	// one is not. A term carried by a single chain names one commit attempt.
	groups := []TermGroup{
		{Findings: []Finding{{Text: "shared vocabulary"}}},
		{Findings: []Finding{{Text: "shared identifier"}}},
	}
	ranked := RankTerms(groups, groups, 2)
	if len(ranked) != 1 || ranked[0].Term != "shared" {
		t.Fatalf("only the term on both chains is listed; got %v", ranked)
	}
	if ranked[0].Chains != 2 {
		t.Errorf("it is carried by both chains; got %d", ranked[0].Chains)
	}
	if all := RankTerms(groups, groups, 1); len(all) != 3 {
		t.Errorf("a floor of one lists every term; got %v", all)
	}
}

func TestRankTermsBreakTiesLexically(t *testing.T) {
	groups := []TermGroup{
		{Findings: []Finding{{Text: "zebra alpha"}}},
		{Findings: []Finding{{Text: "zebra alpha"}}},
	}
	ranked := RankTerms(groups, groups, 2)
	if len(ranked) != 2 || ranked[0].Term != "alpha" || ranked[1].Term != "zebra" {
		t.Errorf("equal counts rank lexically; got %v", ranked)
	}
}

func TestMatchResidueKeepsAMissInsideAMatchingChain(t *testing.T) {
	// The chain matches, so the chain-level view would call it covered. The two
	// findings the regex misses inside it are exactly what widening it needs.
	dir := findingsDir(t, nil,
		[]map[string]any{
			c("the parser drops a flag"),
			c("the resolver loses a symbol"),
			c("the resolver loses a binding"),
		})
	var buf strings.Builder
	if err := Match(&buf, dir, MatchOpts{Regex: "parser", Kind: "accepted", ShowResidue: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "# residue: 2 of 3 accepted findings this regex misses, over 1 chain(s)") {
		t.Errorf("residue is per finding, not per chain; got:\n%s", out)
	}
}

func TestMatchResidueIsEmptyWhenTheRegexTakesEverything(t *testing.T) {
	dir := findingsDir(t, nil, []map[string]any{c("the parser drops a flag")})
	var buf strings.Builder
	if err := Match(&buf, dir, MatchOpts{Regex: ".", Kind: "accepted", ShowResidue: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "nothing is left over") {
		t.Errorf("a regex matching everything leaves no residue; got:\n%s", buf.String())
	}
}

func TestMatchResidueIsRefusedOnTheSessionCorpus(t *testing.T) {
	var buf strings.Builder
	err := Match(&buf, t.TempDir(), MatchOpts{Regex: "parser", ShowResidue: true,
		Corpus: CorpusTranscript})
	if err == nil {
		t.Fatal("a corpus with no chains cannot answer for a chain residue")
	}
	if !strings.Contains(err.Error(), "--show-residue") {
		t.Errorf("the refusal names the flag; got %v", err)
	}
}

func TestEllipsisCutsOnRunes(t *testing.T) {
	// An em-dash is three bytes. Cutting by byte lands inside it and writes a
	// broken character; cutting by rune keeps every character whole.
	got := ellipsis("a—b—c—d", 4)
	if got != "a—b—…" {
		t.Errorf("want a—b—…, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("the shortened text is not valid UTF-8: %q", got)
	}
}
