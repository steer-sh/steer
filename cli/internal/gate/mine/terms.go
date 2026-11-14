package mine

import (
	"sort"
	"strings"
	"unicode"
)

// termStopwords are the words that carry no discriminating signal in a finding.
// They are the ordinary connective tissue of English plus the review vocabulary
// every finding shares, which would otherwise top every ranking.
var termStopwords = map[string]bool{
	"and": true, "are": true, "but": true, "for": true, "from": true, "has": true,
	"have": true, "how": true, "into": true, "its": true, "not": true, "now": true,
	"one": true, "only": true, "over": true, "such": true, "than": true, "that": true,
	"the": true, "their": true, "them": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "under": true, "use": true, "used": true,
	"uses": true, "was": true, "were": true, "what": true, "when": true, "where": true,
	"which": true, "while": true, "will": true, "with": true, "would": true, "you": true,
	"commit": true, "change": true, "changes": true, "code": true, "file": true,
	"files": true, "line": true, "lines": true, "test": true, "tests": true,
	// Vague quantifiers, modals and time words. Each one is frequent in any prose
	// about a defect and names no kind of defect, so each would otherwise sit at
	// the top of every ranking regardless of the evidence beneath it.
	"after": true, "all": true, "also": true, "any": true, "because": true,
	"been": true, "before": true, "being": true, "both": true, "can": true,
	"could": true, "did": true, "does": true, "done": true, "each": true,
	"every": true, "get": true, "gets": true, "got": true, "here": true,
	"just": true, "like": true, "made": true, "make": true, "makes": true,
	"may": true, "more": true, "most": true, "much": true, "must": true,
	"never": true, "new": true, "other": true, "same": true, "should": true,
	"since": true, "some": true, "still": true, "very": true, "via": true,
	"yet": true,
}

// Terms are the distinct words of a finding, lowercased and deduplicated.
//
// A word is a run of letters and digits; every other rune separates. A token
// shorter than three runes, a token of digits alone, and a stopword are dropped,
// because none of them can tell one class of finding from another.
func Terms(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(field) < 3 || termStopwords[field] || !strings.ContainsFunc(field, unicode.IsLetter) {
			continue
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// TermCount is one term and the evidence it was counted over.
type TermCount struct {
	Term string
	// Chains is how many chains carry the term among the findings counted, which
	// is the number the ranking reads: a term repeated inside one chain describes
	// one commit attempt, not a recurrence.
	Chains int
	// CorpusChains is how many chains carry it across the whole corpus. A term
	// whose two counts are equal says nothing about the subset it was ranked in —
	// it is the vocabulary of the corpus, not a name for what is left over.
	CorpusChains int
	// Example is the first finding text carrying the term, for a reader deciding
	// whether the term names something.
	Example string
	// ExampleRepo is the repository label of the chain that example came from.
	ExampleRepo string
}

// TermGroup is one chain's findings, for counting terms across chains.
type TermGroup struct {
	Repo     string
	Findings []Finding
}

// CountTerms counts each term once per group, so a word repeated inside one
// chain adds one.
func CountTerms(groups []TermGroup) map[string]int {
	counts := map[string]int{}
	for _, g := range groups {
		seen := map[string]bool{}
		for _, f := range g.Findings {
			for _, term := range Terms(f.Text) {
				if seen[term] {
					continue
				}
				seen[term] = true
				counts[term]++
			}
		}
	}
	return counts
}

// RankTerms ranks the terms of groups by how many of those chains carry them,
// and reports each one's count across corpus beside it. minChains drops the terms
// appearing in fewer, because a term carried by one chain sizes no class. Ties
// are broken lexically, so the same evidence ranks the same way on every run.
func RankTerms(groups, corpus []TermGroup, minChains int) []TermCount {
	counts := CountTerms(groups)
	corpusCounts := CountTerms(corpus)

	examples := map[string]TermCount{}
	for _, g := range groups {
		for _, f := range g.Findings {
			for _, term := range Terms(f.Text) {
				if _, ok := examples[term]; ok {
					continue
				}
				examples[term] = TermCount{Example: f.Text, ExampleRepo: g.Repo}
			}
		}
	}

	out := make([]TermCount, 0, len(counts))
	for term, n := range counts {
		if n < minChains {
			continue
		}
		t := examples[term]
		t.Term, t.Chains, t.CorpusChains = term, n, corpusCounts[term]
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chains != out[j].Chains {
			return out[i].Chains > out[j].Chains
		}
		return out[i].Term < out[j].Term
	})
	return out
}
