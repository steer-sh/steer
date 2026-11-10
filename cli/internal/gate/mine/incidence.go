package mine

import (
	"regexp"
	"strings"
)

// compilePattern compiles a class regex case-insensitively, searching anywhere. RE2
// rejects lookaround and backreferences; its compile error names the unsupported
// construct, which the calling command wraps for the user.
func compilePattern(expr string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + expr)
}

// Deferred reports whether the author accepted a finding but left it unrepaired:
// verdict ACCEPTED with a qualifier containing "not fixed".
func Deferred(f Finding) bool {
	return f.Verdict == "ACCEPTED" && strings.Contains(strings.ToLower(f.Qualifier), "not fixed")
}

// KindFilter reports whether a finding belongs to the requested kind. accepted is
// every ACCEPTED finding; repaired and deferred split it by Deferred.
func KindFilter(f Finding, kind string) bool {
	switch kind {
	case "accepted":
		return f.Verdict == "ACCEPTED"
	case "repaired":
		return f.Verdict == "ACCEPTED" && !Deferred(f)
	case "deferred":
		return Deferred(f)
	case "rejected":
		return f.Verdict == "REJECTED"
	default: // "both"
		return true
	}
}

// kindVerdict is the verdict filter a findings kind narrows; repaired and deferred
// are subsets of accepted.
func kindVerdict(kind string) string {
	if kind == "repaired" || kind == "deferred" {
		return "accepted"
	}
	return kind
}

// ChainHits are the findings in one chain matching the pattern and the verdict.
func ChainHits(ch Chain, pattern *regexp.Regexp, kind string) []Finding {
	var out []Finding
	for _, f := range ch.Findings {
		if KindFilter(f, kind) && pattern.MatchString(f.Text) {
			out = append(out, f)
		}
	}
	return out
}

// HitSequence is one bool per chain, in the order the chains occurred: whether the class
// occurred on that chain. The confidence sequence reads it prefix by prefix.
func HitSequence(chains []Chain, pattern *regexp.Regexp, kind string) []bool {
	seq := make([]bool, len(chains))
	for i, ch := range chains {
		seq[i] = len(ChainHits(ch, pattern, kind)) > 0
	}
	return seq
}

// Incidence is chain-level: a class either occurred on a commit or it did not. Three
// reviewers reporting one defect is one occurrence, and the reviewer count varies.
func Incidence(chains []Chain, pattern *regexp.Regexp, kind string) (hits, total int, rate float64) {
	for _, hit := range HitSequence(chains, pattern, kind) {
		if hit {
			hits++
		}
	}
	total = len(chains)
	if total > 0 {
		rate = float64(hits) / float64(total)
	}
	return hits, total, rate
}

// SplitChains partitions chains into those starting before ts and those at or after.
func SplitChains(chains []Chain, ts float64) (before, after []Chain) {
	for _, ch := range chains {
		if numFloat(ch.StartTS) < ts {
			before = append(before, ch)
		} else {
			after = append(after, ch)
		}
	}
	return before, after
}

// CurrentInstrument is the reviewer that produced the most recent row, or "" on an
// unstamped log. Taken from the ledger so the recorded label is one the ledger uses.
func CurrentInstrument(chains []Chain) string {
	for i := len(chains) - 1; i >= 0; i-- {
		if n := len(chains[i].Instruments); n > 0 {
			return chains[i].Instruments[n-1]
		}
	}
	return ""
}

// HostsIn lists the hosts of a window's chains, in first-seen order.
func HostsIn(chains []Chain) []string {
	var seen []string
	for _, ch := range chains {
		host := ch.Host
		if host == "" {
			host = "unknown"
		}
		seen = appendUnique(seen, host)
	}
	return seen
}

// InstrumentsIn lists the instruments of a window's chains, in first-seen order.
func InstrumentsIn(chains []Chain) []string {
	var seen []string
	for _, ch := range chains {
		for _, i := range ch.Instruments {
			seen = appendUnique(seen, i)
		}
	}
	return seen
}
