package mine

import "regexp"

// compilePattern compiles a class regex case-insensitively, searching anywhere. RE2
// rejects the lookaround and backreferences Python's `re` accepts; its compile error
// names the unsupported construct, which the calling command wraps for the user.
func compilePattern(expr string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + expr)
}

// KindFilter is the verdict a finding must carry for the requested kind.
func KindFilter(verdict, kind string) bool {
	switch kind {
	case "accepted":
		return verdict == "ACCEPTED"
	case "rejected":
		return verdict == "REJECTED"
	default: // "both"
		return true
	}
}

// ChainHits are the findings in one chain matching the pattern and the verdict.
func ChainHits(ch Chain, pattern *regexp.Regexp, kind string) []Finding {
	var out []Finding
	for _, f := range ch.Findings {
		if KindFilter(f.Verdict, kind) && pattern.MatchString(f.Text) {
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
