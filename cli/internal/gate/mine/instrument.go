package mine

import (
	"sort"
	"strconv"
	"strings"
)

// InstrumentFields is one reviewer identity by dimension. It is the ledger row's
// `instrument_fields` object, or the same identity recovered from the legacy
// `instrument` string.
//
// An empty Provider, Model, PromptDigest or Engine, and a zero Runs, mean the
// dimension is unknown. Effort is different: an empty
// effort is the provider's own default, which is a value and not an absence, so
// EffortKnown carries whether the identity could be read at all.
//
// AddendumDigest is "" when the reviewer ran with no review addendum. An absent
// value reads as that, because no row written before the addendum existed ran with
// one; AddendumKnown is false only where the identity could not be read.
type InstrumentFields struct {
	Provider       string
	Model          string
	Effort         string
	EffortKnown    bool
	Runs           int
	PromptDigest   string
	Engine         string
	AddendumDigest string
	AddendumKnown  bool
	// Endpoint is the host an API reviewer sent to, "" for a CLI reviewer.
	Endpoint string
}

// ParseInstrumentID recovers the dimensions the instrument string carries:
// `model/runs/digest/engine`, plus `/effort` when an effort was set. A row written
// before `instrument_fields` existed still reads by dimension this way; only the
// provider is beyond recovery, because the string never held it.
//
// A string of any other shape is unknown in every dimension. It is not guessed at.
func ParseInstrumentID(id string) InstrumentFields {
	parts := strings.Split(id, "/")
	if len(parts) < 4 || len(parts) > 5 {
		return InstrumentFields{}
	}
	runs, err := strconv.Atoi(parts[1])
	if err != nil {
		return InstrumentFields{}
	}
	prompt, addendum, _ := strings.Cut(parts[2], "+")
	f := InstrumentFields{
		Model:          parts[0],
		Runs:           runs,
		PromptDigest:   prompt,
		Engine:         parts[3],
		EffortKnown:    true,
		AddendumDigest: addendum,
		AddendumKnown:  true,
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
		Provider:       str(m["provider"]),
		Model:          str(m["model"]),
		Effort:         str(m["effort"]),
		EffortKnown:    true,
		Runs:           numInt(m["runs"]),
		PromptDigest:   str(m["prompt_digest"]),
		Engine:         str(m["engine"]),
		AddendumDigest: str(m["addendum_digest"]),
		AddendumKnown:  true,
		Endpoint:       str(m["endpoint"]),
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
// value for both.
func (u InstrumentUse) Label() string {
	if u.Fields.Provider == "" {
		return u.ID
	}
	if u.Fields.Endpoint != "" {
		return u.ID + " (provider " + u.Fields.Provider + ", endpoint " + u.Fields.Endpoint + ")"
	}
	return u.ID + " (provider " + u.Fields.Provider + ")"
}

// InstrumentUses lists the distinct reviewer identities a window's rows carry, sorted
// so a rendered list is stable. Two rows sharing an instrument string but naming
// different providers are two identities: the string never held the provider.
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
		if uses[i].Fields.Provider != uses[j].Fields.Provider {
			return uses[i].Fields.Provider < uses[j].Fields.Provider
		}
		return uses[i].Fields.Endpoint < uses[j].Fields.Endpoint
	})
	return uses
}

// instrumentLabels names the reviewer identities a window's rows carry, each with its
// provider where the row recorded one.
func instrumentLabels(chains []Chain) []string {
	var labels []string
	for _, u := range InstrumentUses(chains) {
		labels = append(labels, u.Label())
	}
	return labels
}
