package mine

import (
	"sort"
	"strconv"
	"strings"
)

// Drift grades. A major drift changes what the reviewer is; a minor one changes how
// hard it worked at the same identity.
const (
	DriftMinor = "minor"
	DriftMajor = "major"
)

// InstrumentFields is one reviewer identity by dimension. It is the ledger row's
// `instrument_fields` object, or the same identity recovered from the legacy
// `instrument` string.
//
// An empty Provider, Model, PromptDigest or Engine, and a zero Runs, mean the
// dimension is unknown and no comparison reads it. Effort is different: an empty
// effort is the provider's own default, which is a value and not an absence, so
// EffortKnown carries whether the identity could be read at all.
type InstrumentFields struct {
	Provider     string
	Model        string
	Effort       string
	EffortKnown  bool
	Runs         int
	PromptDigest string
	Engine       string
}

// ParseInstrumentID recovers the dimensions the instrument string carries:
// `model/runs/digest/engine`, plus `/effort` when an effort was set. A row written
// before `instrument_fields` existed still grades by dimension this way; only the
// provider is beyond recovery, because the string never held it.
//
// A string of any other shape is unknown in every dimension. It is not guessed at:
// half an identity grades a change on the dimensions it happens to fill and stays
// silent on the rest, which reads as a smaller drift than the one that occurred.
func ParseInstrumentID(id string) InstrumentFields {
	parts := strings.Split(id, "/")
	if len(parts) < 4 || len(parts) > 5 {
		return InstrumentFields{}
	}
	runs, err := strconv.Atoi(parts[1])
	if err != nil {
		return InstrumentFields{}
	}
	f := InstrumentFields{
		Model:        parts[0],
		Runs:         runs,
		PromptDigest: parts[2],
		Engine:       parts[3],
		EffortKnown:  true,
	}
	if len(parts) == 5 {
		f.Effort = parts[4]
	}
	return f
}

// InstrumentFieldsFrom reads a row's `instrument_fields` object. ok is false when the
// row carries none, which sends the caller to the string.
func InstrumentFieldsFrom(raw any) (InstrumentFields, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return InstrumentFields{}, false
	}
	return InstrumentFields{
		Provider:     str(m["provider"]),
		Model:        str(m["model"]),
		Effort:       str(m["effort"]),
		EffortKnown:  true,
		Runs:         numInt(m["runs"]),
		PromptDigest: str(m["prompt_digest"]),
		Engine:       str(m["engine"]),
	}, true
}

// InstrumentUse is one reviewer identity as a window recorded it: the string the row
// stamped, and the dimensions behind it.
type InstrumentUse struct {
	ID     string
	Fields InstrumentFields
}

// Label names the identity for a reader. It is the instrument string, plus the
// provider when the row recorded one: two windows can share a string and name
// different providers, and a list that printed the string alone would show the same
// value on both sides of the drift it is reporting.
func (u InstrumentUse) Label() string {
	if u.Fields.Provider == "" {
		return u.ID
	}
	return u.ID + " (provider " + u.Fields.Provider + ")"
}

// InstrumentUses lists the distinct reviewer identities a window's rows carry, sorted
// so a rendered list is stable. Two rows sharing an instrument string but naming
// different providers are two identities: the string never held the provider, so a
// provider swap at one model name is invisible to a comparison of strings alone.
func InstrumentUses(windows ...[]Chain) []InstrumentUse {
	var uses []InstrumentUse
	seen := map[InstrumentUse]bool{}
	for _, chains := range windows {
		for _, ch := range chains {
			for _, row := range ch.Rows {
				id := str(row["instrument"])
				if id == "" {
					continue
				}
				fields, ok := InstrumentFieldsFrom(row["instrument_fields"])
				if !ok {
					fields = ParseInstrumentID(id)
				}
				u := InstrumentUse{ID: id, Fields: fields}
				if !seen[u] {
					seen[u] = true
					uses = append(uses, u)
				}
			}
		}
	}
	sort.Slice(uses, func(i, j int) bool {
		if uses[i].ID != uses[j].ID {
			return uses[i].ID < uses[j].ID
		}
		return uses[i].Fields.Provider < uses[j].Fields.Provider
	})
	return uses
}

// BaselineInstrument recovers the dimensions of the record's registered instrument.
// The record stores the string alone, so the BEFORE window is asked first: a row that
// stamped that same string also stamped the provider the string cannot hold.
//
// Only the before window may answer. Two windows can stamp one instrument string over
// different providers — that is the case the string cannot express — so an after-window
// row admitted here would supply the baseline its own provider and grade the swap
// against itself. Where the before window never stamped the string, the string is
// parsed and the provider stays unknown, which grades that dimension not at all rather
// than grading it equal.
func BaselineInstrument(authored string, uses []InstrumentUse) InstrumentFields {
	parsed := ParseInstrumentID(authored)
	var seen []InstrumentFields
	for _, u := range uses {
		if u.ID == authored {
			seen = append(seen, u.Fields)
		}
	}
	if len(seen) == 0 {
		return parsed
	}
	return foldInstruments(parsed, seen)
}

// foldInstruments reduces the identities the before window stamped on one instrument
// string to a single baseline, dimension by dimension. Where the rows know a value and
// agree, that value wins. Where they disagree, the dimension is UNKNOWN, and grading
// skips it: a baseline two providers reviewed is not one identity, and grading an after
// window against either of them would report a swap that did not happen. Where no row
// knows it, the string's own parse answers.
//
// The rows are read as a set rather than folded pairwise. A pairwise fold writes a
// disagreement as the same empty value it writes an absence as, so a third row would
// read that emptiness as "not yet known" and fill the dimension back in.
//
// The parsed string is the fallback only, never one more voice. The string is derived
// from the fields, so a row that stamped it agrees with it by construction; letting it
// vote would let the record's stored string contradict the rows that produced it.
func foldInstruments(parsed InstrumentFields, seen []InstrumentFields) InstrumentFields {
	agreedString := func(get func(InstrumentFields) string, fallback string) string {
		found := ""
		for _, f := range seen {
			v := get(f)
			switch {
			case v == "":
			case found == "":
				found = v
			case found != v:
				return ""
			}
		}
		if found == "" {
			return fallback
		}
		return found
	}
	out := InstrumentFields{
		Provider:     agreedString(func(f InstrumentFields) string { return f.Provider }, parsed.Provider),
		Model:        agreedString(func(f InstrumentFields) string { return f.Model }, parsed.Model),
		PromptDigest: agreedString(func(f InstrumentFields) string { return f.PromptDigest }, parsed.PromptDigest),
		Engine:       agreedString(func(f InstrumentFields) string { return f.Engine }, parsed.Engine),
	}
	runs := 0
	for _, f := range seen {
		switch {
		case f.Runs == 0:
		case runs == 0:
			runs = f.Runs
		case runs != f.Runs:
			runs = -1
		}
		if runs == -1 {
			break
		}
	}
	switch runs {
	case 0:
		out.Runs = parsed.Runs
	case -1:
		out.Runs = 0
	default:
		out.Runs = runs
	}
	out.Effort, out.EffortKnown = parsed.Effort, parsed.EffortKnown
	known := false
	for _, f := range seen {
		if !f.EffortKnown {
			continue
		}
		switch {
		case !known:
			known, out.Effort, out.EffortKnown = true, f.Effort, true
		case f.Effort != out.Effort:
			out.Effort, out.EffortKnown = "", false
			return out
		}
	}
	return out
}

// driftDimension is one graded dimension of the reviewer identity.
type driftDimension struct {
	name  string
	major bool
	// changed reports whether the dimension is known on both sides and differs.
	changed func(a, b InstrumentFields) bool
}

// driftDimensions grades provider and engine beside model and prompt digest: a
// provider swap at an identical model name, or an engine swap at an identical prompt,
// changes what did the reviewing and is otherwise invisible.
var driftDimensions = []driftDimension{
	{"provider", true, func(a, b InstrumentFields) bool {
		return a.Provider != "" && b.Provider != "" && a.Provider != b.Provider
	}},
	{"model", true, func(a, b InstrumentFields) bool {
		return a.Model != "" && b.Model != "" && a.Model != b.Model
	}},
	{"prompt digest", true, func(a, b InstrumentFields) bool {
		return a.PromptDigest != "" && b.PromptDigest != "" && a.PromptDigest != b.PromptDigest
	}},
	{"engine", true, func(a, b InstrumentFields) bool {
		return a.Engine != "" && b.Engine != "" && a.Engine != b.Engine
	}},
	{"effort", false, func(a, b InstrumentFields) bool {
		return a.EffortKnown && b.EffortKnown && a.Effort != b.Effort
	}},
	{"runs", false, func(a, b InstrumentFields) bool {
		return a.Runs != 0 && b.Runs != 0 && a.Runs != b.Runs
	}},
}

// DriftDims names the dimensions that changed between two identities, in the fixed
// order above. A dimension unknown on either side is not named: it says nothing about
// whether the reviewer moved.
func DriftDims(base, other InstrumentFields) []string {
	var names []string
	for _, d := range driftDimensions {
		if d.changed(base, other) {
			names = append(names, d.name)
		}
	}
	return names
}

// DriftGrade grades a change between two identities: major when a dimension that says
// what the reviewer IS changed, minor when only effort or run count moved, and empty
// when nothing comparable differs.
func DriftGrade(base, other InstrumentFields) string {
	grade := ""
	for _, d := range driftDimensions {
		if !d.changed(base, other) {
			continue
		}
		if d.major {
			return DriftMajor
		}
		grade = DriftMinor
	}
	return grade
}

// gradeDrift is DriftGrade over a pair whose strings are also known. Two identities
// whose strings differ while no dimension is comparable grade major: an uncharacterized
// change is a change, and demoting it would rest on the parse that just failed.
func gradeDrift(baseID string, base InstrumentFields, use InstrumentUse) string {
	if g := DriftGrade(base, use.Fields); g != "" {
		return g
	}
	if baseID != use.ID {
		return DriftMajor
	}
	return ""
}

// InstrumentImpact is one live record a prospective reviewer identity would drift.
type InstrumentImpact struct {
	ID    string
	Class string
	Grade string
	// Dims names the dimensions that moved, empty when the identities differ in a
	// way no comparable dimension explains.
	Dims []string
}

// ImpactOf grades a prospective reviewer identity against every record the reviewer
// is measured over, so an operator editing the settings is told which measurements
// the edit lands in before it is written.
//
// Each record's baseline comes from BaselineInstrument over its before-window, and
// the grade from the dimension table above. The field scorer makes no efficacy claim
// and grades no drift, so this warning is the one place a reviewer change is named:
// at the moment of action, before the edit is written.
//
// Only the chain corpus is read. The session corpus stamps the transcript parser as
// its instrument, which no reviewer setting moves.
func ImpactOf(records []Record, chains []Chain, next InstrumentUse) []InstrumentImpact {
	var out []InstrumentImpact
	for _, record := range records {
		if EngineOf(record) != EngineChain || truthy(record["retired_ts"]) {
			continue
		}
		authored := str(record["instrument"])
		if authored == "" {
			continue
		}
		before, _ := SplitChains(chains, SplitTS(record))
		base := BaselineInstrument(authored, InstrumentUses(before))
		grade := gradeDrift(authored, base, next)
		if grade == "" {
			continue
		}
		out = append(out, InstrumentImpact{
			ID:    str(record["id"]),
			Class: str(record["class"]),
			Grade: grade,
			Dims:  DriftDims(base, next.Fields),
		})
	}
	return out
}
