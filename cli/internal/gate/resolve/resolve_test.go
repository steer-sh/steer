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
		cand, join, _ := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), leds)
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
	cand, join, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, leds)
	if cand != nil || join != "round" {
		t.Fatalf("round 2 wrote no ledger; got (cand!=nil=%v, join=%s), want (nil, round)", cand != nil, join)
	}
}

func TestResolverCrossChain(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixT, 1, ""), chainRows(fixT+1657, 1, "")...)
	leds := []LedgerMsg{ledgerFix(fixT+1763, 1)}
	cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, leds)
	if cand != nil {
		t.Fatal("a later chain reaching the same round bounds the search")
	}
	if !(fixT <= leds[0].When && leds[0].When <= fixT+r.staleAfter()) {
		t.Fatal("fixture: the ledger must sit inside the window it escapes")
	}
	cand, _, _ = r.pickLedger(rows, retryIndexes(rows)[1], fixT+1657, leds)
	if cand == nil {
		t.Fatal("the ledger still resolves for chain B, which wrote it")
	}
}

func TestResolverClearedNotAStart(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixT, 1, "")
	rows = append(rows, resolverRow{"phase": "skip-cleared", "ts": fixT + 20, "firing": 1, "session_id": fixSession})
	cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, []LedgerMsg{ledgerFix(fixT+150, 1)})
	if cand == nil {
		t.Fatal("a row where no reviewer ran starts no chain")
	}
}

func TestResolverCleanNotABound(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixT, 1, ""), resolverRow{"phase": "clean", "ts": fixT + 200, "firing": 1, "session_id": fixSession})
	cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, []LedgerMsg{ledgerFix(fixT+400, 1)})
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
	cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, []LedgerMsg{ledgerFix(fixT+1100, 2)})
	if cand != nil {
		t.Fatal("the next chain STARTING bounds the search")
	}
}

func TestResolverChainEveryRound(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 4, "")
	led := chainLedgerFix(fixCT+500, []int{1, 2, 3, 4}, "", 0)
	for _, i := range retryIndexes(rows) {
		cand, join, _ := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), []LedgerMsg{led})
		if join != "chain" || cand == nil || cand.Text != led.Text {
			t.Fatalf("one ledger answers all four rounds; got join=%s match=%v", join, cand != nil && cand.Text == led.Text)
		}
	}
}

func TestResolverChainMissingSection(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 3, "")
	led := chainLedgerFix(fixCT+400, []int{1, 3}, "", 0)
	cand, join, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{led})
	if cand != nil || join != "chain" {
		t.Fatalf("round 2 has no section; got (cand!=nil=%v, join=%s), want (nil, chain)", cand != nil, join)
	}
	cand, _, _ = r.pickLedger(rows, retryIndexes(rows)[2], fixCT+200, []LedgerMsg{led})
	if cand == nil || cand.Text != led.Text {
		t.Fatal("the rounds that DID get a section still resolve")
	}
}

// A round heading with no verdict bullet under it adjudicated nothing, so it answers
// no row: stamping it would file zero concerns as that round's report.
func TestResolverEmptyRoundAnswersNothing(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 2, "")
	led := chainLedgerFix(fixCT+300, []int{1}, "", 0)
	led.Text += "\n**Round 2/4** — 3 reviewers, 1 raw finding, 1 concern. (see above)\n"
	cand, join, why := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{led})
	if cand != nil || join != "chain" || why != MissEmptyRound {
		t.Fatalf("round 2's section is empty; got (cand!=nil=%v, join=%s, why=%q), want (nil, chain, %q)",
			cand != nil, join, why, MissEmptyRound)
	}
	if cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixCT, []LedgerMsg{led}); cand == nil {
		t.Fatal("round 1's section has a bullet and still resolves")
	}
	roundOneOnly := chainLedgerFix(fixCT+250, []int{1}, "", 0)
	if _, _, why := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{roundOneOnly, led}); why != MissEmptyRound {
		t.Fatalf("an empty section outranks a missing one; got why=%q", why)
	}
	full := chainLedgerFix(fixCT+250, []int{1, 2}, "", 0)
	if cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{full, led}); cand == nil || cand.Text != full.Text {
		t.Fatal("a later empty section must not hide an earlier full one")
	}
	perRoundFirst := ledgerFix(fixCT+250, 2)
	if cand, join, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{perRoundFirst, led}); cand == nil || join != "round" || cand.Text != perRoundFirst.Text {
		t.Fatalf("an empty section must leave the round's own ledger to answer; got join=%s", join)
	}

	rows = chainRows(fixT, 2, "")
	perRound := ledgerFix(fixT+300, 1)
	perRound.Text += "\n**Commit gate fired (round 2/4)** — (see above)\n"
	cand, join, why = r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, []LedgerMsg{perRound})
	if cand != nil || join != "round" || why != MissEmptyRound {
		t.Fatalf("round 2's block is empty; got (cand!=nil=%v, join=%s, why=%q), want (nil, round, %q)",
			cand != nil, join, why, MissEmptyRound)
	}
	if cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, []LedgerMsg{perRound}); cand == nil {
		t.Fatal("round 1's block has a bullet and still resolves")
	}
	// An empty block still names its round, so a second chain's full block beside it
	// leaves the reply shared rather than this chain's.
	shared := ledgerFix(fixT+300, 1)
	shared.Text += "\n**Commit gate fired (round 2/4)** — (see above)\n" +
		"\n**Commit gate fired (round 2/4)** — *(other repo)*\n- **ACCEPTED** — x → y\n"
	if cand, _, why := r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, []LedgerMsg{shared}); cand != nil || why != MissRoundShared {
		t.Fatalf("two blocks name round 2; got (cand!=nil=%v, why=%q), want (nil, %q)", cand != nil, why, MissRoundShared)
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
	cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixCT, []LedgerMsg{led})
	if cand != nil {
		t.Fatal("the next chain STARTING bounds the search")
	}
	cand, _, _ = r.pickLedger(rows, retryIndexes(rows)[1], fixCT+1000, []LedgerMsg{led})
	if cand == nil || cand.Text != led.Text {
		t.Fatal("the ledger still resolves for chain B")
	}
}

func TestResolverOldShapeFallback(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 2, "")
	cand, join, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixCT+100, []LedgerMsg{ledgerFix(fixCT+250, 2)})
	if join != "round" || cand == nil {
		t.Fatalf("a per-round ledger under the chain era joins on round; got (join=%s, cand!=nil=%v)", join, cand != nil)
	}
}

func TestResolverHistoricalRoundJoin(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixT, 2, "")
	cand, join, _ := r.pickLedger(rows, retryIndexes(rows)[1], fixT+100, []LedgerMsg{ledgerFix(fixT+250, 2)})
	if join != "round" || cand == nil {
		t.Fatalf("rows under the round join keep it; got (join=%s, cand!=nil=%v)", join, cand != nil)
	}
}

func TestResolverCapSection(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixCT, 4, ""),
		resolverRow{"phase": "cap", "ts": fixCT + 400, "firing": 5, "finding_slots": 3, "session_id": fixSession})
	led := chainLedgerFix(fixCT+500, []int{1, 2, 3, 4}, "", 5)
	cand, join, _ := r.pickLedger(rows, len(rows)-1, fixCT+400, []LedgerMsg{led})
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
		cand, join, _ := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), leds)
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
	cand, _, _ := r.pickLedger(rows, 1, fixCT, leds)
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
		if cand, _, _ := r.pickLedger(rows, i, pyNumOr(rows[i]["deny_ts"]), leds); cand != nil {
			t.Fatalf("row %d claimed a block whose name two chains carry", i)
		}
	}
	same := append(chainRows(fixCT, 1, "wip"), chainRows(fixCT+1000, 1, "wip")...)
	leds = []LedgerMsg{chainLedgerFix(fixCT+1400, []int{1}, "wip", 0)}
	if cand, _, _ := r.pickLedger(same, 1, fixCT, leds); cand != nil {
		t.Fatal("a repeated subject names neither chain past its bound")
	}
}

func TestResolverCleanChainSharesNoName(t *testing.T) {
	r := testResolver()
	clean := func(ts float64) resolverRow {
		return resolverRow{"phase": "clean", "ts": ts, "firing": 1, "session_id": fixSession, "subject": "fix: one"}
	}
	late := chainLedgerFix(fixCT+400, []int{1}, "fix: one", 0)
	after := append(chainRows(fixCT, 1, "fix: one"), clean(fixCT+300))
	if cand, _, _ := r.pickLedger(after, 1, fixCT, []LedgerMsg{late}); cand == nil {
		t.Fatal("a clean commit that repeats the subject leaves the name to the chain handed findings")
	}
	before := append([]resolverRow{clean(fixCT - 300)}, chainRows(fixCT, 1, "fix: one")...)
	if cand, _, _ := r.pickLedger(before, 2, fixCT, []LedgerMsg{late}); cand == nil {
		t.Fatal("an earlier clean commit with the subject owes no ledger to compete for")
	}
	abandoned := append(chainRows(fixCT, 1, "fix: one")[:1], chainRows(fixCT+100, 1, "fix: one")...)
	if cand, _, _ := r.pickLedger(abandoned, 2, fixCT+100, []LedgerMsg{late}); cand != nil {
		t.Fatal("a deny with no retry was handed findings, so it still shares the name")
	}
}

func TestResolverPlaceholderChainTakesAnUnnamedBlock(t *testing.T) {
	r := testResolver()
	real := chainLedgerFix(fixCT+400, []int{1}, "feat: the real subject", 0)
	rows := chainRows(fixCT, 1, gate.SubjectNotRecoverable)
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{real}); cand == nil {
		t.Fatal("a chain whose message was unreadable has no subject for the header to contradict")
	}
	earlier := append(chainRows(fixCT-1000, 1, "feat: the real subject"), chainRows(fixCT, 1, gate.SubjectNotRecoverable)...)
	if cand, _, why := r.pickLedger(earlier, 3, fixCT, []LedgerMsg{real}); cand != nil || why != MissSubjectUnmatched {
		t.Fatalf("a subject another chain was handed findings under names that chain; got (cand!=nil=%v, why=%q)", cand != nil, why)
	}
	git := chainRows(fixCT, 1, gate.SubjectUnavailable)
	for _, row := range git {
		delete(row, "session_id")
	}
	if cand, _, _ := r.pickLedger(git, 1, fixCT, []LedgerMsg{real}); cand != nil {
		t.Fatal("a git row's window spans every session, so a named block stays refused")
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
		cand, _, _ := r.pickLedger(rows, 1, fixCT, leds)
		if got := blockHead(cand); got != "feat: first commit" {
			t.Fatalf("the first chain takes its own deferred block; got %q", got)
		}
		cand, _, _ = r.pickLedger(rows, 3, fixCT+1000, leds)
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
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{late}); cand != nil {
		t.Fatal("a git row's shared subject lets no ledger past the chain bound")
	}
	if cand, _, _ := r.pickLedger(rows, 3, fixCT+1000, []LedgerMsg{late}); cand == nil {
		t.Fatal("the chain the ledger landed inside still takes it")
	}
	early := chainLedgerFix(fixCT+400, []int{1}, placeholder, 0)
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{early}); cand == nil {
		t.Fatal("a git row keeps its bounded join")
	}
}

// stampRepo names the repository every row was written under. An empty name leaves the
// field off, as a legacy row and a row whose repository git could not read both have it.
func stampRepo(rows []resolverRow, repo string) []resolverRow {
	if repo != "" {
		for _, row := range rows {
			row["repo"] = repo
		}
	}
	return rows
}

func TestResolverInterleavedRepoBoundsNoOtherChain(t *testing.T) {
	r := testResolver()
	late := chainLedgerFix(fixCT+1400, []int{1}, "", 0)
	pair := func(first, second string) []resolverRow {
		return append(stampRepo(chainRows(fixCT, 1, ""), first),
			stampRepo(chainRows(fixCT+1000, 1, ""), second)...)
	}
	if cand, _, why := r.pickLedger(pair("/a", "/b"), 1, fixCT, []LedgerMsg{late}); cand == nil {
		t.Fatalf("another repository's chain does not end this one; got why=%q", why)
	}
	if cand, _, _ := r.pickLedger(pair("/a", "/a"), 1, fixCT, []LedgerMsg{late}); cand != nil {
		t.Fatal("the next chain of the same repository still ends this one")
	}
	for _, names := range [][2]string{{"", "/b"}, {"/a", ""}} {
		if cand, _, _ := r.pickLedger(pair(names[0], names[1]), 1, fixCT, []LedgerMsg{late}); cand != nil {
			t.Fatalf("an unnamed repository keeps the bound; got a ledger for %q then %q", names[0], names[1])
		}
	}
}

// withChainID gives a chain ledger's header the chain ID the gate rendered into it.
func withChainID(led LedgerMsg, id string) LedgerMsg {
	led.Text = strings.Replace(led.Text, "chain ledger,", "chain ledger #"+id+",", 1)
	return led
}

func stampChainID(rows []resolverRow, id string) []resolverRow {
	for _, row := range rows {
		row["chain_id"] = id
	}
	return rows
}

func TestResolverChainIDOutranksTheSubject(t *testing.T) {
	r := testResolver()
	rows := append(stampChainID(chainRows(fixCT, 1, "feat: first commit"), "aaaa0001"),
		stampChainID(chainRows(fixCT+1000, 1, "fix: second commit"), "bbbb0002")...)
	reworded := withChainID(chainLedgerFix(fixCT+1400, []int{1}, "feat: the first one", 0), "aaaa0001")
	cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{reworded})
	if cand == nil {
		t.Fatal("the chain ID claims a deferred block whatever its subject says")
	}
	if cand, _, _ = r.pickLedger(rows, 3, fixCT+1000, []LedgerMsg{reworded}); cand != nil {
		t.Fatal("another chain's ID is never this chain's, inside its bound or not")
	}
	wrongID := withChainID(chainLedgerFix(fixCT+400, []int{1}, "feat: first commit", 0), "cccc0003")
	if cand, _, _ = r.pickLedger(rows, 1, fixCT, []LedgerMsg{wrongID}); cand != nil {
		t.Fatal("a conflicting ID is refused even when the subject matches")
	}
}

func TestResolverChainIDAbsentOnOneSide(t *testing.T) {
	r := testResolver()
	legacy := chainRows(fixCT, 1, "feat: first commit")
	withID := withChainID(chainLedgerFix(fixCT+400, []int{1}, "feat: first commit", 0), "aaaa0001")
	if cand, _, _ := r.pickLedger(legacy, 1, fixCT, []LedgerMsg{withID}); cand != nil {
		t.Fatal("a block carrying an ID answers a chain that has one, never a legacy chain")
	}
	stamped := stampChainID(chainRows(fixCT, 1, "feat: first commit"), "aaaa0001")
	dropped := chainLedgerFix(fixCT+400, []int{1}, "feat: first commit", 0)
	if cand, _, _ := r.pickLedger(stamped, 1, fixCT, []LedgerMsg{dropped}); cand == nil {
		t.Fatal("a header whose ID the agent dropped still joins by its subject")
	}
}

func TestResolverChainIDJoinsAGitRowPastItsBound(t *testing.T) {
	r := testResolver()
	const placeholder = "(not available to a pre-commit hook — judge the diff alone)"
	rows := append(stampChainID(chainRows(fixCT, 1, placeholder), "aaaa0001"),
		stampChainID(chainRows(fixCT+1000, 1, placeholder), "bbbb0002")...)
	for _, row := range rows {
		delete(row, "session_id")
		row["repo"] = "/r"
	}
	late := withChainID(chainLedgerFix(fixCT+1400, []int{1}, placeholder, 0), "aaaa0001")
	late.Key = "some-session"
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{late}); cand == nil {
		t.Fatal("the ID associates a git row with the session that wrote its ledger")
	}
	if cand, _, _ := r.pickLedger(rows, 3, fixCT+1000, []LedgerMsg{late}); cand != nil {
		t.Fatal("the git chain the ledger landed inside does not take another chain's ID")
	}
}

func TestResolverSubjectAbsentFallsBack(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixCT, 2, "")
	leds := []LedgerMsg{chainLedgerFix(fixCT+400, []int{1, 2}, "feat: unmatched", 0)}
	cand, join, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixCT, leds)
	if join != "chain" || cand == nil {
		t.Fatalf("an unread commit message leaves the row on the bound it had; got (join=%s, cand!=nil=%v)", join, cand != nil)
	}
}

func TestResolverLegacyAndSpans(t *testing.T) {
	r := testResolver()
	rows := chainRows(fixLegacyT, 2, "")
	leds := []LedgerMsg{ledgerFix(fixLegacyT+150, 0), ledgerFix(fixLegacyT+250, 0)}
	_, join, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixLegacyT, leds)
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
	cand, _, _ := r.pickLedger(rows, retryIndexes(rows)[0], fixT, []LedgerMsg{ledgerFixSession(fixT+150, 1, "other")})
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
	cand, join, _ := r.pickLedger(capRows, idx, fixT+400, []LedgerMsg{ledgerFix(fixT+500, 5)})
	if round, _ := candRound(cand); join != "round" || round != 5 {
		t.Fatalf("the terminal ledger joins on its own round; got (join=%s, round=%d)", join, round)
	}
	cand, _, _ = r.pickLedger(capRows, idx, fixT+400, []LedgerMsg{ledgerFix(fixT+500, 4)})
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

func TestResolverIfDueWaitsOutAnAppliedScan(t *testing.T) {
	now := 2_000_000_000.0
	r := testResolver()
	r.cfg.StateDir = t.TempDir()
	r.now = func() float64 { return now }
	owing := func(deny, ts float64) []resolverRow {
		return []resolverRow{
			{"phase": "deny", "ts": deny, "firing": 1, "session_id": fixSession},
			{"phase": "retry", "ts": ts, "deny_ts": deny, "firing": 1, "session_id": fixSession},
		}
	}
	rows := owing(now-3000, now-2990)
	if !r.pending(rows) {
		t.Fatal("a due row with no scan recorded is scanned")
	}
	r.recordScan(now - 60)
	if r.pending(rows) {
		t.Fatal("nothing is new since the scan a minute ago")
	}
	if !r.pending(append(owing(now-3000, now-2990), owing(now-400, now-30)...)) {
		t.Fatal("a row written since the scan is scanned for")
	}
	if !r.pending(owing(now-150, now-140)) {
		t.Fatal("the scan ran inside the row's grace, before its ledger usually lands")
	}
	deadline := now - r.spanFor("chain") - 10
	if !r.pending(owing(deadline, deadline+10)) {
		t.Fatal("a miss deadline passed since the scan, so the miss is stamped now")
	}
	r.recordScan(now - rescanAfter)
	if !r.pending(rows) {
		t.Fatal("a late ledger is looked for once rescanAfter has passed")
	}
}

func TestResolverOnlyAnAppliedScanIsRecorded(t *testing.T) {
	dir := t.TempDir()
	cfg := gate.Config{StateDir: dir, HostID: "testhost", StaleAfter: 30 * time.Minute}
	line, _ := json.Marshal(resolverRow{"phase": "retry", "ts": fixCT + 10, "deny_ts": fixCT,
		"firing": 1, "session_id": fixSession})
	if err := os.WriteFile(cfg.LogFile(), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &resolver{cfg: cfg, quiet: true, now: func() float64 { return 2_000_000_000 },
		harvests: []Harvest{func() []LedgerMsg { return nil }}}
	Backfill(cfg, r.harvests, BackfillOptions{Quiet: true}, nil, r.now)
	if _, ok := r.lastScan(); ok {
		t.Fatal("a dry run stamps no row, so it must not hold off the next automatic run")
	}
	Backfill(cfg, r.harvests, BackfillOptions{Apply: true, Quiet: true}, nil, r.now)
	if ts, ok := r.lastScan(); !ok || ts != 2_000_000_000 {
		t.Fatalf("an applied scan records its start; got (%v, %v)", ts, ok)
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

func TestResolverMissReasons(t *testing.T) {
	r := testResolver()
	one := func(subject string) []resolverRow { return chainRows(fixCT, 1, subject) }
	two := func(start float64, subject string) []resolverRow {
		return append(chainRows(start, 1, subject), chainRows(start+1000, 1, subject)...)
	}
	cases := []struct {
		name string
		rows []resolverRow
		led  []LedgerMsg
		want string
	}{
		{"nothing written", one("feat: x"), nil, MissNoLedger},
		{"another round's header", one("feat: x"), []LedgerMsg{ledgerFix(fixCT+50, 2)}, MissNoHeader},
		{"no section", one("feat: x"), []LedgerMsg{chainLedgerFix(fixCT+50, []int{2}, "feat: x", 0)}, MissNoSection},
		{"after the next chain", two(fixCT, ""), []LedgerMsg{chainLedgerFix(fixCT+1400, []int{1}, "", 0)}, MissPastBound},
		{"reworded subject", one("feat: x"), []LedgerMsg{chainLedgerFix(fixCT+50, []int{1}, "feat: y", 0)}, MissSubjectUnmatched},
		{"repeated subject", two(fixCT, "wip"), []LedgerMsg{chainLedgerFix(fixCT+1400, []int{1}, "wip", 0)}, MissSubjectShared},
		{"another chain's ID", stampChainID(one("feat: x"), "aaaa0001"),
			[]LedgerMsg{withChainID(chainLedgerFix(fixCT+50, []int{1}, "feat: x", 0), "bbbb0002")}, MissIDConflict},
		{"strongest block wins", one("feat: x"), []LedgerMsg{{fixCT + 50, fixSession, "/t.jsonl",
			chainLedgerFix(0, []int{1}, "feat: y", 0).Text + "\n" + chainLedgerFix(0, []int{2}, "feat: x", 0).Text}}, MissSubjectUnmatched},
		{"round era, no header for the round", chainRows(fixT, 2, ""), []LedgerMsg{ledgerFix(fixT+150, 1)}, MissNoRound},
		{"round era, after the next chain", two(fixT, ""), []LedgerMsg{ledgerFix(fixT+1100, 1)}, MissPastBound},
		{"window era", chainRows(fixLegacyT, 1, ""), nil, MissNoLedger},
	}
	for _, c := range cases {
		i := retryIndexes(c.rows)[0]
		if c.name == "round era, no header for the round" {
			i = retryIndexes(c.rows)[1]
		}
		cand, _, why := r.pickLedger(c.rows, i, pyNumOr(c.rows[i]["deny_ts"]), c.led)
		if cand != nil || why != c.want {
			t.Errorf("%s: got (cand!=nil=%v, why=%q), want (nil, %q)", c.name, cand != nil, why, c.want)
		}
	}
}

// multiRoundFix is the shape of a round-era reply that answered a whole chain at once:
// a header per round, round n carrying n bullets, then a terminal round and prose.
func multiRoundFix(when float64) LedgerMsg {
	text := "All four rounds, adjudicated together.\n"
	for n := 1; n <= 4; n++ {
		text += fmt.Sprintf("\n**Commit gate fired (round %d/4)** — 3 reviewers, %d raw findings, %d distinct concerns.\n", n, n, n)
		for k := 0; k < n; k++ {
			text += fmt.Sprintf("- **REJECTED** — round %d concern %d → no change\n", n, k)
		}
	}
	text += "\n**Commit gate fired (round 5, terminal)** — 2 reviewers, 1 raw finding, 1 distinct concern. Commit not blocked.\n" +
		"- **ACCEPTED** — the terminal concern → fixed\n\n**Every fix was re-verified.** The suite passes.\n"
	return LedgerMsg{When: when, Key: fixSession, Path: "/t.jsonl", Text: text}
}

func TestResolverOneReplyAnswersEachRound(t *testing.T) {
	r := testResolver()
	rows := append(chainRows(fixT, 4, ""), resolverRow{"phase": "cap", "ts": fixT + 400, "firing": 5,
		"session_id": fixSession, "finding_slots": 1})
	led := multiRoundFix(fixT + 900)
	for _, i := range append(retryIndexes(rows), len(rows)-1) {
		firing, _ := pyInt(rows[i]["firing"])
		anchor, _ := anchorTS(rows, i)
		cand, join, _ := r.pickLedger(rows, i, anchor, []LedgerMsg{led})
		if cand == nil || join != "round" {
			t.Fatalf("round %d: a reply with a header per round answers it; got join=%s", firing, join)
		}
		parsed := gate.ParseLedgerText(cand.Text)
		rejected, accepted := firing, 0
		if firing == 5 {
			rejected, accepted = 0, 1
		}
		if parsed["self_reported_rejected"] != rejected || parsed["self_reported_accepted"] != accepted {
			t.Fatalf("round %d carries only its own bullets; got %v", firing, parsed)
		}
	}
	twoChains := multiRoundFix(fixT + 900)
	twoChains.Text += "\n**Commit gate fired (round 2/4)** — 3 reviewers, 1 raw finding, 1 distinct concern. *(other repo)*\n- **ACCEPTED** — x → y\n"
	cand, _, why := r.pickLedger(rows, 3, fixT+100, []LedgerMsg{twoChains})
	if cand != nil || why != MissRoundShared {
		t.Fatalf("a reply naming round 2 twice answered two chains; got (cand!=nil=%v, why=%q)", cand != nil, why)
	}
	var attempts []resolverRow
	for n := 0; n < 4; n++ {
		attempts = append(attempts, chainRows(fixT+float64(n)*300, 1, "")...)
	}
	cand, _, why = r.pickLedger(attempts, 7, fixT+900, []LedgerMsg{led})
	if cand != nil || why != MissRoundsUnmatched {
		t.Fatalf("a reply numbering four attempts the gate kept as four chains names none of them; got (cand!=nil=%v, why=%q)", cand != nil, why)
	}
}

func TestResolverStampsTheMissReason(t *testing.T) {
	r := testResolver()
	r.quiet = true
	var leds []LedgerMsg
	r.harvests = []Harvest{func() []LedgerMsg { return leds }}
	rows := chainRows(fixCT, 1, "feat: x")
	if res := r.resolveAndWrite(rows, len(rows), false); res.Changed != 1 || rows[1]["ledger_miss"] != MissNoLedger {
		t.Fatalf("a miss carries its reason; got changed=%d, ledger_miss=%v", res.Changed, rows[1]["ledger_miss"])
	}
	r.reopen(rows)
	if res := r.resolveAndWrite(rows, len(rows), false); res.Changed != 0 {
		t.Fatalf("a recheck that finds the same miss changes nothing; got %d", res.Changed)
	}
	leds = []LedgerMsg{chainLedgerFix(fixCT+50, []int{1}, "feat: y", 0)}
	r.reopen(rows)
	if res := r.resolveAndWrite(rows, len(rows), false); res.Changed != 1 || rows[1]["ledger_miss"] != MissSubjectUnmatched {
		t.Fatalf("a recheck with a new reason restamps it; got changed=%d, ledger_miss=%v", res.Changed, rows[1]["ledger_miss"])
	}
	leds = []LedgerMsg{chainLedgerFix(fixCT+50, []int{1}, "feat: x", 0)}
	r.reopen(rows)
	r.resolveAndWrite(rows, len(rows), false)
	if _, ok := rows[1]["ledger_miss"]; ok || rows[1]["ledger_found"] != true {
		t.Fatalf("a found ledger clears the reason; got %v", rows[1])
	}
}

func TestResolverRejoinJudgesResolverLinksAgain(t *testing.T) {
	dir := t.TempDir()
	cfg := gate.Config{StateDir: dir, HostID: "testhost", StaleAfter: 30 * time.Minute}
	reply := multiRoundFix(fixT + 400)
	link := func(row resolverRow, text string, ts any) resolverRow {
		for k, v := range gate.ParseLedgerText(text) {
			row[k] = v
		}
		row["ledger_found"], row["backfilled"], row["ledger_join"] = true, true, "round"
		if ts != nil {
			row["ledger_ts"] = ts
		}
		return row
	}
	inSession := func(rows []resolverRow, session string) []resolverRow {
		for _, row := range rows {
			row["session_id"] = session
		}
		return rows
	}
	a := append(chainRows(fixT, 4, ""), resolverRow{"phase": "cap", "ts": fixT + 390, "firing": 5,
		"session_id": fixSession, "finding_slots": 1})
	blocks := gate.RoundBlocks(reply.Text)
	link(a[1], reply.Text, int64(reply.When))
	a[3]["ledger_found"], a[3]["backfilled"], a[3]["ledger_join"] = false, true, "round"
	for row, block := range map[int]int{5: 2, 7: 3, 8: 4} {
		link(a[row], blocks[block], int64(reply.When))
	}
	delete(a[7], "concerns_total")
	hook := inSession(chainRows(fixT+5000, 1, ""), "s2")
	link(hook[1], reply.Text, nil)
	gone := inSession(chainRows(fixT+10000, 1, ""), "s3")
	link(gone[1], reply.Text, int64(fixT+10400))
	shared := inSession(chainRows(fixT+15000, 1, ""), "s4")
	twoChains := "**Commit gate fired (round 1/4)** — one.\n- **ACCEPTED** — a → b\n\n" +
		"**Commit gate fired (round 1/4)** — two.\n- **REJECTED** — c → d\n"
	link(shared[1], twoChains, int64(fixT+15400))
	early := inSession(chainRows(ledgerRequiredTS-1000, 1, ""), "s5")
	link(early[1], reply.Text, int64(ledgerRequiredTS-600))

	var rows []resolverRow
	for _, chain := range [][]resolverRow{a, hook, gone, shared, early} {
		rows = append(rows, chain...)
	}
	var sb strings.Builder
	for _, row := range rows {
		line, _ := json.Marshal(row)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(cfg.LogFile(), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	harvest := []Harvest{func() []LedgerMsg {
		return []LedgerMsg{reply, {When: fixT + 15400, Key: "s4", Path: "/t.jsonl", Text: twoChains}}
	}}
	now := func() float64 { return 2_000_000_000 }
	opts := BackfillOptions{Apply: true, Quiet: true, Rejoin: true}
	if res := Backfill(cfg, harvest, opts, nil, now); res.Changed != 4 {
		t.Fatalf("a re-split link, a new link, a refused link and a stale count change; the identical ones do not; got %d", res.Changed)
	}
	after := readShard(t, cfg.LogFile())
	counts := func(row resolverRow) string {
		return fmt.Sprintf("%v %v/%v", row["ledger_found"], row["self_reported_accepted"], row["self_reported_rejected"])
	}
	for _, c := range []struct {
		why  string
		row  resolverRow
		want string
	}{
		{"a stale link keeps only its round's block", after[1], "true 0/1"},
		{"a stamped miss is re-examined too", after[3], "true 0/2"},
		{"an unchanged link stays", after[5], "true 0/3"},
		{"a hook-time capture is not the resolver's to re-judge", after[10], "true 1/10"},
		{"a link with no ledger left to read is kept", after[12], "true 1/10"},
		{"a link the current rule refuses becomes a miss", after[14], "false <nil>/<nil>"},
		{"a link from before the mandate is kept", after[16], "true 1/10"},
	} {
		if got := counts(c.row); got != c.want {
			t.Errorf("%s: got %s, want %s", c.why, got, c.want)
		}
	}
	if after[7]["concerns_total"] == nil {
		t.Error("a link whose concerns match but whose count is stale takes the current count")
	}
	if after[16]["ledger_expected"] != nil {
		t.Errorf("a kept pre-mandate link is not restamped; got ledger_expected=%v", after[16]["ledger_expected"])
	}
	if after[14]["ledger_miss"] != MissRoundShared {
		t.Errorf("the refused link records why; got %v", after[14]["ledger_miss"])
	}
	for _, row := range after {
		if _, ok := row["_rejoin"]; ok {
			t.Fatal("the kept link never reaches the shard")
		}
	}
	if res := Backfill(cfg, harvest, opts, nil, now); res.Changed != 0 {
		t.Fatalf("a second rejoin over current links changes nothing; got %d", res.Changed)
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

// sequenceWithRelease is a two-round sequence whose cap handed the agent findings, then
// a one-round sequence, each followed by its release row when released is true.
func sequenceWithRelease(released bool) []resolverRow {
	first := stampChainID(chainRows(fixCT, 2, "feat: first commit"), "aaaa0001")
	first = append(first, resolverRow{"phase": "cap", "ts": fixCT + 300, "firing": 3, "chain_id": "aaaa0001",
		"session_id": fixSession, "subject": "feat: first commit", "finding_slots": 1})
	second := stampChainID(chainRows(fixCT+1000, 1, "fix: second commit"), "bbbb0002")
	release := func(id string, ts float64) resolverRow {
		return resolverRow{"phase": gate.PhaseChainEnd, "ts": ts, "chain_id": id, "session_id": fixSession,
			"blocks": 2, "ledger_header": "**Commit gate fired — review sequence #" + id + ", x**"}
	}
	var rows []resolverRow
	rows = append(rows, first...)
	if released {
		rows = append(rows, release("aaaa0001", fixCT+301))
	}
	rows = append(rows, second...)
	if released {
		rows = append(rows, release("bbbb0002", fixCT+1011))
	}
	return rows
}

// repairedLedger is one final reply answering both sequences, each round with its own
// concern.
func repairedLedger() LedgerMsg {
	text := "**Commit gate fired — review sequence #aaaa0001, feat: the first one, reworded**\n\n" +
		"**Round 1, blocking** — 3 reviewers.\n\n- **REJECTED** — round one concern → false premise\n\n" +
		"**Round 2, blocking** — 3 reviewers.\n\n- **ACCEPTED** — round two concern → fixed\n\n" +
		"**Round 3, terminal** — 3 reviewers. Commit not blocked.\n\n- **ACCEPTED, not fixed** — terminal concern → left\n\n" +
		"**Commit gate fired — review sequence #bbbb0002, fix: second commit**\n\n" +
		"**Round 1, blocking** — 3 reviewers.\n\n- **REJECTED** — second sequence concern → false premise\n"
	return LedgerMsg{When: fixCT + 1100, Key: fixSession, Path: "/t.jsonl", Text: text}
}

// Release rows are non-review rows: beside a sequence's rows they move no join and no
// unresolved count, and a backfill rewrite keeps them.
//
// pins existing behaviour
func TestResolverReleaseRowsChangeNoJoin(t *testing.T) {
	resolved := func(released bool) map[string][]string {
		r := testResolver()
		r.quiet = true
		r.harvests = []Harvest{func() []LedgerMsg { return []LedgerMsg{repairedLedger()} }}
		rows := sequenceWithRelease(released)
		r.resolveAndWrite(rows, len(rows), false)
		out := map[string][]string{}
		for _, row := range rows {
			if ExpectsLedger(row) {
				key := fmt.Sprint(row["chain_id"], "/", row["firing"])
				var concerns []string
				list, _ := row["concerns"].([]gate.Concern)
				for _, c := range list {
					concerns = append(concerns, c.Verdict+": "+c.Concern)
				}
				out[key] = append(concerns, fmt.Sprint(row["ledger_found"], row["ledger_join"]))
			}
		}
		return out
	}
	plain, withRelease := resolved(false), resolved(true)
	if fmt.Sprint(plain) != fmt.Sprint(withRelease) {
		t.Errorf("release rows changed the joins:\n without %v\n with    %v", plain, withRelease)
	}
	want := map[string]string{
		"aaaa0001/1": "[REJECTED: round one concern truechain]",
		"aaaa0001/2": "[ACCEPTED: round two concern truechain]",
		"aaaa0001/3": "[ACCEPTED: terminal concern truechain]",
		"bbbb0002/1": "[REJECTED: second sequence concern truechain]",
	}
	for key, w := range want {
		if got := fmt.Sprint(withRelease[key]); got != w {
			t.Errorf("%s resolved to %s, want %s", key, got, w)
		}
	}

	counts := map[bool]int{}
	for _, released := range []bool{false, true} {
		cfg := gate.Config{StateDir: t.TempDir(), HostID: "testhost", StaleAfter: 30 * time.Minute}
		var sb strings.Builder
		for _, row := range sequenceWithRelease(released) {
			line, _ := json.Marshal(row)
			sb.Write(line)
			sb.WriteByte('\n')
		}
		if err := os.WriteFile(cfg.LogFile(), []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		counts[released] = CountUnresolved(cfg)
		harvest := []Harvest{func() []LedgerMsg { return []LedgerMsg{repairedLedger()} }}
		Backfill(cfg, harvest, BackfillOptions{Apply: true, Quiet: true}, nil, func() float64 { return 2_000_000_000 })
		if !released {
			continue
		}
		kept := 0
		for _, row := range readShard(t, cfg.LogFile()) {
			if row["phase"] == gate.PhaseChainEnd {
				kept++
			}
		}
		if kept != 2 {
			t.Errorf("the rewrite kept %d release rows, want 2", kept)
		}
	}
	if counts[false] != counts[true] || counts[true] != 4 {
		t.Errorf("unresolved counts = %v, want 4 with and without release rows", counts)
	}
}

// An exact chain ID does not carry a ledger across thread keys: a parent's reply never
// resolves a child thread's rows.
//
// pins existing behaviour
func TestResolverChainIDStaysInsideItsThread(t *testing.T) {
	r := testResolver()
	rows := stampChainID(chainRows(fixCT, 1, "fix: thread commit"), "aaaa0001")
	for _, row := range rows {
		row["thread_id"] = "agent-9"
	}
	parent := withChainID(chainLedgerFix(fixCT+50, []int{1}, "fix: thread commit", 0), "aaaa0001")
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{parent}); cand != nil {
		t.Fatal("the parent's ledger resolved the child thread's row by its chain ID")
	}
	parent.Key = "agent-9"
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{parent}); cand == nil {
		t.Fatal("the thread's own ledger did not resolve its row")
	}
}

// An exact chain ID joins only inside the chain window, 8 x StaleAfter from the deny
// the row answers.
//
// pins existing behaviour
func TestResolverChainIDJoinsInsideTheChainWindow(t *testing.T) {
	r := testResolver()
	rows := stampChainID(chainRows(fixCT, 1, "feat: commit"), "aaaa0001")
	span := 8 * r.cfg.StaleAfter.Seconds()
	inside := withChainID(chainLedgerFix(fixCT+span, []int{1}, "feat: commit", 0), "aaaa0001")
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{inside}); cand == nil {
		t.Fatal("a ledger at the window's edge did not join")
	}
	past := withChainID(chainLedgerFix(fixCT+span+1, []int{1}, "feat: commit", 0), "aaaa0001")
	if cand, _, _ := r.pickLedger(rows, 1, fixCT, []LedgerMsg{past}); cand != nil {
		t.Fatal("a ledger past the chain window joined by its ID")
	}
}
