package mine

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// TestLegacyBaselineChainsRoundTrip pins the migration-safety claim: a row written
// before the units migration, keyed baseline.chains, survives Save/Load untouched.
// The writer never rewrites it to units, so no reader breaks on a mixed corpus.
func TestLegacyBaselineChainsRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	legacy := Record{
		"id":       "legacy-class",
		"class":    "legacy class",
		"match":    "legacy",
		"status":   "active",
		"baseline": map[string]any{"hits": 5, "chains": 20, "rate": 0.25},
	}
	if err := Save(stateDir, []Record{legacy}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("want 1 record, got %d", len(loaded))
	}
	baseline := loaded[0]["baseline"].(map[string]any)
	check(t, "legacy chains key survives", numInt(baseline["chains"]), 20)
	check(t, "no units key was injected", baseline["units"], nil)
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "widget parser flag loss", Match: "widget parser",
		Kind: "defect", Tier: "check", Target: "check.sh", InstalledTS: int64(install),
	})
	if err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	check(t, "one intervention recorded", len(recorded), 1)
	baseline := recorded[0]["baseline"].(map[string]any)
	check(t, "baseline hits", numInt(baseline["hits"]), 12)
	// Pin the corpus-neutral key literally: a revert to "chains" must fail here.
	check(t, "baseline units", numInt(baseline["units"]), 12)
	check(t, "no legacy chains key", baseline["chains"], nil)
	check(t, "corpus tag", str(recorded[0]["corpus"]), CorpusReviewFindings)
	check(t, "unit tag", str(recorded[0]["unit"]), UnitChain)
	check(t, "id is slugified", str(recorded[0]["id"]), "widget-parser-flag-loss")

	allRows, _ := LoadRows(stateDir, ledger)
	scored, err := ScoreRecord(recorded[0], EvidenceSet{Chains: BuildChains(allRows, false)}, 12)
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "never seen", Match: "zzz nonexistent zzz",
		Tier: "check", Target: "check.sh", InstalledTS: int64(install),
	}); err == nil {
		t.Error("zero baseline should be refused")
	}

	// Duplicate class names must not collide onto one id.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "widget parser flag loss", Match: "widget parser",
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "widget parser flag loss v2", Match: "widget parser",
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "steer class without an id", Match: "widget parser",
		Tier: "steer", Target: "hooks/some-hook.py", InstalledTS: int64(install),
	}); err == nil {
		t.Error("steer tier should demand a hook id")
	}
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "steer class with an id", Match: "widget parser",
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "within-chain repeat finding", Match: "repeat of round",
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "never seen", Match: "zzz nonexistent zzz",
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "repeat suppression", Match: "repeat of round",
		Tier: "check", Target: "check.sh", InstalledTS: int64(ts),
	}); err == nil {
		t.Error("a re-proposed rejected class should be blocked")
	}

	scored := scoreOK(t, records[0], chains, 12)
	check(t, "a rejection is scored from its rejection date", scored["verdict"], "class-steady")

	// Deciding to gate it after all supersedes the refusal rather than deleting it.
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "repeat suppression", Match: "repeat of round",
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "repeat suppression v2", Match: "repeat of round",
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
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "repeat class", Match: "repeat of round",
		Reason: "r", Reopen: "o", RejectedTS: int64(ts),
	}); err != nil {
		t.Fatal(err)
	}

	overlapping := func(force bool) error {
		return Register(io.Discard, stateDir, RegisterOpts{
			Prose:    testProse,
			ScopeAll: true, Ledger: ledger, Class: "repeat again", Match: "repeat",
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
	res, err := ScoreRecord(record, EvidenceSet{Chains: chains}, minAfter)
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

// TestScoreEmitsTheRange pins the measurement fields a scored record carries, and that a
// record predating scopes abstains instead of reporting a verdict.
func TestScoreEmitsTheRange(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 20; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("widget parser drops a flag")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	install := ts
	for i := 0; i < 20; i++ {
		rows = append(rows, row(ts, "deny"),
			concerns(row(ts+1, "retry"), "round", c("unrelated naming nit")),
			firing(row(ts+2, "clean"), 2))
		ts += 100
	}
	stateDir, ledger := writeLedger(t, rows)
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "widget parser flag loss", Match: "widget parser",
		Kind: "defect", Tier: "check", Target: "check.sh", InstalledTS: int64(install),
	}); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	allRows, _ := LoadRows(stateDir, ledger)
	chains := BuildChains(allRows, false)

	scored, err := ScoreRecord(recorded[0], EvidenceSet{Chains: chains}, 0)
	if err != nil {
		t.Fatal(err)
	}
	effect := scored["effect"].(map[string]any)
	check(t, "the effect names its method", effect["method"], EffectMethod)
	check(t, "the effect names its level", effect["confidence"], pyFloat(Confidence))
	if _, ok := effect["lower"].(json.Number); !ok {
		t.Errorf("the lower bound must always be a number, got %v", effect["lower"])
	}
	if effect["upper"] == nil {
		t.Error("a measured baseline bounds the ratio above")
	}
	if n, ok := scored["required_after"].(int); !ok || n <= 0 {
		t.Errorf("a measured baseline names its exposure floor, got %v", scored["required_after"])
	}
	cov := scored["before_coverage"].(map[string]any)
	check(t, "an all-scope makes every before-chain eligible", cov["eligible"], cov["total"])
	check(t, "an all-scope leaves nothing unknown", cov["unknown"], 0)

	// A record written before scopes existed has no frozen denominator, so its windows
	// describe different opportunity sets.
	legacy := Record{}
	for k, v := range recorded[0] {
		legacy[k] = v
	}
	delete(legacy, "scope")
	unscoped, err := ScoreRecord(legacy, EvidenceSet{Chains: chains}, 0)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "a scopeless record abstains", unscoped["verdict"], "legacy-unscoped")
}

// TestStoredBaselineMatchesTheScoredWindow pins that a writer freezes its baseline
// over the chains the scorer will actually measure. A chain carrying no adjudication
// is not evidence of the class's absence, so it belongs in neither count; counting it
// in the receipt and not in the verdict is how the two came to disagree on disk.
func TestStoredBaselineMatchesTheScoredWindow(t *testing.T) {
	var rows []gate.Row
	ts := 1000
	for i := 0; i < 8; i++ { // adjudicated before-chains carrying the class
		rows = append(rows, row(ts, "deny"), concerns(row(ts+1, "retry"), "round", c("widget parser drops a flag")))
		ts += 100
	}
	for i := 0; i < 5; i++ { // before-chains the resolver closed as permanently unadjudicated
		rows = append(rows, row(ts, "deny"), closedMiss(ts+1))
		ts += 100
	}
	install := ts
	stateDir, ledger := writeLedger(t, rows)

	withClock(int64(install)+1_000_000, func() {
		if err := Register(io.Discard, stateDir, RegisterOpts{
			Prose:    testProse,
			ScopeAll: true, Ledger: ledger, Class: "widget parser flag loss", Match: "widget parser",
			Kind: "defect", Tier: "check", Target: "check.sh", InstalledTS: int64(install),
		}); err != nil {
			t.Fatal(err)
		}
		recorded, _ := Load(stateDir)
		stored := recorded[0]["baseline"].(map[string]any)
		check(t, "the unadjudicated chains stay out of the stored baseline",
			numInt(stored["units"]), 8)

		allRows, _ := LoadRows(stateDir, ledger)
		scored, err := ScoreRecord(recorded[0], EvidenceSet{Chains: BuildChains(allRows, false)}, 0)
		if err != nil {
			t.Fatal(err)
		}
		before := scored["before"].(map[string]any)
		check(t, "the stored count is the scored count",
			numInt(stored["units"]), numInt(before["chains"]))
		check(t, "and so is the hit count",
			numInt(stored["hits"]), numInt(before["hits"]))
	})
}

// TestDefaultRegisterStillWritesAV1Scope is the compatibility pin for scope v2: an
// author who names no harness writes exactly the record an older binary reads.
func TestDefaultRegisterStillWritesAV1Scope(t *testing.T) {
	stateDir, ledger := writeLedger(t, []gate.Row{
		row(1000, "deny"), concerns(row(1001, "retry"), "round", c("widget parser drops a flag")),
	})
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:     testProse,
		ScopeRepo: "steer", Ledger: ledger, Class: "c", Match: "widget parser",
		Tier: "check", Target: "check.sh", InstalledTS: 2000, Force: true,
	}); err != nil {
		t.Fatal(err)
	}
	recorded, _ := Load(stateDir)
	scope := recorded[0]["scope"].(map[string]any)
	check(t, "an unharnessed scope stays v1", numInt(scope["version"]), ScopeVersionV1)
	check(t, "no harness key", scope["harness"], nil)
}

// TestScoreCarriesAnUnscorableRecordRatherThanAborting: one record no engine reads
// must not cost the verdicts of every record beside it.
func TestScoreCarriesAnUnscorableRecordRatherThanAborting(t *testing.T) {
	stateDir, ledger := writeLedger(t, []gate.Row{
		row(1000, "deny"), concerns(row(1001, "retry"), "round", c("widget parser drops a flag")),
	})
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "scorable", Match: "widget parser",
		Tier: "check", Target: "check.sh", InstalledTS: 2000,
	}); err != nil {
		t.Fatal(err)
	}
	records, _ := Load(stateDir)
	records = append(records, Record{
		"id": "orphan", "class": "o", "match": "x", "status": "active",
		"corpus": "made-up", "unit": "made-up-unit",
	})
	if err := Save(stateDir, records); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := Score(&buf, stateDir, ScoreOpts{Ledger: ledger}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "# unscored here (no engine reads that corpus): orphan") {
		t.Errorf("the unreadable corpus must be named, got:\n%s", out)
	}
	if !strings.Contains(out, "scorable") {
		t.Errorf("the scorable record must still be scored, got:\n%s", out)
	}
	// Naming it by --id is an error: the caller asked for a verdict that does not exist.
	if err := Score(io.Discard, stateDir, ScoreOpts{Ledger: ledger, ID: "orphan"}); err == nil {
		t.Error("scoring an unreadable corpus by id must be refused")
	}
}

// TestScoreReadsASessionRecordThroughTheSessionEngine: a record of the transcript
// corpus is scored beside a chain record, over its own evidence and its own unit.
func TestScoreReadsASessionRecordThroughTheSessionEngine(t *testing.T) {
	stateDir, ledger := writeLedger(t, []gate.Row{
		row(1000, "deny"), concerns(row(1001, "retry"), "round", c("widget parser drops a flag")),
	})
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "scorable", Match: "widget parser",
		Tier: "check", Target: "check.sh", InstalledTS: 2000,
	}); err != nil {
		t.Fatal(err)
	}
	sessions := []SessionUnit{
		friction(1000, "claude-code", "proj", "bare python here"),
		friction(1100, "claude-code", "proj", "bare python again"),
		friction(3000, "claude-code", "proj", "nothing to see"),
		friction(3100, "claude-code", "proj", "nothing to see"),
	}
	if err := Register(io.Discard, stateDir, RegisterOpts{
		Prose:    testProse,
		ScopeAll: true, Ledger: ledger, Class: "bare python", Match: "bare python",
		Corpus: CorpusTranscript, Sessions: sessions,
		Tier: "check", Target: "check.sh", InstalledTS: 2000,
	}); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := Score(&buf, stateDir, ScoreOpts{Ledger: ledger, Sessions: sessions}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "# unscored here") {
		t.Errorf("a session record must be scored, not carried unscored, got:\n%s", out)
	}
	if !strings.Contains(out, "bare-python") || !strings.Contains(out, "scorable") {
		t.Errorf("both engines' records must appear, got:\n%s", out)
	}
}
