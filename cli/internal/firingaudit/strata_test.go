package firingaudit

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/adjudication"
	"github.com/steer-sh/steer-cli/internal/checks"
)

func blockCase(id string, m int, failures ...CheckFailure) CheckCase {
	return CheckCase{Run: blockRun("/r", id, m), Failures: failures}
}

func checkFailure(check, applies string) CheckFailure {
	return CheckFailure{Kind: adjudication.FailureCheck, Check: check, Required: true, Applies: applies}
}

var oracleFailure = CheckFailure{Kind: adjudication.FailureOracle, Applies: adjudication.Unmanaged}

// cleanGuardReading is a receipt that read the continuation and found the block
// right, under the implementation the fixtures install.
func cleanGuardReading(id string) adjudication.Receipt {
	return adjudication.Receipt{Subject: adjudication.SubjectGuard, FiringID: id, Judgment: adjudication.Prevented,
		ContinuationInspected: true, InspectedSteps: 2, HookDigest: "hook1", RuleHash: "rule1"}
}

func cleanCheckReading(run, check string) adjudication.Receipt {
	return cleanReadingOf(run, adjudication.FailureCheck, check)
}

func cleanReadingOf(run, kind, check string) adjudication.Receipt {
	return adjudication.Receipt{Subject: adjudication.SubjectCheck, RunID: run, ContinuationInspected: true, InspectedSteps: 2,
		Failures: []adjudication.Failure{{Kind: kind, Check: check,
			Validity: adjudication.Valid, Attribution: adjudication.Introduced, Continuation: adjudication.Fixed}}}
}

func second(r Row) Row {
	r.HookName = "second"
	return r
}

// One chatty guard does not hold every other guard behind its backlog.
func TestQueueTakesUnitsInTurn(t *testing.T) {
	rows := []Row{
		ccRow(t, "a1", "2026-09-20T01:00:00Z"),
		ccRow(t, "a2", "2026-09-20T02:00:00Z"),
		ccRow(t, "a3", "2026-09-20T03:00:00Z"),
		second(ccRow(t, "b1", "2026-09-20T04:00:00Z")),
	}
	items, _ := Queue(rows, nil, nil, QueueOptions{Installed: installed})
	if got := strings.Join(ids(items), ","); got != "a1,b1,a2,a3" {
		t.Fatalf("queue = %s; want the guards in turn, a1,b1,a2,a3", got)
	}
	for _, item := range items {
		if item.Stratum != StratumUnit || !strings.HasPrefix(item.Unit, "guard:") {
			t.Errorf("%s stratum %q unit %q; want an owed guard unit", item.ID(), item.Stratum, item.Unit)
		}
	}
}

// A unit's clean readings put it behind a unit nobody has read; a reading that
// settled nothing is not clean.
func TestQueueServesTheUnitWithFewestCleanReadingsFirst(t *testing.T) {
	read := ccRow(t, "r0", "2026-09-20T00:00:00Z")
	rows := []Row{read, ccRow(t, "a1", "2026-09-20T01:00:00Z"), second(ccRow(t, "b1", "2026-09-20T04:00:00Z"))}

	items, _ := Queue(rows, nil, []adjudication.Receipt{cleanGuardReading("r0")}, QueueOptions{Installed: installed})
	if got := strings.Join(ids(items), ","); got != "b1,a1" {
		t.Errorf("queue = %s; want the unread guard first", got)
	}

	unsettled := cleanGuardReading("r0")
	unsettled.Judgment = adjudication.Unresolved
	items, _ = Queue(rows, nil, []adjudication.Receipt{unsettled}, QueueOptions{Installed: installed})
	if got := strings.Join(ids(items), ","); got != "a1,b1" {
		t.Errorf("queue = %s; want an unresolved reading to count for nothing", got)
	}

	empty := cleanGuardReading("r0")
	empty.InspectedSteps = 0
	items, _ = Queue(rows, nil, []adjudication.Receipt{empty}, QueueOptions{Installed: installed})
	if got := strings.Join(ids(items), ","); got != "a1,b1" {
		t.Errorf("queue = %s; want a reading that saw no step after the block to count for nothing", got)
	}
}

// A guard with enough clean readings moves its unread firings behind every owed
// case, and one wrong block keeps the guard owed.
func TestQueueReservesASaturatedGuard(t *testing.T) {
	var rows []Row
	var receipts []adjudication.Receipt
	for _, id := range []string{"r1", "r2", "r3"} {
		rows = append(rows, ccRow(t, id, "2026-09-20T00:00:00Z"))
		receipts = append(receipts, cleanGuardReading(id))
	}
	rows = append(rows, ccRow(t, "a1", "2026-09-20T01:00:00Z"), second(ccRow(t, "b1", "2026-09-20T05:00:00Z")))

	items, counts := Queue(rows, nil, receipts, QueueOptions{Installed: installed})
	if got := strings.Join(ids(items), ","); got != "b1,a1" {
		t.Fatalf("queue = %s; want the saturated guard's firing last", got)
	}
	if items[1].Stratum != StratumReserve || counts.Reserve != 1 || counts.Queued != 2 {
		t.Errorf("a1 stratum %q, counts %+v; want one reserve case of two queued", items[1].Stratum, counts)
	}
	if !strings.Contains(counts.Remaining(), "reserve") {
		t.Errorf("remaining %q; want the reserve disclosed", counts.Remaining())
	}

	rows[0].RowDigest = "row1"
	excluded := map[string]adjudication.Exclusion{"row1": {}}
	items, _ = Queue(rows, nil, receipts, QueueOptions{Installed: installed, Excluded: excluded})
	for _, item := range items {
		if item.Stratum != StratumUnit {
			t.Errorf("%s stratum %q; want an excluded reading to count for nothing", item.ID(), item.Stratum)
		}
	}

	receipts[0].Judgment = adjudication.Incorrect
	items, _ = Queue(rows, nil, receipts, QueueOptions{Installed: installed})
	for _, item := range items {
		if item.Stratum != StratumUnit {
			t.Errorf("%s stratum %q; want a guard with a wrong block owed", item.ID(), item.Stratum)
		}
	}
}

// A check reading saturates its check's unit as a guard reading does, and a
// routed-around continuation keeps it owed.
func TestQueueReservesASaturatedCheck(t *testing.T) {
	var cases []CheckCase
	var receipts []adjudication.Receipt
	for i, id := range []string{"p1", "p2", "p3"} {
		cases = append(cases, blockCase(id, i, checkFailure("go", adjudication.Current), oracleFailure))
		receipts = append(receipts, cleanCheckReading(id, "go"))
	}
	cases = append(cases, blockCase("n", 10, checkFailure("go", adjudication.Current), oracleFailure))

	items, _ := Queue(nil, cases, receipts, QueueOptions{})
	if len(items) != 1 || items[0].Stratum != StratumReserve || items[0].Unit != "check:go@/r" {
		t.Fatalf("queue = %+v; want the one unread block in the reserve of check:go", items)
	}

	moved := blockCase("v", 11, checkFailure("go", adjudication.Historic), oracleFailure)
	moved.Followup.Checks = []checks.CheckFollowup{{Blocked: checks.Execution{CheckID: "go"}, Detector: checks.FollowChanged}}
	items, _ = Queue(nil, append(cases, moved), receipts, QueueOptions{})
	if len(items) != 2 || items[0].RunID != "v" || items[0].Stratum != StratumUnit {
		t.Errorf("queue = %+v; want a block whose detector moved owed ahead of the reserve", items)
	}

	items, _ = Queue(nil, cases, receipts, QueueOptions{ExcludedRuns: map[string]adjudication.Exclusion{"p1": {}}})
	if len(items) != 1 || items[0].Stratum != StratumUnit {
		t.Errorf("queue = %+v; want an excluded block's reading to count for nothing", items)
	}

	interrupted := append([]adjudication.Receipt(nil), receipts...)
	interrupted[0] = cleanCheckReading("p1", "go")
	interrupted[0].Failures[0].Continuation = adjudication.Interrupted
	items, _ = Queue(nil, cases, interrupted, QueueOptions{})
	if len(items) != 1 || items[0].Stratum != StratumUnit {
		t.Errorf("queue = %+v; want an interrupted continuation to count for nothing", items)
	}

	stepless := append([]adjudication.Receipt(nil), receipts...)
	stepless[0] = cleanCheckReading("p1", "go")
	stepless[0].InspectedSteps = 0
	items, _ = Queue(nil, cases, stepless, QueueOptions{})
	if len(items) != 1 || items[0].Stratum != StratumUnit {
		t.Errorf("queue = %+v; want a reading that saw no step after the block to count for nothing", items)
	}

	// check.sh's own unit has no identity, so its readings never saturate it.
	var own []CheckCase
	var ownReadings []adjudication.Receipt
	for i, id := range []string{"o1", "o2", "o3", "o4"} {
		own = append(own, blockCase(id, 20+i, CheckFailure{Kind: adjudication.FailureOracle, Required: true,
			Applies: adjudication.Unmanaged}))
		ownReadings = append(ownReadings, cleanReadingOf(id, adjudication.FailureOracle, ""))
	}
	items, _ = Queue(nil, own, ownReadings[:3], QueueOptions{})
	if len(items) != 1 || items[0].Stratum != StratumUnit {
		t.Errorf("queue = %+v; want check.sh's own unit owed whatever its readings", items)
	}

	receipts[0].Failures[0].Continuation = adjudication.RoutedAround
	items, _ = Queue(nil, cases, receipts, QueueOptions{})
	if len(items) != 1 || items[0].Stratum != StratumUnit {
		t.Errorf("queue = %+v; want a check that was routed around owed", items)
	}
}

// A block whose declared checks all changed since stays owed only where the
// records show the change before the check's next pass or the session's next
// Stop; a block with no declared-check failure has no detector to be stale.
func TestQueueLeavesForOtherABlockNoReadingDescribes(t *testing.T) {
	historic := blockCase("h", 1, checkFailure("go", adjudication.Historic), oracleFailure)
	changed := blockCase("c", 2, checkFailure("go", adjudication.Historic), oracleFailure)
	changed.Followup.Checks = []checks.CheckFollowup{{Blocked: checks.Execution{CheckID: "go"}, Detector: checks.FollowChanged}}
	removed := blockCase("d", 3, checkFailure("go", adjudication.Historic), oracleFailure)
	removed.Followup.Checks = []checks.CheckFollowup{{Blocked: checks.Execution{CheckID: "go"}, Undeclared: true}}
	oracleOnly := blockCase("o", 4, CheckFailure{Kind: adjudication.FailureOracle, Required: true, Applies: adjudication.Unmanaged})
	wiring := blockCase("w", 5, checkFailure("go", adjudication.Historic),
		CheckFailure{Kind: adjudication.FailureIntegration, Required: true, Applies: adjudication.Unmanaged})
	cases := []CheckCase{historic, changed, removed, oracleOnly, wiring}

	items, counts := Queue(nil, cases, nil, QueueOptions{})
	if got := strings.Join(ids(items), ","); got != "c,o,w,d" {
		t.Fatalf("queue = %s; want c, o, w, d and not the historic block", got)
	}
	if counts.Other != 1 || !strings.Contains(counts.Remaining(), "--other") {
		t.Errorf("counts %+v, remaining %q; want the historic block disclosed for --other", counts, counts.Remaining())
	}
	if !items[0].Failures[0].Moved || items[1].Unit != "oracle@/r" {
		t.Errorf("items %+v; want the changed failure marked moved and check.sh's own unit", items)
	}

	items, counts = Queue(nil, cases, nil, QueueOptions{Other: true})
	if last := items[len(items)-1]; last.RunID != "h" || last.Stratum != StratumOther || counts.Other != 0 {
		t.Errorf("widened queue ends %+v, counts %+v; want the historic block listed last as other", last, counts)
	}
}

// A block failing two checks takes the turn of the less-read one, and listing
// it counts as a reading of both.
func TestQueueCountsOneBlockTowardEveryUnitItCovers(t *testing.T) {
	read := blockCase("p", 1, checkFailure("go", adjudication.Current), oracleFailure)
	both := blockCase("m", 2, checkFailure("go", adjudication.Current), checkFailure("lint", adjudication.Current), oracleFailure)
	goOnly := blockCase("g", 3, checkFailure("go", adjudication.Current), oracleFailure)
	lintOnly := blockCase("l", 4, checkFailure("lint", adjudication.Current), oracleFailure)

	items, _ := Queue(nil, []CheckCase{read, both, goOnly, lintOnly},
		[]adjudication.Receipt{cleanCheckReading("p", "go")}, QueueOptions{})
	if got := strings.Join(ids(items), ","); got != "m,l,g" {
		t.Fatalf("queue = %s; want m, then l before the more-read go unit", got)
	}
	if items[0].Unit != "check:lint@/r" {
		t.Errorf("m unit = %q; want the unread lint unit", items[0].Unit)
	}

	// A block takes the turn of whichever of its units is least read when that
	// turn comes, not when the queue starts.
	first := blockCase("m1", 1, checkFailure("go", adjudication.Current), checkFailure("lint", adjudication.Current), oracleFailure)
	goNext := blockCase("a1", 2, checkFailure("go", adjudication.Current), oracleFailure)
	later := blockCase("m2", 3, checkFailure("go", adjudication.Current), checkFailure("lint", adjudication.Current), oracleFailure)
	items, _ = Queue(nil, []CheckCase{first, goNext, later}, nil, QueueOptions{})
	if got := strings.Join(ids(items), ","); got != "m1,a1,m2" || items[2].Unit != "check:lint@/r" {
		t.Errorf("queue = %s, last unit %q; want m2 to take lint's turn", got, items[len(items)-1].Unit)
	}
}
