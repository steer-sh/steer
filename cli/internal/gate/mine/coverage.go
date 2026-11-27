package mine

import (
	"regexp"
	"sort"
)

// Deciding is one live class decision, compiled, that may cover a finding in the
// review corpus. Kind is the record's normalized verdict filter: the verdict a
// finding must carry for this class to speak about it. Active marks an installed
// fix, and SplitTS is where its windows split (SplitTS).
type Deciding struct {
	ID      string
	Kind    string
	Pattern *regexp.Regexp
	Active  bool
	SplitTS float64
}

// DecidingClasses compiles the records that may cover a review finding: a live
// status, the review-findings corpus over chains, and a normalized kind of
// accepted or rejected. A record whose regex does not compile is skipped.
func DecidingClasses(records []Record) []Deciding {
	var out []Deciding
	for _, r := range records {
		status := str(r["status"])
		if status != "active" && status != "rejected" {
			continue
		}
		if CorpusOf(r) != CorpusReviewFindings || UnitOf(r) != UnitChain {
			continue
		}
		kind := RecordKindFilter(r)
		if kind != "accepted" && kind != "rejected" {
			continue
		}
		pattern, err := compilePattern(str(r["match"]))
		if err != nil {
			continue
		}
		out = append(out, Deciding{ID: str(r["id"]), Kind: kind, Pattern: pattern,
			Active: status == "active", SplitTS: SplitTS(r)})
	}
	return out
}

// FindingsOfKind are the chain's findings carrying the requested verdict.
func FindingsOfKind(ch Chain, kind string) []Finding {
	return ChainHits(ch, matchEveryFinding, kind)
}

var matchEveryFinding = regexp.MustCompile("")

// CoveringIDs are the ids of the classes that cover one finding, sorted so the
// same finding reads the same way on every run. A class covers a finding when the
// finding carries the verdict the class was authored against and the class regex
// matches its text. A class authored against accepted findings therefore cannot
// cover a rejected one, whatever kind the reader asked to see.
func CoveringIDs(f Finding, deciding []Deciding) []string {
	covering, _ := ChainCoverage(Chain{}, f, deciding)
	return covering
}

// ChainCoverage splits the classes matching one finding of ch, each list sorted.
// covering decides the finding as CoveringIDs does. caughtAfter holds the active
// classes a branch review chain matched at or after their split, as Score splits a
// window: no score reads a branch chain, so that finding stays shown as evidence
// about those fixes. A rejected class covers a branch finding at any time.
func ChainCoverage(ch Chain, f Finding, deciding []Deciding) (covering, caughtAfter []string) {
	for _, d := range deciding {
		if !KindFilter(f, d.Kind) || !d.Pattern.MatchString(f.Text) {
			continue
		}
		if ch.Channel == ChannelBranch && d.Active && numFloat(ch.StartTS) >= d.SplitTS {
			caughtAfter = append(caughtAfter, d.ID)
			continue
		}
		covering = append(covering, d.ID)
	}
	sort.Strings(covering)
	sort.Strings(caughtAfter)
	return covering, caughtAfter
}

// HasUncovered reports whether any finding is covered by no class. One covered
// finding does not decide the others: a residual unclassified finding is the
// evidence a reader still has to cluster.
func HasUncovered(findings []Finding, deciding []Deciding) bool {
	for _, f := range findings {
		if len(CoveringIDs(f, deciding)) == 0 {
			return true
		}
	}
	return false
}
