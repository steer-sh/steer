package mine

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// --- match ------------------------------------------------------------------

// MatchOpts are the flags of `intervention match`.
type MatchOpts struct {
	Ledger      string
	Regex       string
	Kind        string
	StrictJoins bool
	// Corpus names the evidence the regex is tested against; empty is review-findings.
	Corpus string
	// SignalKind narrows the session corpus to one classifier kind.
	SignalKind string
	// Sessions is the session corpus, supplied by the caller that reads the store.
	Sessions []SessionUnit
	// ShowResidue prints the frequent terms among the findings this regex misses,
	// which turns a verifier into a way of finding the class it should have named.
	ShowResidue bool
	// The scope predicates and cutoff of the record the regex will be registered as.
	// With any scope set, Match also counts the window register would freeze.
	ScopeFlags
	InstalledTS int64
}

// baselinePreview is the registration window a scoped match counts beside the whole
// corpus.
type baselinePreview struct {
	scope    *Scope
	scopeMap map[string]any
	cutoff   float64
	at       float64
}

// preview resolves the window the scope flags describe, or nil when none is set. The
// cutoff defaults to now, as register's does.
func (o MatchOpts) preview(spec CorpusSpec) (*baselinePreview, error) {
	scoped := o.ScopeAll || len(o.ScopeHarness) > 0 || o.ScopeRepo != "" ||
		o.ScopeRepoRoot != "" || o.ScopePath != ""
	if !scoped {
		if o.InstalledTS != 0 {
			return nil, fmt.Errorf("--installed-ts previews a registration baseline and needs " +
				"the record's scope: pass --scope-all or the --scope-* predicates")
		}
		return nil, nil
	}
	if spec.Engine != EngineSession {
		if o.StrictJoins {
			return nil, fmt.Errorf("--strict-joins is not how register builds chains; " +
				"drop it to preview a registration baseline")
		}
		if o.Kind != "accepted" && o.Kind != "rejected" {
			return nil, fmt.Errorf("a registration baseline counts one verdict: pass --kind " +
				"accepted (register --kind defect) or rejected (--kind false-positive)")
		}
	}
	scopeMap, scope, err := resolveScope(spec, o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return nil, err
	}
	at := now()
	cutoff := o.InstalledTS
	if cutoff == 0 {
		cutoff = at
	}
	return &baselinePreview{scope: scope, scopeMap: scopeMap, cutoff: float64(cutoff), at: float64(at)}, nil
}

// header states the window register would freeze and its count under it.
func (p *baselinePreview) header(hits, total int, rate float64, unit string) string {
	return fmt.Sprintf("# registration baseline: %d/%d %s (%s) before %s, scope %s — "+
		"the count `register` freezes with this scope and --installed-ts",
		hits, total, unit, pct(rate), stamp(p.cutoff), pyDumps(p.scopeMap, 0))
}

// mark labels a matched unit by whether the registration window holds it.
func mark(in bool) string {
	if in {
		return "counted in baseline"
	}
	return "outside baseline"
}

// residueMinChains is the number of chains a term must appear in to be listed. A
// term carried by one chain names one commit attempt and sizes no class, and the
// 2-chain floor keeps the list short enough to read in one pass.
const residueMinChains = 2

// residueTermLimit caps the printed list. A longer one stops being read.
const residueTermLimit = 25

// Match tests a candidate class regex against the corpus its record will be counted
// over, routed to the engine that corpus names. It lists every match in the whole
// corpus. Given the record's scope, it also counts the window register would freeze and
// marks each listed unit in or outside it; without one, no count it prints is a baseline.
func Match(w io.Writer, stateDir string, o MatchOpts) error {
	spec, err := writableCorpus(o.Corpus)
	if err != nil {
		return err
	}
	if spec.Engine == EngineSession {
		if o.ShowResidue {
			return fmt.Errorf("--show-residue reads the review corpus; %s is mined by chain-free units",
				spec.Corpus)
		}
		return matchSessions(w, o, spec)
	}
	p, err := o.preview(spec)
	if err != nil {
		return err
	}
	regex, kind, ledger, strictJoins := o.Regex, o.Kind, o.Ledger, o.StrictJoins
	pattern, err := compilePattern(regex)
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(regex), err)
	}
	rows, err := LoadRows(stateDir, ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, strictJoins)
	type match struct {
		ch    Chain
		found []Finding
	}
	var matched []match
	for _, ch := range chains {
		if h := ChainHits(ch, pattern, kind); len(h) > 0 {
			matched = append(matched, match{ch, h})
		}
	}
	hits, total, rate := Incidence(chains, pattern, kind)
	fmt.Fprintf(w, "# %s matches %d/%d chains (%s) in the whole corpus\n", pyRepr(regex), hits, total, pct(rate))
	if p == nil {
		fmt.Fprintln(w, "# not a registration baseline: pass the record's --scope-* flags to count that window")
	} else {
		bHits, bTotal, bRate := Incidence(baselineChains(chains, p.scope, p.cutoff, p.at), pattern, kind)
		fmt.Fprintln(w, p.header(bHits, bTotal, bRate, "chains"))
	}
	for _, m := range matched {
		label := ""
		if p != nil {
			label = "  " + mark(len(baselineChains([]Chain{m.ch}, p.scope, p.cutoff, p.at)) == 1)
		}
		fmt.Fprintf(w, "--- %s  ts=%s%s\n", stamp(numFloat(m.ch.StartTS)), pyStr(m.ch.StartTS), label)
		for _, f := range m.found {
			fmt.Fprintf(w, "  [%s] %s\n", verdictLabel(f), f.Text)
		}
	}
	if len(matched) == 0 {
		fmt.Fprintln(w, "# no matches — the regex is too narrow, or the class is misnamed")
	}
	if o.ShowResidue {
		printResidue(w, chains, pattern, kind)
	}
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	prior, err := OverlappingDecisions(records, chains, pattern, kind)
	if err != nil {
		return err
	}
	if len(prior) > 0 {
		fmt.Fprintf(w, "\n# %d prior decision(s) cover chains this regex matches in the whole corpus:\n", len(prior))
		for _, d := range prior {
			printDecisionShared(w, d.Record, d.Shared, d.Total)
		}
	}
	return nil
}

// printResidue reports the findings of the requested kind that the pattern does
// not match, counted per finding rather than per chain, and ranks the terms
// recurring among them. What it lists is unmatched, not unclassified.
func printResidue(w io.Writer, chains []Chain, pattern *regexp.Regexp, kind string) {
	var groups, corpus []TermGroup
	labels := repoLabels(chains)
	selected, residue, residueChains := 0, 0, 0
	for i, ch := range chains {
		all := FindingsOfKind(ch, kind)
		selected += len(all)
		if len(all) > 0 {
			corpus = append(corpus, TermGroup{Repo: labels[i], Findings: all})
		}
		var missed []Finding
		for _, f := range all {
			if !pattern.MatchString(f.Text) {
				missed = append(missed, f)
			}
		}
		if len(missed) == 0 {
			continue
		}
		residue += len(missed)
		residueChains++
		groups = append(groups, TermGroup{Repo: labels[i], Findings: missed})
	}

	fmt.Fprintf(w, "\n# residue: %d of %d %s findings this regex misses, over %d chain(s) of the whole corpus\n",
		residue, selected, kind, residueChains)
	if residue == 0 {
		fmt.Fprintln(w, "# nothing is left over — the regex covers every finding of this kind")
		return
	}
	ranked := RankTerms(groups, corpus, residueMinChains)
	if len(ranked) == 0 {
		fmt.Fprintf(w, "# no term recurs in %d or more of them; the residue has no shared vocabulary to widen toward\n",
			residueMinChains)
		return
	}
	shown := ranked
	if len(shown) > residueTermLimit {
		shown = shown[:residueTermLimit]
	}
	fmt.Fprintf(w, "# frequent terms among them — showing %d of %d listed (a term in fewer than %d chains is omitted)\n",
		len(shown), len(ranked), residueMinChains)
	fmt.Fprintln(w, "# each is chains-in-residue of chains-in-corpus: where the two are equal the term is the corpus's vocabulary, not a name for the residue")
	for _, t := range shown {
		fmt.Fprintf(w, "#   %4d/%-4d  %-22s %s: %s\n",
			t.Chains, t.CorpusChains, t.Term, t.ExampleRepo, ellipsis(t.Example, 80))
	}
	fmt.Fprintln(w, "# this is what the regex misses, not what no class covers — `mine findings --undecided` answers that")
}

// ellipsis shortens a finding to one readable line. The limit counts runes, not
// bytes: findings carry em-dashes and curly quotes, and a byte cut lands inside
// one of those and writes a broken character to the reader's terminal.
func ellipsis(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
