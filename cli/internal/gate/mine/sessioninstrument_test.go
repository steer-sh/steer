package mine

import "testing"

func unit(harness, instrument string, ts float64) SessionUnit {
	return SessionUnit{Harness: harness, Instrument: instrument, StartTS: ts}
}

// A record registered before adapters were named carries a bare parser digest.
// The adapter dimension is then unknown on one side, so it is not graded.
func TestLegacyInstrumentDoesNotDriftAgainstAComposedUnit(t *testing.T) {
	if SessionInstrumentDiffers("scan-aaa/codex:1111", "scan-aaa") {
		t.Error("a composed unit drifted against a bare parser baseline")
	}
	if SessionInstrumentDiffers("scan-aaa", "scan-aaa/codex:1111") {
		t.Error("a bare parser unit drifted against a composed baseline")
	}
}

// Two harnesses in one window are two adapters, not a drift: the record
// registers both, and each unit is compared against its own harness's entry.
func TestOneWindowOverTwoHarnessesDoesNotDrift(t *testing.T) {
	units := []SessionUnit{
		unit("claude-code", "scan-aaa/claude-code:1111", 10),
		unit("codex", "scan-aaa/codex:2222", 20),
	}
	authored := RegisteredSessionInstrument(units)
	if authored != "scan-aaa/claude-code:1111+codex:2222" {
		t.Fatalf("registered instrument = %q; want both adapters", authored)
	}
	if drifted := driftingSessionInstruments(units, authored); len(drifted) > 0 {
		t.Errorf("drift = %v; want none", drifted)
	}
}

// An adapter change drifts only the harness it changed; a class on the other
// harness alone reads no drift.
func TestAnAdapterChangeDriftsOnlyItsOwnHarness(t *testing.T) {
	authored := "scan-aaa/claude-code:1111+codex:2222"
	moved := unit("codex", "scan-aaa/codex:9999", 30)
	steady := unit("claude-code", "scan-aaa/claude-code:1111", 30)

	if !SessionInstrumentDiffers(moved.Instrument, authored) {
		t.Error("the changed Codex adapter did not read as drift")
	}
	if SessionInstrumentDiffers(steady.Instrument, authored) {
		t.Error("the unchanged Claude Code adapter read as drift")
	}
	drifted := driftingSessionInstruments([]SessionUnit{steady, moved}, authored)
	if len(drifted) != 1 || drifted[0] != moved.Instrument {
		t.Errorf("drift = %v; want only the Codex unit", drifted)
	}
}

func TestAClassifierChangeDriftsEveryHarness(t *testing.T) {
	authored := "scan-aaa/claude-code:1111+codex:2222"
	for _, u := range []string{"scan-bbb/claude-code:1111", "scan-bbb/codex:2222"} {
		if !SessionInstrumentDiffers(u, authored) {
			t.Errorf("%q did not read as drift against %q", u, authored)
		}
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
