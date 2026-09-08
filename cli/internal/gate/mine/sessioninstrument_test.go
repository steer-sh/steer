package mine

import "testing"

func unit(harness, instrument string, ts float64) SessionUnit {
	return SessionUnit{Harness: harness, Instrument: instrument, StartTS: ts}
}

// A window holding two harnesses registers both adapters, so the label names the
// whole pipeline the baseline was counted over.
func TestARegisteredInstrumentNamesEveryHarnessInTheWindow(t *testing.T) {
	units := []SessionUnit{
		unit("claude-code", "scan-aaa/claude-code:1111", 10),
		unit("codex", "scan-aaa/codex:2222", 20),
	}
	if got := RegisteredSessionInstrument(units); got != "scan-aaa/claude-code:1111+codex:2222" {
		t.Fatalf("registered instrument = %q; want both adapters", got)
	}
}

// The newest unit supplies each dimension, and a unit stamped before the adapter
// was named contributes only its parser.
func TestARegisteredInstrumentTakesTheNewestOfEachDimension(t *testing.T) {
	units := []SessionUnit{
		unit("codex", "scan-aaa/codex:1111", 10),
		unit("codex", "scan-bbb/codex:2222", 20),
	}
	if got := RegisteredSessionInstrument(units); got != "scan-bbb/codex:2222" {
		t.Fatalf("registered instrument = %q; want the newest parser and adapter", got)
	}
	if got := RegisteredSessionInstrument([]SessionUnit{unit("codex", "scan-aaa", 10)}); got != "scan-aaa" {
		t.Fatalf("registered instrument = %q; want the bare parser", got)
	}
	if got := RegisteredSessionInstrument(nil); got != "" {
		t.Fatalf("an unstamped window names no instrument, got %q", got)
	}
}

func TestComposeAndParseRoundTrip(t *testing.T) {
	if got := ComposeSessionInstrument("scan-aaa", "pi:3333"); got != "scan-aaa/pi:3333" {
		t.Fatalf("compose = %q", got)
	}
	// An adapter naming no instrument leaves the legacy bare form.
	if got := ComposeSessionInstrument("scan-aaa", ""); got != "scan-aaa" {
		t.Fatalf("compose with no adapter = %q; want the bare parser", got)
	}
	inst := ParseSessionInstrument("scan-aaa/claude-code:1111+codex:2222")
	if inst.Parser != "scan-aaa" || inst.Adapters["claude-code"] != "1111" || inst.Adapters["codex"] != "2222" {
		t.Fatalf("parse = %+v", inst)
	}
}
