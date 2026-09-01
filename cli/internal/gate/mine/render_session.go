package mine

import (
	"fmt"
	"io"
	"regexp"
	"sort"
)

// recordsForCorpus keeps the records counted over one corpus. A prior decision from
// another corpus shares no unit with this candidate, so an overlap between them is
// arithmetic on two different denominators.
func recordsForCorpus(records []Record, corpus string) []Record {
	var out []Record
	for _, r := range records {
		if CorpusOf(r) == corpus {
			out = append(out, r)
		}
	}
	return out
}

// matchSessions is `intervention match` over the transcript-friction corpus.
func matchSessions(w io.Writer, o MatchOpts) error {
	pattern, err := compilePattern(o.Regex)
	if err != nil {
		return fmt.Errorf("bad regex %s: %w", pyRepr(o.Regex), err)
	}
	signalKind := o.SignalKind
	if signalKind == "" {
		signalKind = SignalAny
	}
	hits, total, rate := SessionIncidence(o.Sessions, pattern, signalKind)
	fmt.Fprintf(w, "# %s matches %d/%d sessions (%s), signal kind %s\n",
		pyRepr(o.Regex), hits, total, pct(rate), pyRepr(signalKind))
	shown := 0
	for _, u := range o.Sessions {
		found := SessionHits(u, pattern, signalKind)
		if len(found) == 0 {
			continue
		}
		shown++
		fmt.Fprintf(w, "--- %s  %s  session=%s  ts=%s\n",
			stamp(u.StartTS), u.Harness, u.SessionID, pyStr(u.StartTS))
		for _, sig := range found {
			fmt.Fprintf(w, "  [%s] %s\n", sig.Kind, sig.Text)
		}
	}
	if shown == 0 {
		fmt.Fprintln(w, "# no matches — the regex is too narrow, the class is misnamed, "+
			"or the snapshot store is empty (run `steer report refresh`)")
	}
	return nil
}

// SessionOverlap counts the sessions a candidate regex and a recorded class's regex
// both match.
func SessionOverlap(record Record, units []SessionUnit, pattern *regexp.Regexp, signalKind string) (shared, total int, err error) {
	prior, err := compilePattern(str(record["match"]))
	if err != nil {
		return 0, 0, err
	}
	priorKind := SessionSignalKind(record)
	for _, u := range units {
		if len(SessionHits(u, pattern, signalKind)) == 0 {
			continue
		}
		total++
		if len(SessionHits(u, prior, priorKind)) > 0 {
			shared++
		}
	}
	return shared, total, nil
}

// SessionOverlappingDecisions lists recorded transcript classes sharing sessions with a
// candidate, most overlapping first.
func SessionOverlappingDecisions(records []Record, units []SessionUnit, pattern *regexp.Regexp, signalKind string) ([]Decision, error) {
	var out []Decision
	for _, record := range recordsForCorpus(records, CorpusTranscript) {
		shared, total, err := SessionOverlap(record, units, pattern, signalKind)
		if err != nil {
			return nil, err
		}
		if shared > 0 {
			ratio := 0.0
			if total > 0 {
				ratio = float64(shared) / float64(total)
			}
			out = append(out, Decision{record, shared, total, ratio})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ratio > out[j].Ratio })
	return out, nil
}

// sessionInstrumentValue is the classifier version the newest snapshot carries, or nil
// so the record stores null when the corpus is empty.
func sessionInstrumentValue(units []SessionUnit) any {
	best := ""
	bestTS := float64(-1)
	for _, u := range units {
		if u.Instrument != "" && u.StartTS >= bestTS {
			best, bestTS = u.Instrument, u.StartTS
		}
	}
	if best == "" {
		return nil
	}
	return best
}
