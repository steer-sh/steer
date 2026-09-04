package mine

import (
	"sort"
	"strings"
)

// A session instrument names the classifier and the adapter that produced a
// session's signals, as "<parser>/<harness>:<digest>[+<harness>:<digest>…]". A
// unit carries one harness; a record's registered instrument carries every
// harness its baseline window held.
const (
	sessionInstrumentSep     = "/"
	sessionAdapterSep        = "+"
	sessionAdapterHarnessCut = ":"
)

// SessionInstrument is a session instrument by dimension. A dimension absent on
// one side of a comparison is not graded, which is what keeps a record
// registered before adapters were named from reading as drift.
type SessionInstrument struct {
	// Parser is the classifier digest.
	Parser string
	// Adapters maps a harness id to that adapter's digest.
	Adapters map[string]string
}

// ComposeSessionInstrument joins a classifier digest to the adapter that
// normalized this unit. An adapter that names no instrument yields the bare
// parser, which is the form every record written before this carried.
func ComposeSessionInstrument(parser, adapter string) string {
	if parser == "" || adapter == "" {
		return parser
	}
	return parser + sessionInstrumentSep + adapter
}

// ParseSessionInstrument reads a session instrument's dimensions. A string
// carrying no adapter part parses as a parser with no adapter known.
func ParseSessionInstrument(s string) SessionInstrument {
	inst := SessionInstrument{Adapters: map[string]string{}}
	parser, rest, _ := strings.Cut(s, sessionInstrumentSep)
	inst.Parser = parser
	for _, part := range strings.Split(rest, sessionAdapterSep) {
		harness, digest, ok := strings.Cut(part, sessionAdapterHarnessCut)
		if ok && harness != "" {
			inst.Adapters[harness] = digest
		}
	}
	return inst
}

// SessionInstrumentDiffers reports whether a unit's instrument names a different
// pipeline than the record's registered one. It compares dimension by dimension:
// a parser both sides name, and an adapter both sides name for the same harness.
// Two units of different harnesses therefore do not differ by virtue of running
// on different harnesses, which is what makes a record spanning both scorable.
func SessionInstrumentDiffers(unit, authored string) bool {
	if unit == "" || authored == "" || unit == authored {
		return false
	}
	u, a := ParseSessionInstrument(unit), ParseSessionInstrument(authored)
	if u.Parser != "" && a.Parser != "" && u.Parser != a.Parser {
		return true
	}
	for harness, digest := range u.Adapters {
		if other, ok := a.Adapters[harness]; ok && other != digest {
			return true
		}
	}
	return false
}

// RegisteredSessionInstrument is the instrument a record freezes over its
// baseline window: the newest parser the window carries, and the newest adapter
// per harness. A window holding two harnesses registers both, so a later change
// to either one is graded.
func RegisteredSessionInstrument(units []SessionUnit) string {
	parser, parserTS := "", float64(-1)
	adapters := map[string]string{}
	adapterTS := map[string]float64{}
	for _, u := range units {
		if u.Instrument == "" {
			continue
		}
		inst := ParseSessionInstrument(u.Instrument)
		if inst.Parser != "" && u.StartTS >= parserTS {
			parser, parserTS = inst.Parser, u.StartTS
		}
		for harness, digest := range inst.Adapters {
			if ts, seen := adapterTS[harness]; !seen || u.StartTS >= ts {
				adapters[harness], adapterTS[harness] = digest, u.StartTS
			}
		}
	}
	if parser == "" {
		return ""
	}
	names := make([]string, 0, len(adapters))
	for harness := range adapters {
		names = append(names, harness)
	}
	sort.Strings(names)
	var parts []string
	for _, harness := range names {
		parts = append(parts, harness+sessionAdapterHarnessCut+adapters[harness])
	}
	if len(parts) == 0 {
		return parser
	}
	return parser + sessionInstrumentSep + strings.Join(parts, sessionAdapterSep)
}

// driftingSessionInstruments is the distinct unit instruments that name a
// different pipeline than authored, in first-seen order.
func driftingSessionInstruments(units []SessionUnit, authored string) []string {
	var drifted []string
	for _, u := range units {
		if SessionInstrumentDiffers(u.Instrument, authored) {
			drifted = appendUnique(drifted, u.Instrument)
		}
	}
	return drifted
}
