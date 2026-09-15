package resolve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Join cases ported from hooks/commit-review/test_resolver.py: which ledger answers
// which retry row. A wrong join is worse than a missing one — it files a number that was
// never about that round, and nothing downstream can tell.

const (
	fixSession = "s1"
	fixBody    = "\n\n- **ACCEPTED** — a concern → a fix\n"
	// Rows before ledgerRoundTS take the window join; round-join cases sit after it.
	fixT = ledgerRoundTS + 1000
	// Rows before the mandate resolve with no scan.
	fixLegacyT = ledgerRequiredTS + 1000
	// Rows from ledgerChainTS take the chain join.
	fixCT = ledgerChainTS + 1000
)

func testResolver() *resolver {
	return &resolver{
		cfg: gate.Config{StaleAfter: 30 * time.Minute},
		now: func() float64 { return 2_000_000_000 },
	}
}

func ledgerFix(when float64, firing int) LedgerMsg {
	return ledgerFixSession(when, firing, fixSession)
}

func ledgerFixSession(when float64, firing int, session string) LedgerMsg {
	head := "**Commit gate fired"
	if firing != 0 {
		head += fmt.Sprintf(" (round %d/4)**", firing)
	} else {
		head += "**"
	}
	return LedgerMsg{When: when, Key: session, Path: "/t.jsonl", Text: head + fixBody}
}

func chainLedgerFix(when float64, rounds []int, subject string, terminal int) LedgerMsg {
	if subject == "" {
		subject = "a subject"
	}
	text := fmt.Sprintf("**Commit gate fired — chain ledger, %s**\n", subject)
	for _, n := range rounds {
		text += fmt.Sprintf("\n**Round %d/4** — 3 reviewers, 2 raw findings, 1 concern.\n", n) + fixBody
	}
	if terminal != 0 {
		text += fmt.Sprintf("\n**Round %d, terminal** — 3 reviewers, 1 raw finding, 1 concern. Commit not blocked.\n", terminal) + fixBody
	}
	return LedgerMsg{When: when, Key: fixSession, Path: "/t.jsonl", Text: text}
}

func chainRows(start float64, rounds int, subject string) []resolverRow {
	var rows []resolverRow
	for n := 1; n <= rounds; n++ {
		ts := start + float64(n-1)*100
		rows = append(rows,
			resolverRow{"phase": "deny", "ts": ts, "firing": n, "session_id": fixSession},
			resolverRow{"phase": "retry", "ts": ts + 10, "deny_ts": ts, "firing": n, "session_id": fixSession})
	}
	if subject != "" {
		for _, r := range rows {
			r["subject"] = subject
		}
	}
	return rows
}

func retryIndexes(rows []resolverRow) []int {
	var out []int
	for i, r := range rows {
		if pyStr(r["phase"]) == "retry" {
			out = append(out, i)
		}
	}
	return out
}

func candRound(m *LedgerMsg) (int, bool) {
	if m == nil {
		return 0, false
	}
	return gate.LedgerRound(m.Text)
}

func TestResolverSectionPatternsAgree(t *testing.T) {
	// The pattern that files a section and the one that declines a chain ledger must
	// agree on what a section line is — the Go twin of test_resolver's identity checks.
	section := "**Round 3/4** — 3 reviewers.\n"
	if !gate.HasRoundSections(section) || len(gate.LedgerRounds(section)) == 0 {
		t.Fatal("both section patterns must match a section line")
	}
	header := "**Commit gate fired — chain ledger, x**\n"
	if gate.HasRoundSections(header) || len(gate.LedgerRounds(header)) != 0 {
		t.Fatal("neither section pattern may match a header line")
	}
}

func TestResolverPerRound(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixT, 4, "")
	var leds []LedgerMsg
	for n := 1; n <= 4; n++ {
		leds = append(leds, ledgerFix(fixT+float64(n-1)*100+150, n))
	}
	for k, i := range retryIndexes(rows) {
		cand, join := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), leds)
		round, _ := candRound(cand)
		if join != "round" || round != k+1 {
			t.Fatalf("round %d resolved to (join=%s, round=%d), want (round, %d)", k+1, join, round, k+1)
		}
	}
}

func TestResolverMissingRound(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixT, 3, "")
	leds := []LedgerMsg{ledgerFix(fixT+150, 1), ledgerFix(fixT+350, 3)}
	cand, join := r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, leds)
	if cand != nil || join != "round" {
		t.Fatalf("round 2 wrote no ledger; got (cand!=nil=%v, join=%s), want (nil, round)", cand != nil, join)
	}
}

func TestResolverCrossChain(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixT, 1, ""), chainRows(fixT+1657, 1, "")...)
	leds := []LedgerMsg{ledgerFix(fixT+1763, 1)}
	cand, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, leds)
	if cand != nil {
		t.Fatal("a later chain reaching the same round bounds the search")
	}
	if !(fixT <= leds[0].When && leds[0].When <= fixT+r.staleAfter()) {
		t.Fatal("fixture: the ledger must sit inside the window it escapes")
	}
	cand, _ = r.pickLedger(rows, retryIndexes(rows)[1], fixT+1657, leds)
	if cand == nil {
		t.Fatal("the ledger still resolves for chain B, which wrote it")
	}
}

func TestResolverClearedNotAStart(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixT, 1, "")
	rows = append(rows, resolverRow{"phase": "skip-cleared", "ts": fixT + 20, "firing": 1, "session_id": fixSession})
	cand, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, []LedgerMsg{ledgerFix(fixT+150, 1)})
	if cand == nil {
		t.Fatal("a row where no reviewer ran starts no chain")
	}
}

func TestResolverCleanNotABound(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixT, 1, ""), resolverRow{"phase": "clean", "ts": fixT + 200, "firing": 1, "session_id": fixSession})
	cand, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, []LedgerMsg{ledgerFix(fixT+400, 1)})
	if cand == nil {
		t.Fatal("a chain that found nothing can own no ledger")
	}
}

func TestResolverReopen(t *testing.T) {
	r := testResolver()
	r.quiet = true
	rows := []resolverRow{
		{"phase": "retry", "ts": fixT, "firing": 1, "ledger_found": false, "backfilled": true},
		{"phase": "retry", "ts": fixT + 10, "firing": 1, "ledger_found": true, "backfilled": true},
	}
	r.reopen(rows)
	if _, ok := rows[0]["backfilled"]; ok {
		t.Fatal("a stamped miss is reopened")
	}
	if rows[1]["backfilled"] != true {
		t.Fatal("a resolved row is never reopened")
	}
}

func TestResolverShortChainBound(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixT, 2, ""), chainRows(fixT+1000, 1, "")...)
	cand, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, []LedgerMsg{ledgerFix(fixT+1100, 2)})
	if cand != nil {
		t.Fatal("the next chain STARTING bounds the search")
	}
}

func TestResolverChainEveryRound(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 4, "")
	led := chainLedgerFix(fixCT+500, []int{1, 2, 3, 4}, "", 0)
	for _, i := range retryIndexes(rows) {
		cand, join := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), []LedgerMsg{led})
		if join != "chain" || cand == nil || cand.Text != led.Text {
			t.Fatalf("one ledger answers all four rounds; got join=%s match=%v", join, cand != nil && cand.Text == led.Text)
		}
	}
}

func TestResolverChainMissingSection(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 3, "")
	led := chainLedgerFix(fixCT+400, []int{1, 3}, "", 0)
	cand, join := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{led})
	if cand != nil || join != "chain" {
		t.Fatalf("round 2 has no section; got (cand!=nil=%v, join=%s), want (nil, chain)", cand != nil, join)
	}
	cand, _ = r.pickLedger(rows, retryIndexes(rows)[2], fixCT+200, []LedgerMsg{led})
	if cand == nil || cand.Text != led.Text {
		t.Fatal("the rounds that DID get a section still resolve")
	}
}

func TestResolverSectionRounds(t *testing.T) {
	if got := gate.LedgerRounds(chainLedgerFix(0, []int{1, 2}, "", 0).Text); !sameSet(got, 1, 2) {
		t.Fatalf("section rounds = %v, want {1,2}", got)
	}
	if got := gate.LedgerRounds(chainLedgerFix(0, []int{4}, "", 5).Text); !sameSet(got, 4, 5) {
		t.Fatalf("terminal section rounds = %v, want {4,5}", got)
	}
	if got := gate.LedgerRounds(ledgerFix(0, 2).Text); len(got) != 0 {
		t.Fatalf("a per-round ledger has no sections, got %v", got)
	}
	r := testResolver()
	if got := r.spanFor("chain"); got != 8*r.staleAfter() {
		t.Fatalf("chain span = %v, want 8*staleAfter", got)
	}
}

func TestResolverChainCrossChain(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixCT, 1, ""), chainRows(fixCT+1000, 1, "")...)
	led := chainLedgerFix(fixCT+1100, []int{1}, "", 0)
	cand, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixCT, []LedgerMsg{led})
	if cand != nil {
		t.Fatal("the next chain STARTING bounds the search")
	}
	cand, _ = r.pickLedger(rows, retryIndexes(rows)[1], fixCT+1000, []LedgerMsg{led})
	if cand == nil || cand.Text != led.Text {
		t.Fatal("the ledger still resolves for chain B")
	}
}

func TestResolverOldShapeFallback(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 2, "")
	cand, join := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{ledgerFix(fixCT+250, 2)})
	if join != "round" || cand == nil {
		t.Fatalf("a per-round ledger under the chain era joins on round; got (join=%s, cand!=nil=%v)", join, cand != nil)
	}
}

func TestResolverHistoricalRoundJoin(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixT, 2, "")
	cand, join := r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, []LedgerMsg{ledgerFix(fixT+250, 2)})
	if join != "round" || cand == nil {
		t.Fatalf("rows under the round join keep it; got (join=%s, cand!=nil=%v)", join, cand != nil)
	}
}

func TestResolverCapSection(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixCT, 4, ""),
		resolverRow{"phase": "cap", "ts": fixCT + 400, "firing": 5, "finding_slots": 3, "session_id": fixSession})
	led := chainLedgerFix(fixCT+500, []int{1, 2, 3, 4}, "", 5)
	cand, join := r.pickLedger(rows, len(rows)-1, fixCT+400, []LedgerMsg{led})
	if join != "chain" || cand == nil || cand.Text != led.Text {
		t.Fatalf("the terminal round is a section like any other; got join=%s match=%v", join, cand != nil && cand.Text == led.Text)
	}
}

const multiLedger = "**Commit gate fired — chain ledger**\n" +
	"\n**Round 1/4** — 3 reviewers.\n\n- **ACCEPTED** — first → a fix\n" +
	"\n**Round 2/4** — 3 reviewers.\n\n- **REJECTED** — second → a reason\n" +
	"- **REJECTED** — third → another reason\n"

func TestResolverSectionSlice(t *testing.T) {
	concerns := gate.ParseLedgerText(gate.LedgerSection(multiLedger, 1))["concerns"].([]gate.Concern)
	if len(concerns) != 1 || concerns[0].Verdict != "ACCEPTED" || concerns[0].Concern != "first" {
		t.Fatalf("round 1's row carries round 1's one concern, got %+v", concerns)
	}
	if got := gate.ParseLedgerText(gate.LedgerSection(multiLedger, 2))["self_reported_rejected"]; got != 2 {
		t.Fatalf("the last section runs to the end; rejected = %v, want 2", got)
	}
	if got := gate.LedgerSection(multiLedger, 3); got != "" {
		t.Fatalf("a round with no section has nothing to slice, got %q", got)
	}
}

func TestResolverResolvedRow(t *testing.T) {
	r := testResolver()
	r.quiet = true
	r.harvests = []Harvest{func() []LedgerMsg {
		return []LedgerMsg{{When: fixCT + 400, Key: fixSession, Path: "/t.jsonl", Text: multiLedger}}
	}}
	rows := chainRows(fixCT, 2, "")
	r.resolveAndWrite(rows, len(rows), false)
	r1 := rows[retryIndexes(rows)[0]]
	if r1["ledger_join"] != "chain" || r1["self_reported_accepted"] != 1 ||
		r1["self_reported_rejected"] != 0 || r1["ledger_round"] != 1 {
		t.Fatalf("row records its own section's verdicts; got join=%v A=%v R=%v round=%v",
			r1["ledger_join"], r1["self_reported_accepted"], r1["self_reported_rejected"], r1["ledger_round"])
	}
}

func TestResolverTwoChainsOneRepo(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixCT, 2, "feat: first commit"), chainRows(fixCT+1000, 2, "fix: second commit")...)
	both := chainLedgerFix(0, []int{1, 2}, "feat: first commit", 0).Text + "\n" +
		chainLedgerFix(0, []int{1, 2}, "fix: second commit", 0).Text
	leds := []LedgerMsg{{fixCT + 1400, fixSession, "/t.jsonl", both}}
	var got []string
	for _, i := range retryIndexes(rows) {
		cand, join := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), leds)
		head := ""
		if cand != nil {
			head = strings.SplitN(cand.Text, "\n", 2)[0]
		}
		tag := "nil"
		switch {
		case strings.Contains(head, "first"):
			tag = "first"
		case strings.Contains(head, "second"):
			tag = "second"
		}
		got = append(got, join+"/"+tag)
	}
	want := []string{"chain/first", "chain/first", "chain/second", "chain/second"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("each chain takes the block naming ITS commit; got %v, want %v", got, want)
		}
	}
	if len(gate.ChainBlocks(both)) != 2 {
		t.Fatal("one message can hold two chain ledgers")
	}
	if s := gate.BlockSubject("**Commit gate fired — chain ledger, feat: x**\n- **ACCEPTED** — fix: y → done\n"); s != "feat: x" {
		t.Fatalf("only the header line names the commit; got %q", s)
	}
}

// subjectRows is a one-round chain whose deny and retry carry different subjects, as
// when the agent rewrites the message before it retries.
func subjectRows(start float64, denySubject, retrySubject string) []resolverRow {
	rows := chainRows(start, 1, "")
	rows[0]["subject"] = denySubject
	rows[1]["subject"] = retrySubject
	return rows
}

// blockHead is the header line of a picked block, or "nil" for a miss.
func blockHead(cand *LedgerMsg) string {
	if cand == nil {
		return "nil"
	}
	return gate.BlockSubject(cand.Text)
}

func TestResolverChainAnswersToEverySubject(t *testing.T) {
	r := testResolver()
	rows := append(subjectRows(fixCT, "fix: 24 leaks", "fix: 25 leaks"),
		chainRows(fixCT+1000, 1, "feat: other")...)
	deferred := chainLedgerFix(0, []int{1}, "feat: other", 0).Text + "\n" +
		chainLedgerFix(0, []int{1}, "fix: 24 leaks", 0).Text
	leds := []LedgerMsg{{fixCT + 1400, fixSession, "/t.jsonl", deferred}}
	cand, _ := r.pickLedger(rows, 1, fixCT, leds)
	if got := blockHead(cand); got != "fix: 24 leaks" {
		t.Fatalf("a header copied from the deny names the chain its retry changed; got %q", got)
	}
}

func TestResolverSharedNameIdentifiesNoChain(t *testing.T) {
	r := testResolver()
	rows := append(subjectRows(fixCT, "fix: 24 leaks", "fix: 25 leaks"),
		subjectRows(fixCT+1000, "fix: 25 leaks", "fix: the rest")...)
	leds := []LedgerMsg{chainLedgerFix(fixCT+1400, []int{1}, "fix: 25 leaks", 0)}
	for _, i := range retryIndexes(rows) {
		if cand, _ := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), leds); cand != nil {
			t.Fatalf("row %d claimed a block whose name two chains carry", i)
		}
	}
	same := append(chainRows(fixCT, 1, "wip"), chainRows(fixCT+1000, 1, "wip")...)
	leds = []LedgerMsg{chainLedgerFix(fixCT+1400, []int{1}, "wip", 0)}
	if cand, _ := r.pickLedger(same, 1, fixCT, leds); cand != nil {
		t.Fatal("a repeated subject names neither chain past its bound")
	}
}

// pins existing behaviour
func TestResolverRewordedNameClaimsNoOtherChain(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixCT, 1, "feat: first commit"), chainRows(fixCT+1000, 1, "fix: second commit")...)
	a := chainLedgerFix(0, []int{1}, "feat: first commit", 0).Text
	b := chainLedgerFix(0, []int{1}, "fix: the second one", 0).Text
	for _, text := range []string{a + "\n" + b, b + "\n" + a} {
		leds := []LedgerMsg{{fixCT + 1400, fixSession, "/t.jsonl", text}}
		cand, _ := r.pickLedger(rows, 1, fixCT, leds)
		if got := blockHead(cand); got != "feat: first commit" {
			t.Fatalf("the first chain takes its own deferred block; got %q", got)
		}
		cand, _ = r.pickLedger(rows, 3, fixCT+1000, leds)
		if cand != nil {
			t.Fatalf("a reworded block never hands the second chain the first one's; got %q", blockHead(cand))
		}
	}
}

func TestResolverGitRowStaysInsideItsBound(t *testing.T) {
	r := testResolver()
	const placeholder = "(not available to a pre-commit hook — judge the diff alone)"
	rows := append(chainRows(fixCT, 1, placeholder), chainRows(fixCT+1000, 1, placeholder)...)
	for _, row := range rows {
		delete(row, "session_id")
		row["repo"] = "/r"
	}
	late := chainLedgerFix(fixCT+1400, []int{1}, placeholder, 0)
	if cand, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{late}); cand != nil {
		t.Fatal("a git row's shared subject lets no ledger past the chain bound")
	}
	if cand, _ := r.pickLedger(rows, 3, fixCT+1000, []LedgerMsg{late}); cand == nil {
		t.Fatal("the chain the ledger landed inside still takes it")
	}
	early := chainLedgerFix(fixCT+400, []int{1}, placeholder, 0)
	if cand, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{early}); cand == nil {
		t.Fatal("a git row keeps its bounded join")
	}
}

func TestResolverSubjectAbsentFallsBack(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 2, "")
	leds := []LedgerMsg{chainLedgerFix(fixCT+400, []int{1, 2}, "feat: unmatched", 0)}
	cand, join := r.pickLedger(rows, retryIndexes(rows)[0], fixCT, leds)
	if join != "chain" || cand == nil {
		t.Fatalf("an unread commit message leaves the row on the bound it had; got (join=%s, cand!=nil=%v)", join, cand != nil)
	}
}

func TestResolverLegacyAndSpans(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixLegacyT, 2, "")
	leds := []LedgerMsg{ledgerFix(fixLegacyT+150, 0), ledgerFix(fixLegacyT+250, 0)}
	_, join := r.pickLedger(rows, retryIndexes(rows)[0], fixLegacyT, leds)
	if join != "window" {
		t.Fatalf("unmarked ledgers fall back to the window join, got %s", join)
	}
	if r.spanFor("window") != r.staleAfter() {
		t.Fatal("the window keeps the 30-minute span")
	}
	if r.spanFor("round") != 4*r.staleAfter() {
		t.Fatal("a marked ledger may land long after its deny")
	}
}

func TestResolverSessionIsolation(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixT, 1, "")
	cand, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, []LedgerMsg{ledgerFixSession(fixT+150, 1, "other")})
	if cand != nil {
		t.Fatal("another session's ledger is never this row's")
	}
}

func TestResolverCap(t *testing.T) {
	r := testResolver()
	capRows := append(chainRows(fixT, 4, ""),
		resolverRow{"phase": "cap", "ts": fixT + 400, "firing": 5, "finding_slots": 3, "session_id": fixSession})
	idx := len(capRows) - 1
	if !ExpectsLedger(capRows[idx]) {
		t.Fatal("a terminal review with findings demands a ledger")
	}
	if ExpectsLedger(resolverRow{"phase": "cap", "firing": 5, "finding_slots": 0}) {
		t.Fatal("a clean terminal review asked for nothing")
	}
	if !ExpectsLedger(resolverRow{"phase": "cap", "firing": 5, "finding_slots": 0,
		"reviews_off_format": 2}) {
		t.Fatal("prose findings reached the agent and owe a ledger, whatever the slots read")
	}
	if ts, _ := anchorTS(capRows, idx); ts != fixT+400 {
		t.Fatalf("the cap row IS the handover moment; anchor = %v, want %v", ts, fixT+400)
	}
	cand, join := r.pickLedger(capRows, idx, fixT+400, []LedgerMsg{ledgerFix(fixT+500, 5)})
	if round, _ := candRound(cand); join != "round" || round != 5 {
		t.Fatalf("the terminal ledger joins on its own round; got (join=%s, round=%d)", join, round)
	}
	cand, _ = r.pickLedger(capRows, idx, fixT+400, []LedgerMsg{ledgerFix(fixT+500, 4)})
	if cand != nil {
		t.Fatal("round 4's ledger answers round 4's deny, not the terminal review")
	}
}

func TestResolverPending(t *testing.T) {
	now := 2_000_000_000.0
	r := testResolver()
	r.now = func() float64 { return now }
	fresh := []resolverRow{
		{"phase": "deny", "ts": now, "firing": 1},
		{"phase": "retry", "ts": now, "deny_ts": now, "firing": 1},
	}
	if r.pending(fresh) {
		t.Fatal("a deny seconds old has no ledger yet")
	}
	old := []resolverRow{
		{"phase": "deny", "ts": now - 9999, "firing": 1},
		{"phase": "retry", "ts": now - 9999, "deny_ts": now - 9999, "firing": 1},
	}
	if !r.pending(old) {
		t.Fatal("an unresolved old row is worth a scan")
	}
	done := []resolverRow{old[0], {"phase": "retry", "ts": now - 9999, "deny_ts": now - 9999, "firing": 1, "backfilled": true, "ledger_found": true}}
	if r.pending(done) {
		t.Fatal("resolved rows are not due again")
	}
}

func TestResolverOpenWindowUntouched(t *testing.T) {
	now := 2_000_000_000.0
	r := testResolver()
	r.quiet = true
	r.now = func() float64 { return now }
	r.harvests = []Harvest{func() []LedgerMsg { return nil }}
	waiting := []resolverRow{
		{"phase": "deny", "ts": now - 200, "firing": 1, "session_id": fixSession},
		{"phase": "retry", "ts": now - 190, "deny_ts": now - 200, "firing": 1, "session_id": fixSession},
	}
	r.resolveAndWrite(waiting, len(waiting), false)
	for _, k := range []string{"ledger_join", "ledger_found", "backfilled"} {
		if _, ok := waiting[1][k]; ok {
			t.Fatalf("no verdict about a ledger that may still arrive; %s was set", k)
		}
	}
	if waiting[1]["ledger_expected"] != true {
		t.Fatal("the row IS known to owe one")
	}
}

func TestResolverConcurrentAppendKept(t *testing.T) {
	dir := t.TempDir()
	cfg := gate.Config{StateDir: dir, HostID: "testhost", StaleAfter: 30 * time.Minute}
	stale := float64(ledgerRequiredTS - 1000)
	snapshot := []resolverRow{
		{"phase": "deny", "ts": stale, "firing": 1},
		{"phase": "retry", "ts": stale + 10, "deny_ts": stale, "firing": 1},
	}
	var sb strings.Builder
	for _, row := range snapshot {
		line, _ := json.Marshal(row)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(cfg.LogFile(), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := len(snapshot)
	// The hook appends mid-run.
	f, _ := os.OpenFile(cfg.LogFile(), os.O_APPEND|os.O_WRONLY, 0o644)
	appended, _ := json.Marshal(resolverRow{"phase": "deny", "ts": 999, "firing": 1})
	f.Write(append(appended, '\n'))
	f.Close()

	r := &resolver{cfg: cfg, quiet: true, now: func() float64 { return 2_000_000_000 }, harvests: []Harvest{func() []LedgerMsg { return nil }}}
	rows := []resolverRow{copyRow(snapshot[0]), copyRow(snapshot[1])}
	if res := r.resolveAndWrite(rows, seen, true); res.Err != nil {
		t.Fatalf("resolver write failed: %v", res.Err)
	}
	after := readShard(t, cfg.LogFile())
	if after[1]["backfilled"] != true {
		t.Fatal("the snapshot row really was rewritten")
	}
	gotTS := []float64{pyNumOr(after[0]["ts"]), pyNumOr(after[1]["ts"]), pyNumOr(after[2]["ts"])}
	want := []float64{stale, stale + 10, 999}
	for i := range want {
		if gotTS[i] != want[i] {
			t.Fatalf("a row appended during the run survives; ts = %v, want %v", gotTS, want)
		}
	}
	// A backup was written before the rewrite.
	backups, _ := filepath.Glob(cfg.LogFile() + ".bak.*")
	if len(backups) == 0 {
		t.Fatal("the rewrite must back up the shard first")
	}
}

func TestResolverRewriteKeepsTenNewestBackups(t *testing.T) {
	dir := t.TempDir()
	cfg := gate.Config{StateDir: dir, HostID: "testhost", StaleAfter: 30 * time.Minute}
	stale := float64(ledgerRequiredTS - 1000)
	row := resolverRow{"phase": "retry", "ts": stale + 10, "deny_ts": stale, "firing": 1}
	line, _ := json.Marshal(row)
	if err := os.WriteFile(cfg.LogFile(), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	for ts := 1; ts <= 12; ts++ {
		if err := os.WriteFile(fmt.Sprintf("%s.bak.%d", cfg.LogFile(), ts), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	foreign := cfg.LogFile() + ".bak.manual"
	if err := os.WriteFile(foreign, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &resolver{cfg: cfg, quiet: true, now: func() float64 { return 2_000_000_000 }, harvests: []Harvest{func() []LedgerMsg { return nil }}}
	if res := r.resolveAndWrite([]resolverRow{copyRow(row)}, 1, true); res.Err != nil || res.Changed == 0 {
		t.Fatalf("resolver write: %+v", res)
	}

	backups, _ := filepath.Glob(cfg.LogFile() + ".bak.*")
	if len(backups) != backupKeep+1 {
		t.Fatalf("want %d dated backups plus the undated one, got %v", backupKeep, backups)
	}
	for _, gone := range []int{1, 2, 3} {
		if _, err := os.Stat(fmt.Sprintf("%s.bak.%d", cfg.LogFile(), gone)); !os.IsNotExist(err) {
			t.Fatalf("backup %d is among the oldest and must be removed", gone)
		}
	}
	for _, kept := range []string{cfg.LogFile() + ".bak.2000000000", cfg.LogFile() + ".bak.12", foreign} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s must be kept: %v", filepath.Base(kept), err)
		}
	}
}

// --- test helpers ------------------------------------------------------------

func pyNumOr(v any) float64 {
	f, _ := pyNum(v)
	return f
}

func sameSet(got map[int]bool, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for _, n := range want {
		if !got[n] {
			return false
		}
	}
	return true
}

func copyRow(row resolverRow) resolverRow {
	out := make(resolverRow, len(row))
	for k, v := range row {
		out[k] = v
	}
	return out
}

func readShard(t *testing.T, path string) []resolverRow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []resolverRow
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, DecodeRow(line))
	}
	return rows
}

// A harness that gates inside a subagent writes two chains under one session id. The
// join key is the thread, so neither chain can claim the other's ledger.
func TestResolverKeysAThreadedRowOnItsThread(t *testing.T) {
	r := testResolver()
	r.quiet = true
	rootRows := chainRows(fixCT, 1, "feat: root commit")
	threadRows := chainRows(fixCT+200, 1, "fix: thread commit")
	for _, row := range threadRows {
		row["thread_id"] = "agent-9"
	}
	rows := append(rootRows, threadRows...)

	rootLedger := chainLedgerFix(fixCT+50, []int{1}, "feat: root commit", 0)
	threadLedger := chainLedgerFix(fixCT+250, []int{1}, "fix: thread commit", 0)
	threadLedger.Key = "agent-9"
	r.harvests = []Harvest{func() []LedgerMsg { return []LedgerMsg{rootLedger, threadLedger} }}

	r.resolveAndWrite(rows, len(rows), false)
	idx := retryIndexes(rows)
	if len(idx) != 2 {
		t.Fatalf("retry rows = %d, want 2", len(idx))
	}
	for _, i := range idx {
		if rows[i]["ledger_found"] != true {
			t.Fatalf("row %d found no ledger: %v", i, rows[i])
		}
	}
	// The parent's session id must survive: the thread is provenance, not the unit.
	if rows[idx[1]]["session_id"] != fixSession {
		t.Fatalf("session_id = %v, want the parent's", rows[idx[1]]["session_id"])
	}
}

// A thread's ledger is not the parent's, however close in time it landed.
func TestResolverThreadedRowRefusesTheParentLedger(t *testing.T) {
	r := testResolver()
	r.quiet = true
	rows := chainRows(fixCT, 1, "fix: thread commit")
	for _, row := range rows {
		row["thread_id"] = "agent-9"
	}
	parent := chainLedgerFix(fixCT+50, []int{1}, "fix: thread commit", 0)
	r.harvests = []Harvest{func() []LedgerMsg { return []LedgerMsg{parent} }}

	r.resolveAndWrite(rows, len(rows), false)
	row := rows[retryIndexes(rows)[0]]
	if row["ledger_found"] == true {
		t.Fatalf("the thread claimed the parent's ledger: %v", row)
	}
}
