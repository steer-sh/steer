package mine

import (
	"fmt"
	"io"
	"strings"
)

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
// emits exactly the JSON it emitted before the view existed. CaughtAfter is the
// active classes a branch review matched after their split (ChainCoverage): the
// finding stays shown, as evidence about those fixes rather than a new candidate.
type RenderedFinding struct {
	Finding
	CoveredBy   []string `json:"covered_by,omitempty"`
	CaughtAfter []string `json:"caught_after,omitempty"`
}

// Findings emits adjudicated findings grouped by chain, for a reader to cluster.
func Findings(out io.Writer, stateDir string, o FindingsOpts) error {
	ew := &errWriter{w: out}
	var w io.Writer = ew
	rows, err := LoadReviewRows(stateDir, o.Ledger)
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
	var read map[string]bool
	readRecorded := false
	if o.Undecided {
		deciding = DecidingClasses(records)
		read, readRecorded = loadKeySets(FindingsReadPath(stateDir))[o.Kind]
	}
	shownKeys := map[string]bool{}
	var isNew []bool

	var payload []map[string]any
	var shown []Chain
	selected, covered, residue, carrying, residueChains := 0, 0, 0, 0, 0
	deferred, shownDeferred, caught := 0, 0, 0
	for _, ch := range chains {
		findings := renderFindings(ch, filterFindings(ch.Findings, o.Kind), deciding, o.Undecided)
		selected += len(findings)
		chainDeferred := 0
		for _, f := range findings {
			if Deferred(f.Finding) {
				chainDeferred++
			}
		}
		deferred += chainDeferred
		if len(findings) > 0 {
			carrying++
		}
		chainResidue := 0
		chainNew := false
		for _, f := range findings {
			if len(f.CoveredBy) > 0 {
				covered++
				continue
			}
			chainResidue++
			if len(f.CaughtAfter) > 0 {
				caught++
			}
			if o.Undecided {
				key := FindingKey(ch, f.Finding)
				shownKeys[key] = true
				chainNew = chainNew || !read[key]
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
		if ch.Channel == ChannelBranch {
			entry["channel"] = ch.Channel
		}
		if o.Undecided {
			entry["new"] = chainNew
		}
		shownDeferred += chainDeferred
		shown = append(shown, ch)
		payload = append(payload, entry)
		isNew = append(isNew, chainNew)
	}
	reviewed := len(chains)
	newChains := 0
	if o.Undecided {
		shown, payload, newChains = newFirst(shown, payload, isNew)
	}
	// Only a whole-pile dump that reached its reader in full is a reading of
	// every residual finding; any other leaves the pending set as it was.
	record := func() error {
		if !o.Undecided || o.Since != 0 || o.StrictJoins || o.Ledger != "" || ew.err != nil {
			return ew.err
		}
		return recordShown(stateDir, o.Kind, shownKeys)
	}

	if o.JSON {
		doc := map[string]any{
			"chains_reviewed": reviewed,
			"decisions":       nonNilRecords(records),
			"chains":          nonNilMaps(payload),
		}
		if o.Undecided {
			doc["selected_findings"] = selected
			doc["covered_findings"] = covered
			doc["residual_findings"] = residue
			doc["residual_chains"] = residueChains
			doc["new_chains"] = newChains
		}
		if err := writeJSON(w, doc); err != nil {
			return err
		}
		return record()
	}

	if len(records) > 0 {
		fmt.Fprintf(w, "# %d class(es) already decided on this corpus:\n", len(records))
		decides := map[string]bool{}
		for _, d := range deciding {
			decides[d.ID] = o.Kind == "both" || d.Kind == kindVerdict(o.Kind)
		}
		// A class's incidence is read over commit chains, the corpus its score reads.
		commitChains := make([]Chain, 0, len(chains))
		for _, ch := range chains {
			if ch.Channel != ChannelBranch {
				commitChains = append(commitChains, ch)
			}
		}
		for _, r := range records {
			printDecisionIncidence(w, r, commitChains)
			if o.Undecided && !decides[str(r["id"])] {
				fmt.Fprintln(w, "#     decides none of the findings below: retired, another corpus, or another verdict")
			}
		}
		fmt.Fprintln(w, "#")
	}
	// Every header count describes the whole time-filtered corpus. --undecided
	// narrows what is printed below, and says so on its own line, so no line
	// reports the filtered subset under a name that means the corpus.
	if o.Undecided {
		fmt.Fprintf(w, "# %d chains reviewed, %d carrying %s findings\n", reviewed, carrying, o.Kind)
		fmt.Fprintf(w, "# %d distinct findings%s\n", selected, deferredShare(deferred))
		fmt.Fprintf(w, "# --undecided: %d covered, %d remaining over %d chain(s) — those %d chains are what is printed below\n",
			covered, residue, residueChains, len(payload))
		fmt.Fprintln(w, "# a covered finding is shown in place, marked [covered-by=...]; quote the text without the marker")
		if caught > 0 {
			fmt.Fprintf(w, "# %d branch review finding(s) an active class matched after its install are marked [caught-after=...]: evidence about that fix, not a new candidate\n", caught)
		}
		if readRecorded {
			fmt.Fprintf(w, "# %d chain(s) carry a finding no earlier pass read and come first under %q; the other %d follow under %q\n",
				newChains, newHeading, len(payload)-newChains, readHeading)
		} else {
			fmt.Fprintf(w, "# no earlier pass recorded a read: all %d chain(s) are under %q\n", len(payload), newHeading)
		}
	} else {
		fmt.Fprintf(w, "# %d chains reviewed, %d carrying %s findings\n", reviewed, len(payload), o.Kind)
		distinct := 0
		for _, c := range payload {
			distinct += len(c["findings"].([]RenderedFinding))
		}
		fmt.Fprintf(w, "# %d distinct findings%s\n", distinct, deferredShare(shownDeferred))
	}
	fmt.Fprintln(w)
	labels := repoLabels(shown)
	if o.Undecided {
		fmt.Fprintf(w, "%s: %d chain(s)\n\n", newHeading, newChains)
	}
	for i, c := range payload {
		if o.Undecided && i == newChains {
			fmt.Fprintf(w, "%s: %d chain(s)\n\n", readHeading, len(payload)-newChains)
		}
		channel := ""
		if c["channel"] == ChannelBranch {
			channel = "  channel=" + ChannelBranch
		}
		fmt.Fprintf(w, "--- %s  ts=%s  repo=%s  rounds=%d  files=%d  end=%s%s\n",
			c["date"], pyStr(c["start_ts"]), labels[i], c["rounds"], c["files"], pyStr(c["terminal"]), channel)
		for _, f := range c["findings"].([]RenderedFinding) {
			verdict := verdictLabel(f.Finding)
			switch {
			case len(f.CoveredBy) > 0:
				fmt.Fprintf(w, "  [%s] [covered-by=%s] %s\n", verdict, strings.Join(f.CoveredBy, ","), f.Text)
			case len(f.CaughtAfter) > 0:
				fmt.Fprintf(w, "  [%s] [caught-after=%s] %s\n", verdict, strings.Join(f.CaughtAfter, ","), f.Text)
			default:
				fmt.Fprintf(w, "  [%s] %s\n", verdict, f.Text)
			}
		}
		fmt.Fprintln(w)
	}
	if o.Undecided && newChains == len(payload) {
		fmt.Fprintf(w, "%s: 0 chain(s)\n", readHeading)
	}
	return record()
}

// deferredMarker is the verdict label of every deferred finding in text output, in
// the ledger template's form whatever qualifier the author wrote.
const deferredMarker = "ACCEPTED, not fixed"

// verdictLabel is the bracketed verdict a text listing prints for a finding.
func verdictLabel(f Finding) string {
	if Deferred(f) {
		return deferredMarker
	}
	return f.Verdict
}

// deferredShare is the header suffix naming how many findings are deferred, or ""
// when none are.
func deferredShare(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d of them %s", n, deferredMarker)
}

// errWriter keeps the first write error, so a dump can tell whether its reader
// received all of it.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}

// The section headings of the undecided dump. A reader finds the read-before
// section by its heading, so the wording is part of the dump's contract.
const (
	newHeading  = "=== new since the last read"
	readHeading = "=== read before"
)

// newFirst moves the chains flagged new ahead of the rest, keeping each group's
// order, and returns how many are new.
func newFirst(chains []Chain, payload []map[string]any, isNew []bool) ([]Chain, []map[string]any, int) {
	var nc, rc []Chain
	var np, rp []map[string]any
	for i := range payload {
		if isNew[i] {
			nc, np = append(nc, chains[i]), append(np, payload[i])
		} else {
			rc, rp = append(rc, chains[i]), append(rp, payload[i])
		}
	}
	return append(nc, rc...), append(np, rp...), len(np)
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

// renderFindings pairs each finding of ch with the classes covering it and those it
// was caught after. Outside the undecided view nothing is consulted, so no finding
// carries a marker.
func renderFindings(ch Chain, findings []Finding, deciding []Deciding, undecided bool) []RenderedFinding {
	out := make([]RenderedFinding, 0, len(findings))
	for _, f := range findings {
		r := RenderedFinding{Finding: f}
		if undecided {
			r.CoveredBy, r.CaughtAfter = ChainCoverage(ch, f, deciding)
		}
		out = append(out, r)
	}
	return out
}

func filterFindings(findings []Finding, kind string) []Finding {
	out := []Finding{}
	for _, f := range findings {
		if KindFilter(f, kind) {
			out = append(out, f)
		}
	}
	return out
}
