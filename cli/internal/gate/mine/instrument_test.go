package mine

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func TestParseInstrumentIDRecoversTheDimensions(t *testing.T) {
	four := ParseInstrumentID("gemini-3.7-flash/3/aaaa1111/go")
	check(t, "model", four.Model, "gemini-3.7-flash")
	check(t, "runs", four.Runs, 3)
	check(t, "prompt digest", four.PromptDigest, "aaaa1111")
	check(t, "engine", four.Engine, "go")
	check(t, "an unset effort is a known value", four.EffortKnown, true)
	check(t, "the string never held the provider", four.Provider, "")

	five := ParseInstrumentID("gemini-3.7-flash/3/aaaa1111/go/high")
	check(t, "the fifth segment is the effort", five.Effort, "high")

	for _, id := range []string{"modelA/3/aaaa1111", "a/b/c/d", "a/1/b/c/d/e", ""} {
		check(t, "an unreadable id is unknown everywhere: "+id, ParseInstrumentID(id), InstrumentFields{})
	}
}

// Two rows that share an instrument string but name different providers are two
// identities, and each label names its provider.
//
// pins existing behaviour
func TestInstrumentUsesKeepsAProviderSwapAtOneString(t *testing.T) {
	const id = "sonnet/3/abc/go"
	row := func(provider string) gate.Row {
		return gate.Row{"instrument": id, "instrument_fields": map[string]any{
			"provider": provider, "model": "sonnet", "runs": 3, "prompt_digest": "abc", "engine": "go",
		}}
	}
	legacy := gate.Row{"instrument": id}
	chains := []Chain{{Rows: []gate.Row{row("codex"), row("agy"), row("agy"), legacy}}}

	var labels []string
	for _, u := range InstrumentUses(chains) {
		labels = append(labels, u.Label())
	}
	check(t, "labels", labels, []string{id, id + " (provider agy)", id + " (provider codex)"})
}
