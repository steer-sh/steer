package mine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/steerpath"
	"github.com/steer-sh/steer-cli/internal/tier"
)

// now is the wall clock for records that carry no explicit timestamp. A variable so a
// test can pin it.
var now = func() int64 { return time.Now().Unix() }

// stamp renders a unix timestamp as mine.py's stamp does: a local YYYY-MM-DD, or "?".
func stamp(ts float64) string {
	if ts == 0 {
		return "?"
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02")
}

func pct(rate float64) string { return fmt.Sprintf("%.1f%%", rate*100) }

// pyStr renders a value the way Python's str() would in an f-string.
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case *string:
		if t == nil {
			return "None"
		}
		return *t
	case json.Number:
		return t.String()
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return string(pyFloat(t))
	}
	return fmt.Sprint(v)
}

// --- findings ---------------------------------------------------------------

// FindingsOpts are the flags of `mine findings`.
type FindingsOpts struct {
	Ledger       string
	Kind         string
	Since        float64
	StrictJoins  bool
	IncludeEmpty bool
	// Undecided drops the chains whose every selected finding a live class already
	// covers, and marks the covered findings inside the chains it keeps.
	Undecided bool
	JSON      bool
}

// RenderedFinding is a finding plus what the dump knows about it. CoveredBy is the
// classes covering it, populated only in the undecided view, so a default dump
// emits exactly the JSON it emitted before the view existed.
type RenderedFinding struct {
	Finding
	CoveredBy []string `json:"covered_by,omitempty"`
}

// Findings emits adjudicated findings grouped by chain, for a reader to cluster.
func Findings(w io.Writer, stateDir string, o FindingsOpts) error {
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, o.StrictJoins)
	var cutoff float64
	if o.Since != 0 {
		cutoff = float64(now()) - o.Since*86400
	}
	var kept []Chain
	for _, ch := range chains {
		if numFloat(ch.StartTS) >= cutoff {
			kept = append(kept, ch)
		}
	}
	chains = kept

	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	// The decided classes are read over the whole time-filtered corpus, so every
	// incidence the dump prints keeps its denominator when --undecided narrows
	// what the reader is shown.
	var deciding []Deciding
	if o.Undecided {
		deciding = DecidingClasses(records)
	}

	var payload []map[string]any
	var shown []Chain
	selected, covered, residue, carrying, residueChains := 0, 0, 0, 0, 0
	for _, ch := range chains {
		findings := renderFindings(filterFindings(ch.Findings, o.Kind), deciding, o.Undecided)
		selected += len(findings)
		if len(findings) > 0 {
			carrying++
		}
		chainResidue := 0
		for _, f := range findings {
			if len(f.CoveredBy) > 0 {
				covered++
			} else {
				chainResidue++
			}
		}
		residue += chainResidue
		if chainResidue > 0 {
			residueChains++
		}
		if len(findings) == 0 && !o.IncludeEmpty {
			continue
		}
		if o.Undecided && chainResidue == 0 && len(findings) > 0 {
			continue
		}
		entry := map[string]any{
			"date":      stamp(numFloat(ch.StartTS)),
			"start_ts":  ch.StartTS,
			"terminal":  ch.Terminal,
			"rounds":    ch.Rounds,
			"files":     ch.Files,
			"findings":  findings,
			"repo":      nil,
			"repo_root": nil,
		}
		if ch.RepoKnown {
			entry["repo"] = ch.Repo
		}
		if ch.RepoRootKnown {
			entry["repo_root"] = ch.RepoRoot
		}
		shown = append(shown, ch)
		payload = append(payload, entry)
	}
	reviewed := len(chains)

	if o.JSON {
		out := map[string]any{
			"chains_reviewed": reviewed,
			"decisions":       nonNilRecords(records),
			"chains":          nonNilMaps(payload),
		}
		if o.Undecided {
			out["selected_findings"] = selected
			out["covered_findings"] = covered
			out["residual_findings"] = residue
			out["residual_chains"] = residueChains
		}
		return writeJSON(w, out)
	}

	if len(records) > 0 {
		fmt.Fprintf(w, "# %d class(es) already decided on this corpus:\n", len(records))
		for _, r := range records {
			printDecisionIncidence(w, r, chains)
		}
		fmt.Fprintln(w, "#")
	}
	// Every header count describes the whole time-filtered corpus. --undecided
	// narrows what is printed below, and says so on its own line, so no line
	// reports the filtered subset under a name that means the corpus.
	if o.Undecided {
		fmt.Fprintf(w, "# %d chains reviewed, %d carrying %s findings\n", reviewed, carrying, o.Kind)
		fmt.Fprintf(w, "# %d distinct findings\n", selected)
		fmt.Fprintf(w, "# --undecided: %d covered, %d remaining over %d chain(s) — those %d chains are what is printed below\n",
			covered, residue, residueChains, len(payload))
		fmt.Fprintln(w, "# a covered finding is shown in place, marked [covered-by=...]; quote the text without the marker")
	} else {
		fmt.Fprintf(w, "# %d chains reviewed, %d carrying %s findings\n", reviewed, len(payload), o.Kind)
		distinct := 0
		for _, c := range payload {
			distinct += len(c["findings"].([]RenderedFinding))
		}
		fmt.Fprintf(w, "# %d distinct findings\n", distinct)
	}
	fmt.Fprintln(w)
	labels := repoLabels(shown)
	for i, c := range payload {
		fmt.Fprintf(w, "--- %s  ts=%s  repo=%s  rounds=%d  files=%d  end=%s\n",
			c["date"], pyStr(c["start_ts"]), labels[i], c["rounds"], c["files"], pyStr(c["terminal"]))
		for _, f := range c["findings"].([]RenderedFinding) {
			if len(f.CoveredBy) > 0 {
				fmt.Fprintf(w, "  [%s] [covered-by=%s] %s\n", f.Verdict, strings.Join(f.CoveredBy, ","), f.Text)
				continue
			}
			fmt.Fprintf(w, "  [%s] %s\n", f.Verdict, f.Text)
		}
		fmt.Fprintln(w)
	}
	return nil
}

// repoLabels names each chain's repository for the text dump, one label per chain
// in the order given. A basename is enough to read, but two checkouts can share
// one: where a basename covers more than one root among the chains shown, every
// chain carrying that basename is labelled by its whole root instead. A chain
// whose rows carried no repository metadata is "unknown", never blank, because a
// missing fact and an empty one are different readings.
func repoLabels(chains []Chain) []string {
	roots := map[string]map[string]bool{}
	for _, ch := range chains {
		// A chain with no root of its own cannot be disambiguated by one, so it
		// must not create the collision that sends the others to their full paths.
		if !ch.RepoKnown || !ch.RepoRootKnown {
			continue
		}
		if roots[ch.Repo] == nil {
			roots[ch.Repo] = map[string]bool{}
		}
		roots[ch.Repo][ch.RepoRoot] = true
	}
	labels := make([]string, len(chains))
	for i, ch := range chains {
		switch {
		case !ch.RepoKnown:
			labels[i] = "unknown"
		case len(roots[ch.Repo]) > 1 && ch.RepoRootKnown:
			labels[i] = ch.RepoRoot
		default:
			labels[i] = ch.Repo
		}
	}
	return labels
}

// renderFindings pairs each finding with the classes covering it. Outside the
// undecided view nothing is consulted, so no finding carries a marker.
func renderFindings(findings []Finding, deciding []Deciding, undecided bool) []RenderedFinding {
	out := make([]RenderedFinding, 0, len(findings))
	for _, f := range findings {
		r := RenderedFinding{Finding: f}
		if undecided {
			r.CoveredBy = CoveringIDs(f, deciding)
		}
		out = append(out, r)
	}
	return out
}

func filterFindings(findings []Finding, kind string) []Finding {
	out := []Finding{}
	for _, f := range findings {
		if KindFilter(f.Verdict, kind) {
			out = append(out, f)
		}
	}
	return out
}

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
	ScopeAll      bool
	ScopeHarness  []string
	ScopeRepo     string
	ScopeRepoRoot string
	ScopePath     string
	InstalledTS   int64
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
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return nil, err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return nil, err
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
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
			fmt.Fprintf(w, "  [%s] %s\n", f.Verdict, f.Text)
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

// --- decision headlines -----------------------------------------------------

func decisionHeadline(record Record) string {
	class := str(record["class"])
	if IsRejected(record) {
		status := strings.ToUpper(getString(record, "status", "rejected"))
		return fmt.Sprintf("%s %s  %s — %s", status, getString(record, "rejected_date", "?"),
			class, getString(record, "reason", ""))
	}
	date := str(record["installed_date"])
	if date == "" {
		date = getString(record, "rejected_date", "?")
	}
	return fmt.Sprintf("%s %s  %s -> %s", strings.ToUpper(getString(record, "status", "active")),
		date, class, str(record["tier"]))
}

func printDecisionHead(w io.Writer, record Record) {
	fmt.Fprintf(w, "#   %s\n", decisionHeadline(record))
	fmt.Fprintf(w, "#     id=%s  match=%s\n", str(record["id"]), pyRepr(str(record["match"])))
}

func printDecisionReopen(w io.Writer, record Record) {
	if r := getString(record, "reopen", ""); r != "" {
		fmt.Fprintf(w, "#     reopen if: %s\n", r)
	}
}

// printReopenIncidence scores a refusal's stored reopen regex against the corpus
// now. Without it a reason citing a subset count names a threshold no later
// reader can evaluate.
func printReopenIncidence(w io.Writer, record Record, chains []Chain) {
	expr := getString(record, "reopen_match", "")
	if expr == "" {
		return
	}
	pattern, err := compilePattern(expr)
	if err != nil {
		return
	}
	hits, denom, rate := Incidence(chains, pattern, RecordKindFilter(record))
	at := getInt(record, "reopen_at", 0)
	state := ""
	if at > 0 {
		state = ", below the reopen threshold"
		if hits >= at {
			state = fmt.Sprintf(", REOPEN CONDITION MET (>= %d)", at)
		}
	}
	fmt.Fprintf(w, "#     reopen subset %s now %d/%d chains (%s)%s\n",
		pyRepr(expr), hits, denom, pct(rate), state)
}

func printDecisionShared(w io.Writer, record Record, shared, total int) {
	printDecisionHead(w, record)
	fmt.Fprintf(w, "#     shares %d/%d of the candidate's chains\n", shared, total)
	printDecisionReopen(w, record)
}

func printDecisionIncidence(w io.Writer, record Record, chains []Chain) {
	printDecisionHead(w, record)
	pattern, err := compilePattern(str(record["match"]))
	if err == nil {
		hits, denom, rate := Incidence(chains, pattern, RecordKindFilter(record))
		fmt.Fprintf(w, "#     now %d/%d chains (%s)\n", hits, denom, pct(rate))
	}
	printDecisionReopen(w, record)
	printReopenIncidence(w, record, chains)
}

// --- register / reject ------------------------------------------------------

// RegisterOpts are the flags of `mine register`.
type RegisterOpts struct {
	Ledger     string
	Class      string
	Match      string
	Kind       string
	Corpus     string
	SignalKind string
	Sessions   []SessionUnit
	Tier       string
	Target     string
	Note       string
	Prose      Prose
	HookID     string
	HookScope  string
	RuleID     string
	RuleScope  string
	// AddendumID names the review addendum entry a review-tier record binds.
	AddendumID    string
	AddendumScope string
	// CheckID names the declared check a check-tier record binds, and CheckFacts is
	// that check's frozen state, read by the caller: the adapters that resolve a
	// registration live outside this package.
	CheckID    string
	CheckFacts map[string]any
	// NotMechanizable is required when Tier names the instruction rung.
	NotMechanizable string
	Supersedes      string
	InstalledTS     int64
	Force           bool
	ScopeAll        bool
	ScopeHarness    []string
	ScopeRepo       string
	ScopeRepoRoot   string
	ScopePath       string
}

// Register records an intervention with its baseline incidence.
func Register(w io.Writer, stateDir string, o RegisterOpts) error {
	if o.Kind == "" {
		o.Kind = "defect"
	}
	if err := o.Prose.Validate(o.Class); err != nil {
		return err
	}
	spec, err := writableCorpus(o.Corpus)
	if err != nil {
		return err
	}
	kindFilter := "accepted"
	if o.Kind == "false-positive" {
		kindFilter = "rejected"
	}
	pattern, err := compilePattern(o.Match)
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(o.Match), err)
	}
	if err := spec.ValidateSignalKind(o.SignalKind); err != nil {
		return err
	}
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return err
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return err
	}
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, false)
	installed := o.InstalledTS
	if installed == 0 {
		installed = now()
	}
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	var bHits, bTotal int
	var bRate float64
	var instrument any
	var decisions []Decision
	if spec.Engine == EngineSession {
		signalKind := o.SignalKind
		if signalKind == "" {
			signalKind = SignalAny
		}
		bHits, bTotal, bRate = SessionIncidence(baselineSessions(o.Sessions, scope, float64(installed)),
			pattern, signalKind)
		instrument = sessionInstrumentValue(o.Sessions)
		decisions, err = SessionOverlappingDecisions(records, o.Sessions, pattern, signalKind)
	} else {
		bHits, bTotal, bRate = Incidence(baselineChains(chains, scope, float64(installed), float64(now())),
			pattern, kindFilter)
		instrument = instrumentValue(chains)
		decisions, err = OverlappingDecisions(recordsForCorpus(records, spec.Corpus), chains, pattern, kindFilter)
	}
	if err != nil {
		return err
	}
	if bHits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — an intervention " +
			"with no baseline can never be scored. Widen the scope or the regex, or pass --force")
	}
	if !o.Force {
		for _, d := range decisions {
			if StandsRejected(d.Record) && d.Ratio >= OverlapBlock && str(d.Record["id"]) != o.Supersedes {
				return fmt.Errorf("this regex shares %d/%d of its chains with %s, rejected on %s: %s\n"+
					"  reopen if: %s\n"+
					"Install it anyway with --force, or --supersedes to retire the rejection",
					d.Shared, d.Total, pyRepr(str(d.Record["id"])), getString(d.Record, "rejected_date", "?"),
					getString(d.Record, "reason", ""), getString(d.Record, "reopen", "(unrecorded)"))
			}
		}
	}
	// The ladder's vocabulary is validated here, at the shared write boundary, so
	// the CLI and the dashboard refuse the same values. A typo tier used to fall
	// through every display and render as itself.
	if !tier.Valid(o.Tier) {
		return fmt.Errorf("--tier %q names no fix type. Use one of: %s",
			o.Tier, strings.Join(tier.Accepted(), ", "))
	}
	if tier.IsGuard(o.Tier) && o.HookID == "" {
		return fmt.Errorf("a guard tier needs --hook-id (the steer-manifest key, which is " +
			"also the audit log's hook_name). Without it the audit-log join is a path search " +
			"that breaks the moment the hook is renamed or retired")
	}
	// A check record binds the check whose execution evidence it is scored beside,
	// and the repository that check runs in.
	if tier.IsCheck(o.Tier) {
		if err := requireCheckBinding(o.CheckID, o.CheckFacts); err != nil {
			return err
		}
		if err := requireCheckScope(o.CheckID, o.CheckFacts, scopeMap); err != nil {
			return err
		}
	}
	// An instruction record names the rule its digest was taken over. Without it the
	// record names a document and no text inside it, and nothing later can say which
	// prose the decision was made about.
	if tier.IsInstruction(o.Tier) {
		if o.RuleID == "" {
			return fmt.Errorf("an instruction tier needs --rule-id (the steer-manifest rules " +
				"key). Install the rule first with `steer rules add <id> --rule \"<text>\"`")
		}
		// The instruction rung is the last one to reach for, so the record states why
		// the two rungs above it cannot detect this class.
		if err := ValidateArgument(NotMechanizableFlag, o.NotMechanizable,
			"why a repository check and a pre-tool guard cannot detect this class",
			o.Class, ""); err != nil {
			return err
		}
	}
	// A review record names the addendum entry it installed, and only a reviewer false
	// positive routes to the review prompt.
	if tier.IsReview(o.Tier) {
		if o.AddendumID == "" {
			return fmt.Errorf("a review tier needs --addendum-id (the steer-manifest " +
				"review_addendum key). Install the entry first with " +
				"`steer commit-gate addendum add <id> --text \"<text>\"`")
		}
		if o.Kind != "false-positive" {
			return fmt.Errorf("a review tier needs --kind false-positive: the review prompt " +
				"changes what the reviewer raises, so a defect class never routes there")
		}
	}
	record := Record{
		"id":             UniqueID(Slugify(o.Class), records),
		"class":          o.Class,
		"match":          o.Match,
		"kind":           o.Kind,
		"corpus":         spec.Corpus,
		"unit":           spec.Unit,
		"tier":           o.Tier,
		"target":         o.Target,
		"note":           o.Note,
		"installed_ts":   int(installed),
		"installed_date": stamp(float64(installed)),
		"status":         "active",
		"scope":          scopeMap,
		"baseline":       baselineMap(bHits, bTotal, bRate),
		"instrument":     instrument,
	}
	o.Prose.writeInto(record)
	if spec.Engine == EngineSession {
		if o.SignalKind != "" {
			record["signal_kind"] = o.SignalKind
		} else {
			record["signal_kind"] = SignalAny
		}
	}
	for k, v := range o.CheckFacts {
		record[k] = v
	}
	if o.HookID != "" {
		record["hook_id"] = o.HookID
		facts, err := SteerHookFacts(o.HookID, o.HookScope)
		if err != nil {
			return err
		}
		if facts != nil {
			for k, v := range facts {
				record[k] = v
			}
		} else {
			fmt.Fprintf(os.Stderr, "steer commit-gate mine: %s is not in steer-manifest.json — "+
				"recording the id anyway, but confirm it matches the audit log's hook_name\n", pyRepr(o.HookID))
		}
	}
	if v := strings.TrimSpace(o.NotMechanizable); v != "" {
		record[NotMechanizableKey] = v
	}
	if o.RuleID != "" {
		record["rule_id"] = o.RuleID
		facts, err := SteerRuleFacts(o.RuleID, o.RuleScope)
		if err != nil {
			return err
		}
		if facts == nil {
			return fmt.Errorf("no rule %s in the steer store. Install it first with "+
				"`steer rules add %s --rule \"<text>\"`", pyRepr(o.RuleID), o.RuleID)
		}
		for k, v := range facts {
			record[k] = v
		}
	}
	if o.AddendumID != "" {
		record["addendum_id"] = o.AddendumID
		facts, err := SteerAddendumFacts(o.AddendumID, o.AddendumScope)
		if err != nil {
			return err
		}
		if facts == nil {
			return fmt.Errorf("no review addendum entry %s in the steer store. Install it first "+
				"with `steer commit-gate addendum add %s --text \"<text>\"`",
				pyRepr(o.AddendumID), o.AddendumID)
		}
		if facts["addendum_scope"] == steerpath.ProjectScope {
			if err := RequireAddendumAtHead(o.AddendumID, str(facts["addendum"])); err != nil {
				return err
			}
		}
		for k, v := range facts {
			record[k] = v
		}
	}
	if o.Supersedes != "" {
		prior := findRecord(records, o.Supersedes)
		if prior == nil {
			return fmt.Errorf("no intervention with id %s", pyRepr(o.Supersedes))
		}
		if IsRejected(prior) {
			prior["status"] = "reopened"
		} else {
			prior["status"] = "reworked"
		}
		prior["retired_ts"] = int(installed)
		prior["superseded_by"] = record["id"]
		record["supersedes"] = o.Supersedes
	}
	records = append(records, record)
	if err := save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(record, 2))
	return nil
}

// RejectOpts are the flags of `mine reject`.
type RejectOpts struct {
	Ledger        string
	Class         string
	Match         string
	Kind          string
	Corpus        string
	SignalKind    string
	Sessions      []SessionUnit
	Reason        string
	Reopen        string
	ReopenMatch   string
	ReopenAt      int
	Note          string
	Prose         Prose
	RejectedTS    int64
	Force         bool
	ScopeAll      bool
	ScopeHarness  []string
	ScopeRepo     string
	ScopeRepoRoot string
	ScopePath     string
}

// validateReopenSubset refuses half a reopen trigger. A regex without a count
// names no threshold, and a count without a regex names no subset to count.
func validateReopenSubset(match string, at int) error {
	switch {
	case match == "" && at == 0:
		return nil
	case match == "":
		return fmt.Errorf("--reopen-at needs --reopen-match: a threshold with no regex " +
			"cannot be evaluated by a later pass")
	case at == 0:
		return fmt.Errorf("--reopen-match needs --reopen-at: a subset regex with no " +
			"threshold never reports itself met")
	}
	if _, err := compilePattern(match); err != nil {
		return fmt.Errorf("--reopen-match does not compile: %w", err)
	}
	return nil
}

// Reject records a class examined and refused, so it is not re-proposed.
func Reject(w io.Writer, stateDir string, o RejectOpts) error {
	if o.Kind == "" {
		o.Kind = "defect"
	}
	if err := o.Prose.Validate(o.Class); err != nil {
		return err
	}
	if err := validateReopenSubset(o.ReopenMatch, o.ReopenAt); err != nil {
		return err
	}
	spec, err := writableCorpus(o.Corpus)
	if err != nil {
		return err
	}
	kindFilter := "accepted"
	if o.Kind == "false-positive" {
		kindFilter = "rejected"
	}
	pattern, err := compilePattern(o.Match)
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(o.Match), err)
	}
	if err := spec.ValidateSignalKind(o.SignalKind); err != nil {
		return err
	}
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return err
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return err
	}
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	chains := BuildChains(rows, false)
	rejected := o.RejectedTS
	if rejected == 0 {
		rejected = now()
	}
	var bHits, bTotal int
	var bRate float64
	var instrument any
	if spec.Engine == EngineSession {
		signalKind := o.SignalKind
		if signalKind == "" {
			signalKind = SignalAny
		}
		bHits, bTotal, bRate = SessionIncidence(baselineSessions(o.Sessions, scope, float64(rejected)),
			pattern, signalKind)
		instrument = sessionInstrumentValue(o.Sessions)
	} else {
		bHits, bTotal, bRate = Incidence(baselineChains(chains, scope, float64(rejected), float64(now())),
			pattern, kindFilter)
		instrument = instrumentValue(chains)
	}
	if bHits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — a rejection with " +
			"no baseline can never be re-scored, and cannot be recognised when the class " +
			"returns. Widen the scope or the regex, or pass --force")
	}
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	record := Record{
		"id":            UniqueID(Slugify(o.Class), records),
		"class":         o.Class,
		"match":         o.Match,
		"kind":          o.Kind,
		"corpus":        spec.Corpus,
		"unit":          spec.Unit,
		"tier":          "none",
		"target":        "",
		"note":          o.Note,
		"reason":        o.Reason,
		"reopen":        o.Reopen,
		"reopen_match":  o.ReopenMatch,
		"reopen_at":     o.ReopenAt,
		"rejected_ts":   int(rejected),
		"rejected_date": stamp(float64(rejected)),
		"status":        "rejected",
		"scope":         scopeMap,
		"baseline":      baselineMap(bHits, bTotal, bRate),
		"instrument":    instrument,
	}
	o.Prose.writeInto(record)
	if spec.Engine == EngineSession {
		if o.SignalKind != "" {
			record["signal_kind"] = o.SignalKind
		} else {
			record["signal_kind"] = SignalAny
		}
	}
	records = append(records, record)
	if err := save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(record, 2))
	return nil
}

// ScopeSetOpts are the flags of `mine scope`.
type ScopeSetOpts struct {
	Ledger        string
	Sessions      []SessionUnit
	ScopeAll      bool
	ScopeHarness  []string
	ScopeRepo     string
	ScopeRepoRoot string
	ScopePath     string
	ScopeSetTS    int64
	Force         bool
}

// ScopeSet installs a scope on a legacy, unscoped record. It recomputes the scoped
// baseline and stamps scope_set_ts, which opens the after-window so a migration cannot
// select on already-observed post-install results. An existing scope is never edited;
// correcting one is a retire plus a fresh register.
func ScopeSet(w io.Writer, stateDir, id string, o ScopeSetOpts) error {
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	rec := findRecord(records, id)
	if rec == nil {
		return fmt.Errorf("no intervention with id %s", pyRepr(id))
	}
	if _, has := rec["scope"]; has {
		return fmt.Errorf("%s already carries a scope — an installed scope is frozen. "+
			"Retire it and register a fresh record to correct the eligibility rule", pyRepr(id))
	}
	spec, err := writableCorpus(CorpusOf(rec))
	if err != nil {
		return err
	}
	scopeMap, err := BuildScope(o.ScopeAll, o.ScopeHarness, o.ScopeRepo, o.ScopeRepoRoot, o.ScopePath)
	if err != nil {
		return err
	}
	if err := spec.ValidateScope(scopeMap); err != nil {
		return err
	}
	if fix, bound := CheckFixOf(rec); bound {
		facts := map[string]any{CheckRepoKey: fix.Repo}
		if err := requireCheckScope(fix.ID, facts, scopeMap); err != nil {
			return err
		}
	}
	scope, _, err := ParseScope(Record{"scope": scopeMap})
	if err != nil {
		return err
	}
	pattern, err := compilePattern(str(rec["match"]))
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(str(rec["match"])), err)
	}
	var bHits, bTotal int
	var bRate float64
	if spec.Engine == EngineSession {
		bHits, bTotal, bRate = SessionIncidence(baselineSessions(o.Sessions, scope, SplitTS(rec)),
			pattern, SessionSignalKind(rec))
	} else {
		rows, err := LoadRows(stateDir, o.Ledger)
		if err != nil {
			return err
		}
		bHits, bTotal, bRate = Incidence(baselineChains(BuildChains(rows, false), scope, SplitTS(rec), float64(now())),
			pattern, RecordKindFilter(rec))
	}
	if bHits == 0 && !o.Force {
		return fmt.Errorf("regex matches no unit in the scoped baseline — widen the scope, or pass --force")
	}
	setTS := o.ScopeSetTS
	if setTS == 0 {
		setTS = now()
	}
	rec["scope"] = scopeMap
	rec["scope_set_ts"] = int(setTS)
	rec["baseline"] = baselineMap(bHits, bTotal, bRate)
	if err := save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(rec, 2))
	return nil
}

// baselineChains is the eligible before-window a writer freezes its baseline over: the
// chains starting before cutoff that the scope admits. It runs the same evidence pass
// ScoreRecord runs, read against the clock at, so the stored baseline and the scored
// one describe one set of chains. A chain with no adjudication is not evidence of the
// class's absence, so it belongs in neither.
func baselineChains(chains []Chain, scope *Scope, cutoff, at float64) []Chain {
	before, _ := SplitChains(chains, cutoff)
	return CoverEvidence(before, scope, at).EligibleChains()
}

// baselineSessions is the session engine's baseline window: the sessions wholly before
// cutoff that the scope admits. A session spanning cutoff belongs to neither window.
func baselineSessions(units []SessionUnit, scope *Scope, cutoff float64) []SessionUnit {
	before, _, _ := SplitSessions(units, cutoff)
	return SessionBaselineWindow(before, scope)
}

// baselineMap is the stored baseline in the one candidate-record contract. Its
// count key is the corpus-neutral "units", not "chains": the record no longer
// assumes a chain corpus. The field is write-only today — nothing reads the stored
// baseline count, so legacy rows still keyed "chains" break no reader; a reader
// built later tolerates both keys at its own call site.
func baselineMap(hits, total int, rate float64) map[string]any {
	return map[string]any{"hits": hits, "units": total, "rate": pyFloat(round4(rate))}
}

// instrumentValue is the current reviewer, or nil so the record stores null the way
// mine.py stores None on an unstamped log.
func instrumentValue(chains []Chain) any {
	if inst := CurrentInstrument(chains); inst != "" {
		return inst
	}
	return nil
}

func findRecord(records []Record, id string) Record {
	for _, r := range records {
		if str(r["id"]) == id {
			return r
		}
	}
	return nil
}

// --- score ------------------------------------------------------------------

// ScoreOpts are the flags of `mine score`.
type ScoreOpts struct {
	Ledger string
	ID     string
	All    bool
	JSON   bool
	// Sessions is the transcript-friction corpus, and Firings the guard audit stream.
	// A caller that reads only the ledger leaves them empty; a record scored by the
	// session engine then reports missing evidence rather than a range.
	Sessions     []SessionUnit
	Firings      []Firing
	AuditPresent bool
	CheckRuns    []CheckRun
	CheckRepos   map[string]bool
	// FixtureReceipts and the two identities that resolved them travel together:
	// a receipt read on another machine or in another checkout binds no record here.
	FixtureReceipts    map[FixtureKey]FixtureReceipt
	FixtureHost        string
	FixtureProjectRoot string
}

// Score recomputes each intervention's before/after incidence and verdict. With
// JSON set it prints an array of scored rows, `[]` when none is selected; each
// row's before/after `chains` counts the record's own unit, whatever its corpus.
func Score(w io.Writer, stateDir string, o ScoreOpts) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	if o.ID != "" {
		records = filterByID(records, o.ID)
		if len(records) == 0 {
			return fmt.Errorf("no intervention with id %s", pyRepr(o.ID))
		}
	} else if !o.All {
		var kept []Record
		for _, r := range records {
			s := str(r["status"])
			if s == "active" || s == "rejected" {
				kept = append(kept, r)
			}
		}
		records = kept
	}
	if len(records) == 0 {
		if o.JSON {
			return writeJSON(w, []map[string]any{})
		}
		fmt.Fprintln(w, "# no interventions recorded yet")
		return nil
	}
	rows, err := LoadRows(stateDir, o.Ledger)
	if err != nil {
		return err
	}
	ev := EvidenceSet{
		Chains:       BuildChains(rows, false),
		Sessions:     o.Sessions,
		Firings:      o.Firings,
		AuditPresent: o.AuditPresent,
		CheckRuns:    o.CheckRuns,
		CheckRepos:   o.CheckRepos,

		FixtureReceipts:    o.FixtureReceipts,
		FixtureHost:        o.FixtureHost,
		FixtureProjectRoot: o.FixtureProjectRoot,
	}
	results := make([]map[string]any, 0, len(records))
	var unscored []string
	for _, r := range records {
		// A record from a corpus no engine reads is carried unscored. Naming one by
		// --id is an error, because the caller asked for a verdict nothing can give.
		if !Scorable(r) {
			if o.ID != "" {
				return requireScorable(r)
			}
			unscored = append(unscored, str(r["id"]))
			continue
		}
		res, err := ScoreWith(r, ev)
		if err != nil {
			return err
		}
		results = append(results, res)
	}
	if o.JSON {
		return writeJSON(w, results)
	}
	if len(unscored) > 0 {
		sort.Strings(unscored)
		fmt.Fprintf(w, "# unscored here (no engine reads that corpus): %s\n", strings.Join(unscored, ", "))
	}
	for _, r := range results {
		where := firstNonEmpty(str(r["hook_id"]), str(r["target"]), "nothing installed")
		obs, _ := r["observation"].(Observation)
		fmt.Fprintf(w, "%s  [%s]  %s -> %s\n", r["id"], observationState(obs, r["scope"] != nil), r["tier"], where)
		fmt.Fprintf(w, "    class: %s\n", r["class"])
		writeObservation(w, obs)
		verb := "installed"
		if refusalStatuses[str(r["status"])] {
			verb = "rejected"
		}
		fmt.Fprintf(w, "    %s %s  status %s\n", verb, r["installed"], r["status"])
		if str(r["reopen"]) != "" {
			fmt.Fprintf(w, "    reason: %s\n", r["reason"])
			fmt.Fprintf(w, "    reopen if: %s\n", r["reopen"])
		}
		fmt.Fprintln(w)
	}
	return nil
}

// observationState is the bracketed state on a scored line. It names what the
// corpus recorded and claims no effect, so no reading of it can become one.
//
// It reads the frozen scope for the same reason status.ActionFor does: a record
// with no rule for which work counts had every unit treated as eligible, so its
// counts describe a window nobody defined. Reporting that as quiet would print a
// settled state over the one fault the reader can repair.
func observationState(obs Observation, scoped bool) string {
	switch {
	case obs.Recurrence.Occurred:
		return "recurred"
	case !scoped:
		return "no-scope"
	case MissingCoverage(obs):
		return "coverage-gap"
	case obs.Coverage.ZeroEligibleAfter:
		return "no-eligible-work"
	}
	return "quiet"
}

// writeObservation prints the field counts under one record: the windows, the
// recurrence, the guard activity and the identities that counted each window.
// writeFixtureReceipt prints a guard-tier record's fixture line: what this machine
// knows about the stored cases behind the installed guard. Every state carries its
// own limit, because a pass is over inputs frozen at verification time and says
// nothing about the guard being reached in the field.
func writeFixtureReceipt(w io.Writer, r FixtureReceipt) {
	if !r.Applicable {
		return
	}
	switch r.State {
	case FixtureCurrent:
		fmt.Fprintf(w, "    fixtures: current — %s passed%s; validates the stored cases only\n",
			casesWord(r.Cases), shadowedClause(r.Shadowed))
	case FixtureStale:
		fmt.Fprintf(w, "    fixtures: stale — changed: %s; retained pass: %s%s\n",
			joinOrNone(r.Changed), casesWord(r.Cases), shadowedClause(r.Shadowed))
		if r.Reason != "" {
			fmt.Fprintf(w, "      %s\n", r.Reason)
		}
	case FixtureNone:
		fmt.Fprintln(w, "    fixtures: none — no passing fixture run is retained for this guard")
	default:
		fmt.Fprintf(w, "    fixtures: unavailable — %s\n", firstNonEmpty(r.Reason, "this reading cannot reach the store"))
	}
}

// casesWord names the size of a retained pass, and says so when the manifest kept
// no count rather than printing a zero it did not read.
func casesWord(cases *int) string {
	if cases == nil {
		return "an unrecorded number of stored cases"
	}
	return fmt.Sprintf("%d stored cases", *cases)
}

// shadowedClause reports cases another guard intercepted first. A pass earned with
// shadowed cases did not establish that this guard is what blocked them.
func shadowedClause(shadowed *int) string {
	if shadowed == nil || *shadowed == 0 {
		return ""
	}
	return fmt.Sprintf("; %d shadowed by another guard", *shadowed)
}

// writeCheckActivity prints a check-tier record's execution line. Other tiers
// print nothing.
func writeCheckActivity(w io.Writer, c CheckActivity) {
	switch {
	case !c.Applicable:
	case c.Attribution != AttributionCheckID:
		fmt.Fprintln(w, "    checks: unavailable — the record binds no check")
	case c.Elsewhere():
		fmt.Fprintf(w, "    checks: unknown here — registered on host %s, whose check evidence this machine does not hold\n",
			c.RegisteredHost)
	case !c.StreamPresent:
		fmt.Fprintln(w, "    checks: unknown — no check evidence for this repository on this machine")
	default:
		fmt.Fprintf(w, "    checks: %d executions of %s on this machine, counted over %s (class-scoped=%t)\n",
			c.Executions, c.CheckID, c.Scope, c.ClassScoped)
		if c.Executions > 0 {
			fmt.Fprintf(w, "      outcomes: %s; %d blocked a turn; %d ran a changed detector\n",
				OutcomeCounts(c.Outcomes), c.Blocked, c.ChangedDetector)
		}
	}
}

// OutcomeCounts renders outcome counts in token order, such as "failed 2, passed 9".
func OutcomeCounts(outcomes map[string]int) string {
	keys := make([]string, 0, len(outcomes))
	for k := range outcomes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, outcomes[k]))
	}
	return strings.Join(parts, ", ")
}

func writeObservation(w io.Writer, obs Observation) {
	c := obs.Counts
	fmt.Fprintf(w, "    window: split %s .. read %s  unit %s\n",
		stamp(c.SplitTS), stamp(c.ObservedTS), orUnitWord(c.Unit))
	fmt.Fprintf(w, "    before %d/%d   after %d/%d\n",
		c.Before.Hits, c.Before.Eligible, c.After.Hits, c.After.Eligible)
	if obs.Recurrence.Occurred {
		fmt.Fprintf(w, "    recurrence: %d after the split (%s)\n",
			obs.Recurrence.Matches, firstNonEmpty(obs.Recurrence.ExampleKind, "no example"))
		if obs.Recurrence.Example != "" {
			fmt.Fprintf(w, "      newest: %s\n", obs.Recurrence.Example)
		}
	} else {
		fmt.Fprintln(w, "    recurrence: none after the split")
	}
	switch {
	case !obs.Firing.Applicable:
		fmt.Fprintln(w, "    firing: not applicable — this tier writes no audit rows")
	case !obs.Firing.LogPresent:
		fmt.Fprintln(w, "    firing: unknown — no guard audit log on this machine")
	default:
		fmt.Fprintf(w, "    firing: %d rows for %s, counted over %s (class-scoped=%t)\n",
			obs.Firing.Rows, obs.Firing.HookID, obs.Firing.Scope, obs.Firing.ClassScoped)
	}
	writeFixtureReceipt(w, obs.Fixtures)
	writeCheckActivity(w, obs.Checks)
	if obs.Coverage.ZeroEligibleAfter {
		fmt.Fprintln(w, "    coverage: no eligible work has arrived since the split")
	}
	if UnitsExcluded(obs) {
		fmt.Fprintf(w, "    coverage: %d before and %d after could not be counted and are left out of the counts\n",
			numInt(obs.Coverage.Before["unknown"]), numInt(obs.Coverage.After["unknown"]))
	}
	fmt.Fprintf(w, "    %s: before %s  after %s\n", obs.Provenance.Kind,
		joinOrNone(obs.Provenance.Before), joinOrNone(obs.Provenance.After))
	fmt.Fprintln(w, "    these counts describe each period; they do not establish that the fix caused a change")
}

func orUnitWord(unit string) string {
	if unit == "" {
		return "unit"
	}
	return unit
}

func joinOrNone(v []string) string {
	if len(v) == 0 {
		return "none recorded"
	}
	return strings.Join(v, ", ")
}

func rateOf(m map[string]any) float64 {
	f, _ := m["rate"].(json.Number).Float64()
	return f
}

func filterByID(records []Record, id string) []Record {
	var out []Record
	for _, r := range records {
		if str(r["id"]) == id {
			out = append(out, r)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- list / retire ----------------------------------------------------------

// List shows the recorded interventions.
func List(w io.Writer, stateDir string, asJSON bool) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	if asJSON {
		if records == nil {
			records = []Record{}
		}
		return writeJSON(w, records)
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "# no interventions recorded yet")
		return nil
	}
	for _, r := range records {
		date := str(r["installed_date"])
		if date == "" {
			date = getString(r, "rejected_date", "?")
		}
		fmt.Fprintf(w, "%-28s %-10s %-8s %s  %s\n",
			str(r["id"]), getString(r, "status", "active"), str(r["tier"]), date, str(r["class"]))
	}
	return nil
}

// Retire marks an intervention reworked or removed.
func Retire(w io.Writer, stateDir, id, reason, status string) error {
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	hit := findRecord(records, id)
	if hit == nil {
		return fmt.Errorf("no intervention with id %s", pyRepr(id))
	}
	hit["status"] = status
	hit["retired_ts"] = int(now())
	hit["retired_reason"] = reason
	if err := save(stateDir, records); err != nil {
		return err
	}
	fmt.Fprintln(w, pyDumps(hit, 2))
	return nil
}

// --- shared JSON output -----------------------------------------------------

// writeJSON emits a value as indented JSON, keeping json.Number floats verbatim. Key
// order and HTML escaping are the only differences from mine.py's dump, and the reader
// of a --json stream decodes it, so neither carries meaning.
func writeJSON(w io.Writer, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}

func nonNilRecords(records []Record) []Record {
	if records == nil {
		return []Record{}
	}
	return records
}

func nonNilMaps(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
}

// effectPoint renders the relative-risk point estimate, or "n/a" when the baseline
// carries no rate to divide by.
func effectPoint(v any) string {
	m, _ := v.(map[string]any)
	n, ok := m["estimate"].(json.Number)
	if !ok {
		return "n/a"
	}
	return n.String() + "x"
}

// effectRange renders the two bounds, with an unbounded upper bound as an infinity sign.
func effectRange(v any) string {
	m, _ := v.(map[string]any)
	lo := "0.0"
	if n, ok := m["lower"].(json.Number); ok {
		lo = n.String()
	}
	hi := "inf"
	if n, ok := m["upper"].(json.Number); ok {
		hi = n.String()
	}
	return lo + " - " + hi
}

// attributionLine names the guard's audit join, empty when the record's tier writes no
// audit rows. A silent guard is stated plainly: a range it did not cause is not its
// effect.
func attributionLine(v any) string {
	attr, ok := v.(Attribution)
	if !ok || !attr.Applicable {
		return ""
	}
	if !attr.LogPresent {
		return "attribution: no guard audit log on this machine — the firing join is unavailable"
	}
	if attr.Firings == 0 {
		return fmt.Sprintf("attribution: %s never fired in the after-window", pyRepr(attr.HookID))
	}
	return fmt.Sprintf("attribution: %s fired %d time(s) across %d after-window session(s)",
		pyRepr(attr.HookID), attr.Firings, attr.Sessions)
}

// requiredSuffix names the clean after-window a verdict still needs, when one exists.
func requiredSuffix(v any) string {
	n, ok := v.(int)
	if !ok || n == 0 {
		return ""
	}
	return fmt.Sprintf("  needs %d clean chains", n)
}

// writableCorpus resolves a --corpus flag against the registry. An empty value is
// review-findings. A corpus no engine can freeze a baseline over is refused at write
// time rather than recorded with a baseline no reader agrees with.
func writableCorpus(name string) (CorpusSpec, error) {
	if name == "" {
		name = CorpusReviewFindings
	}
	spec, ok := LookupCorpus(name)
	if !ok {
		return CorpusSpec{}, fmt.Errorf("unknown corpus %s — known: %s",
			pyRepr(name), strings.Join(Corpora(), ", "))
	}
	if !spec.Scorable {
		return CorpusSpec{}, fmt.Errorf("corpus %s counts by %s, which no scoring engine "+
			"reads; it cannot freeze a baseline", pyRepr(name), pyRepr(spec.Unit))
	}
	return spec, nil
}

// ValidateScope refuses a scope predicate this corpus's unit cannot decide. A predicate
// no unit carries would leave every unit Unknown, which reads as missing evidence
// forever rather than as the write-time mistake it is.
func (spec CorpusSpec) ValidateScope(m map[string]any) error {
	if !spec.PathScope && str(m["path"]) != "" {
		return fmt.Errorf("--scope-path cannot be decided over corpus %s: a %s carries no "+
			"changed-path list. Scope it by harness or repo instead",
			pyRepr(spec.Corpus), pyRepr(spec.Unit))
	}
	return nil
}

// ValidateSignalKind refuses a signal kind the corpus does not classify by.
func (spec CorpusSpec) ValidateSignalKind(kind string) error {
	if kind == "" {
		return nil
	}
	if spec.Engine != EngineSession {
		return fmt.Errorf("--signal-kind applies to corpus %s only", pyRepr(CorpusTranscript))
	}
	for _, k := range SignalKinds() {
		if k == kind {
			return nil
		}
	}
	return fmt.Errorf("unknown signal kind %s — known: %s",
		pyRepr(kind), strings.Join(SignalKinds(), ", "))
}
