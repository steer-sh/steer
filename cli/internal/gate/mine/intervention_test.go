package mine

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// writeLedger seeds a single-file ledger in a fresh state dir and returns both paths.
func writeLedger(t *testing.T, rows []gate.Row) (stateDir, ledger string) {
	t.Helper()
	stateDir = t.TempDir()
	ledger = filepath.Join(stateDir, "adjudication.jsonl")
	f, err := os.Create(ledger)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range rows {
		if _, err := f.Write(append(mustJSON(t, r), '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return stateDir, ledger
}

func TestRegisterScoreRoundTrip(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("widget parser drops a flag")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	install := ts
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("unrelated naming nit")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	stateDir, ledger := writeLedger(t, rows)

	err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "widget parser flag loss", Match: "widget parser",
		Kind: "defect", Tier: "check", Target: "check.sh", InstalledTS: int64(install),
	})
	if err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	check(t, "one intervention recorded", len(recorded), 1)
	baseline := recorded[0]["baseline"].(map[string]any)
	check(t, "baseline hits", numInt(baseline["hits"]), 12)
	check(t, "baseline chains", numInt(baseline["chains"]), 12)
	check(t, "id is slugified", str(recorded[0]["id"]), "widget-parser-flag-loss")

	allRows, _ := LoadRows(stateDir, ledger)
	scored, err := ScoreRecord(recorded[0], BuildChains(allRows, false), 12)
	if err != nil {
		t.Fatal(err)
	}
	before := scored["before"].(map[string]any)
	after := scored["after"].(map[string]any)
	check(t, "before incidence", []int{numInt(before["hits"]), numInt(before["chains"])}, []int{12, 12})
	check(t, "after incidence", []int{numInt(after["hits"]), numInt(after["chains"])}, []int{0, 12})
	check(t, "a real elimination scores effective", scored["verdict"], "effective")

	// A regex matching nothing before install can never be scored, so registering it
	// is refused rather than recorded with a zero baseline.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "never seen", Match: "zzz nonexistent zzz",
		Tier: "check", Target: "check.sh", InstalledTS: int64(install),
	}); err == nil {
		t.Error("zero baseline should be refused")
	}

	// Duplicate class names must not collide onto one id.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "widget parser flag loss", Match: "widget parser",
		Tier: "steer", Target: "other.py", HookID: "widget-parser-hook", InstalledTS: int64(install),
	}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	recs, _ := Load(stateDir)
	for _, r := range recs {
		ids[str(r["id"])] = true
	}
	check(t, "duplicate class gets a distinct id", len(ids), len(recs))

	// Superseding retires the prior record rather than deleting it.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "widget parser flag loss v2", Match: "widget parser",
		Tier: "check", Target: "check.sh", InstalledTS: int64(install),
		Supersedes: "widget-parser-flag-loss",
	}); err != nil {
		t.Fatal(err)
	}
	byID := loadByID(t, stateDir)
	check(t, "superseded record is reworked", str(byID["widget-parser-flag-loss"]["status"]), "reworked")
	check(t, "superseded record points forward",
		str(byID["widget-parser-flag-loss"]["superseded_by"]), "widget-parser-flag-loss-v2")

	// A steer-tier intervention needs a hook id at register time.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "steer class without an id", Match: "widget parser",
		Tier: "steer", Target: "hooks/some-hook.py", InstalledTS: int64(install),
	}); err == nil {
		t.Error("steer tier should demand a hook id")
	}
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "steer class with an id", Match: "widget parser",
		Tier: "steer", Target: "hooks/some-hook.py", HookID: "no-such-hook-in-manifest",
		InstalledTS: int64(install),
	}); err != nil {
		t.Fatal(err)
	}
	byID = loadByID(t, stateDir)
	check(t, "an unknown hook id is still recorded",
		str(byID["steer-class-with-an-id"]["hook_id"]), "no-such-hook-in-manifest")
	_, hasHookID := byID["widget-parser-flag-loss"]["hook_id"]
	check(t, "check tier needs no hook id", hasHookID, false)

	if err := Retire(io.Discard, stateDir, "widget-parser-flag-loss-2", "no signal", "removed"); err != nil {
		t.Fatal(err)
	}
	byID = loadByID(t, stateDir)
	check(t, "retire sets status", str(byID["widget-parser-flag-loss-2"]["status"]), "removed")
	check(t, "retire records a reason", str(byID["widget-parser-flag-loss-2"]["retired_reason"]), "no signal")
}

func TestInstrumentDrift(t *testing.T) {
	driftRows := []gate.Row{
		instrument(row(1000, "deny"), "modelA/3/aaaa1111"),
		instrument(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")), "modelA/3/aaaa1111"),
		instrument(row(1100, "deny"), "modelB/3/bbbb2222"),
		instrument(concerns(row(1101, "retry"), "round", c("unrelated nit")), "modelB/3/bbbb2222"),
	}
	chains := BuildChains(driftRows, false)
	check(t, "chain records its instrument", chains[0].Instruments, []string{"modelA/3/aaaa1111"})
	check(t, "current instrument is the latest", CurrentInstrument(chains), "modelB/3/bbbb2222")
	check(t, "instruments_in preserves order", InstrumentsIn(chains),
		[]string{"modelA/3/aaaa1111", "modelB/3/bbbb2222"})

	base := Record{"id": "d", "class": "d", "tier": "check", "target": "check.sh",
		"match": "widget parser", "kind": "defect", "installed_ts": 1050, "installed_date": "x"}
	drifted := scoreOK(t, withKV(base, "instrument", "modelA/3/aaaa1111"), chains, 1)
	check(t, "drift is flagged when the reviewer changed", drifted["instrument_drift"], true)
	steady := scoreOK(t, withKV(base, "instrument", "modelB/3/bbbb2222"), chains, 1)
	check(t, "no drift when the reviewer is unchanged", steady["instrument_drift"], false)
	unstamped := scoreOK(t, base, chains, 1)
	check(t, "an unstamped record claims no drift", unstamped["instrument_drift"], false)

	plain := BuildChains([]gate.Row{row(1, "deny"), concerns(row(2, "retry"), "round", c("x"))}, false)
	check(t, "unstamped log yields no current instrument", CurrentInstrument(plain), "")
}

func TestHostMixDrift(t *testing.T) {
	hostRows := []gate.Row{
		host(row(1000, "deny"), "alpha"),
		host(concerns(row(1001, "retry"), "round", c("widget parser drops a flag")), "alpha"),
		host(row(1100, "deny"), "alpha"),
		host(concerns(row(1101, "retry"), "round", c("unrelated nit")), "alpha"),
		host(row(1200, "deny"), "beta"),
		host(concerns(row(1201, "retry"), "round", c("unrelated nit")), "beta"),
	}
	rec := Record{"id": "h", "class": "h", "tier": "check", "target": "check.sh",
		"match": "widget parser", "kind": "defect", "installed_ts": 1050, "installed_date": "x"}
	mixed := scoreOK(t, rec, BuildChains(hostRows, false), 1)
	check(t, "before-window hosts are reported", mixed["before_hosts"], []string{"alpha"})
	check(t, "after-window hosts are reported", mixed["after_hosts"], []string{"alpha", "beta"})
	check(t, "a machine appearing mid-window is flagged", mixed["host_mix_drift"], true)

	single := scoreOK(t, rec, BuildChains(hostRows[:4], false), 1)
	check(t, "one machine on both sides is no drift", single["host_mix_drift"], false)

	emptyAfter := scoreOK(t, withKV(rec, "installed_ts", 9999), BuildChains(hostRows, false), 1)
	check(t, "an empty after-window claims no host drift", emptyAfter["host_mix_drift"], false)
}

func TestRejections(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round",
				cv("repeat of round 1", "ACCEPTED"), cv("style nit", "REJECTED")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	rejectedAt := ts
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("repeat of round 1")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	stateDir, ledger := writeLedger(t, rows)

	if err := Reject(io.Discard, stateDir, RejectOpts{
		Ledger: ledger, Class: "within-chain repeat finding", Match: "repeat of round",
		Reason: "the class cannot be sized from the ledger",
		Reopen: "raw per-round reviewer text starts being persisted", RejectedTS: int64(rejectedAt),
	}); err != nil {
		t.Fatal(err)
	}
	rej, _ := Load(stateDir)
	check(t, "a rejection is recorded", str(rej[0]["status"]), "rejected")
	check(t, "a rejection installs nothing", str(rej[0]["tier"]), "none")
	check(t, "a rejection keeps a baseline", numInt(rej[0]["baseline"].(map[string]any)["hits"]), 12)

	// An unmatchable rejection is refused.
	if err := Reject(io.Discard, stateDir, RejectOpts{
		Ledger: ledger, Class: "never seen", Match: "zzz nonexistent zzz",
		Reason: "r", Reopen: "o", RejectedTS: int64(rejectedAt),
	}); err == nil {
		t.Error("a rejection with no baseline should be refused")
	}

	allRows, _ := LoadRows(stateDir, ledger)
	chains := BuildChains(allRows, false)
	records, _ := Load(stateDir)
	prior, _ := OverlappingDecisions(records, chains, mustCompile(t, "repeat of round 1"), "accepted")
	check(t, "an overlapping candidate finds the rejection", len(prior), 1)
	check(t, "overlap is over the candidate's chains", []int{prior[0].Shared, prior[0].Total}, []int{24, 24})

	fp, _ := OverlappingDecisions(records, chains, mustCompile(t, "style nit"), "rejected")
	check(t, "a rejection on accepted findings is not offered to a false-positive candidate", len(fp), 0)

	// Re-proposing the rejected class is blocked, not warned.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "repeat suppression", Match: "repeat of round",
		Tier: "check", Target: "check.sh", InstalledTS: int64(ts),
	}); err == nil {
		t.Error("a re-proposed rejected class should be blocked")
	}

	scored := scoreOK(t, records[0], chains, 12)
	check(t, "a rejection is scored from its rejection date", scored["verdict"], "class-steady")

	// Deciding to gate it after all supersedes the refusal rather than deleting it.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "repeat suppression", Match: "repeat of round",
		Tier: "check", Target: "check.sh", InstalledTS: int64(ts),
		Supersedes: "within-chain-repeat-finding",
	}); err != nil {
		t.Fatal(err)
	}
	byID := loadByID(t, stateDir)
	check(t, "superseding a rejection reopens it",
		str(byID["within-chain-repeat-finding"]["status"]), "reopened")
	check(t, "a reopened refusal keeps a date in its headline",
		containsQuestionMark(decisionHeadline(byID["within-chain-repeat-finding"])), false)

	// An overturned refusal must stop blocking.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Ledger: ledger, Class: "repeat suppression v2", Match: "repeat of round",
		Tier: "check", Target: "check.sh", InstalledTS: int64(ts),
		Supersedes: "repeat-suppression",
	}); err != nil {
		t.Fatalf("a reopened refusal should block nothing: %v", err)
	}
}

func TestForceBypassesOverlapBlock(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 12; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("repeat of round 1")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	stateDir, ledger := writeLedger(t, rows)

	if err := Reject(io.Discard, stateDir, RejectOpts{
		Ledger: ledger, Class: "repeat class", Match: "repeat of round",
		Reason: "r", Reopen: "o", RejectedTS: int64(ts),
	}); err != nil {
		t.Fatal(err)
	}

	overlapping := func(force bool) error {
		return Register(io.Discard, stateDir, RegisterOpts{
			Ledger: ledger, Class: "repeat again", Match: "repeat",
			Tier: "check", Target: "check.sh", InstalledTS: int64(ts), Force: force,
		})
	}
	if err := overlapping(false); err == nil {
		t.Error("without --force, an overlapping candidate must be blocked")
	}
	if err := overlapping(true); err != nil {
		t.Errorf("with --force, the rejection-overlap block must not fire: %v", err)
	}
}

// --- helpers ----------------------------------------------------------------

func loadByID(t *testing.T, stateDir string) map[string]Record {
	t.Helper()
	records, err := Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Record{}
	for _, r := range records {
		out[str(r["id"])] = r
	}
	return out
}

func scoreOK(t *testing.T, record Record, chains []Chain, minAfter int) map[string]any {
	t.Helper()
	res, err := ScoreRecord(record, chains, minAfter)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func withKV(base Record, key string, value any) Record {
	out := Record{}
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

func containsQuestionMark(s string) bool {
	for _, r := range s {
		if r == '?' {
			return true
		}
	}
	return false
}
