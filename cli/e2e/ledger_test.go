//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ledgerHeaderRe reads the chain ID and subject from the header line a deny tells the
// agent to copy.
var ledgerHeaderRe = regexp.MustCompile(`\*\*Commit gate fired — review sequence #([0-9a-f]+), ([^*]+)\*\*`)

// DenyThenRetry runs one review chain in session on the Claude Code gate: a staged
// file draws a finding from every reviewer, then the unchanged retry ends the chain.
// It returns the chain ID the deny's ledger header carries.
func (g *NativeGate) DenyThenRetry(session, file, subject string) string {
	g.s.t.Helper()
	g.s.Script("finding", "finding", "finding")
	g.s.StageCode(file)
	command := "git commit -m '" + subject + "'"
	deny := g.hookAs(session, command, "")
	m := ledgerHeaderRe.FindStringSubmatch(deny.Text())
	if !deny.Denied() || m == nil || m[2] != subject {
		g.s.t.Fatalf("the first review did not deny with a ledger header for %q:\n%s", subject, deny.Text())
	}
	g.s.Script("clean")
	if retry := g.hookAs(session, command, ""); retry.Denied() || !strings.Contains(retry.Text(), "the review sequence ended") {
		g.s.t.Fatalf("the unchanged retry did not end the chain:\n%s", retry.Text())
	}
	return m[1]
}

// chainLedger is the agent's ledger for a one-round chain: a header naming the chain
// and subject, then one REJECTED bullet per concern.
func chainLedger(id, subject string, concerns ...string) string {
	header := "**Commit gate fired — review sequence"
	if id != "" {
		header += " #" + id
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s, %s**\n\n**Round 1, blocking** — 3 reviewers, 3 raw findings, %d distinct concerns.\n\n", header, subject, len(concerns))
	for _, c := range concerns {
		fmt.Fprintf(&b, "- **REJECTED** — %s → the premise is false: the code does not do that\n", c)
	}
	b.WriteString("- Weakest surviving: none survived.\n")
	return b.String()
}

// SeedReplies writes session's transcript as one prompt followed by one assistant text
// message per reply, the first at start and each one second after the last.
func (s *Sandbox) SeedReplies(session string, start time.Time, replies ...string) {
	s.t.Helper()
	stamp := func(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000Z") }
	lines := []map[string]any{{"type": "user", "uuid": "u0", "sessionId": session, "cwd": s.Repo,
		"timestamp": stamp(start.Add(-time.Second)), "message": map[string]any{"role": "user", "content": "commit the change"}}}
	for i, text := range replies {
		lines = append(lines, map[string]any{"type": "assistant", "uuid": fmt.Sprintf("a%d", i), "sessionId": session,
			"cwd": s.Repo, "timestamp": stamp(start.Add(time.Duration(i) * time.Second)),
			"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}})
	}
	s.writeTranscript(s.SessionPath(session), lines)
}

// LedgerRow is the one row of the ledger shard that expects an agent ledger.
func (s *Sandbox) LedgerRow() map[string]any {
	s.t.Helper()
	var found []map[string]any
	for _, row := range s.LedgerRows() {
		if row["ledger_expected"] == true {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		s.t.Fatalf("%d ledger rows expect a ledger, want 1: %v", len(found), s.LedgerRows())
	}
	return found[0]
}

// AgeLedger moves every row of the ledger shard back by d and returns the moved time
// of the first row, so a resolver run judges rows whose ledger window has closed.
func (s *Sandbox) AgeLedger(d time.Duration) time.Time {
	s.t.Helper()
	path := filepath.Join(s.Home, ".steer", "state", "commit-review", "adjudication.e2e.jsonl")
	rows := s.LedgerRows()
	var b strings.Builder
	for _, row := range rows {
		for _, k := range []string{"ts", "deny_ts", "cleared_ts", "ledger_ts"} {
			if v, ok := row[k].(float64); ok {
				row[k] = v - d.Seconds()
			}
		}
		data, _ := json.Marshal(row)
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		s.t.Fatal(err)
	}
	return time.Unix(int64(rows[0]["ts"].(float64)), 0)
}

// concernsOf reads a row's stored concern texts.
func concernsOf(row map[string]any) []string {
	var out []string
	list, _ := row["concerns"].([]any)
	for _, c := range list {
		if m, ok := c.(map[string]any); ok {
			out = append(out, fmt.Sprint(m["verdict"], ": ", m["concern"]))
		}
	}
	return out
}

// TestReviewFindingsJoinByChainID checks that a review chain's findings count only once
// the agent's ledger rules on them, and that the ledger joins its chain by the chain ID
// in its header even when the agent reworded the subject.
//
// pins existing behaviour
func TestReviewFindingsJoinByChainID(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	id := g.DenyThenRetry("sess-1", "mod.py", "add mod")

	for _, regex := range []string{"stub finding", "names no failing input"} {
		if r := s.MustSteer("fix", "match", regex, "--kind", "both"); !strings.Contains(r.Stdout, "matches 0/") {
			t.Errorf("before any ledger, %q matched a chain:\n%s", regex, r.Stdout)
		}
	}

	s.SeedReplies("sess-1", time.Now(), chainLedger(id, "add the mod module", "the finding names no failing input"))
	s.MustSteer("commit-gate", "backfill", "--apply")
	row := s.LedgerRow()
	if row["ledger_found"] != true || row["ledger_join"] != "chain" || row["chain_id"] != id || row["ledger_round"] != float64(1) {
		t.Fatalf("the ledger did not join chain %s: %v", id, row)
	}
	if got := concernsOf(row); fmt.Sprint(got) != "[REJECTED: the finding names no failing input]" {
		t.Errorf("concerns = %v", got)
	}
	if r := s.MustSteer("fix", "match", "names no failing input", "--kind", "rejected"); !strings.Contains(r.Stdout, "matches 1/1 chains") {
		t.Errorf("the adjudicated concern does not count its chain:\n%s", r.Stdout)
	}
	if r := s.MustSteer("fix", "match", "stub finding", "--kind", "both"); !strings.Contains(r.Stdout, "matches 0/") {
		t.Errorf("the reviewer's own claim counted without a verdict:\n%s", r.Stdout)
	}
}

// TestALedgerNamingAnUnfiredRoundIsAMiss checks that once the ledger window closes, a
// reply whose per-round headers name a round the chain never fired is stamped
// rounds-unmatched rather than joined.
//
// pins existing behaviour
func TestALedgerNamingAnUnfiredRoundIsAMiss(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	g.DenyThenRetry("sess-1", "mod.py", "add mod")
	deny := s.AgeLedger(5 * time.Hour)
	s.SeedReplies("sess-1", deny.Add(10*time.Second),
		"**Commit gate fired — add mod (round 1)**\n- **REJECTED** — the finding names no failing input → it is false\n\n"+
			"**Commit gate fired — add mod (round 2)**\n- **REJECTED** — another finding → it is false\n")
	s.MustSteer("commit-gate", "backfill", "--apply")
	if row := s.LedgerRow(); row["ledger_found"] == true || row["ledger_miss"] != "rounds-unmatched" {
		t.Errorf("row = %v, want a rounds-unmatched miss", row)
	}
}

// TestBackfillRejoinRejudgesTheResolversLinks checks that a plain backfill never reopens
// a found row, that --rejoin replaces a link with the ledger the current rules pick,
// and that --rejoin turns a link the rules now refuse into a miss with its reason.
//
// pins existing behaviour
func TestBackfillRejoinRejudgesTheResolversLinks(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	id := g.DenyThenRetry("sess-1", "mod.py", "add mod")
	first := chainLedger(id, "add mod", "the first concern")
	second := chainLedger(id, "add mod", "the corrected concern")

	start := time.Now()
	s.SeedReplies("sess-1", start, first)
	s.MustSteer("commit-gate", "backfill", "--apply")
	if got := concernsOf(s.LedgerRow()); fmt.Sprint(got) != "[REJECTED: the first concern]" {
		t.Fatalf("the first ledger did not link: %v", got)
	}

	s.SeedReplies("sess-1", start, first, second)
	s.MustSteer("commit-gate", "backfill", "--apply")
	if got := concernsOf(s.LedgerRow()); fmt.Sprint(got) != "[REJECTED: the first concern]" {
		t.Errorf("a plain backfill reopened a found row: %v", got)
	}
	s.MustSteer("commit-gate", "backfill", "--apply", "--rejoin")
	if got := concernsOf(s.LedgerRow()); fmt.Sprint(got) != "[REJECTED: the corrected concern]" {
		t.Errorf("--rejoin did not replace the link with the later ledger: %v", got)
	}

	deny := s.AgeLedger(5 * time.Hour)
	s.SeedReplies("sess-1", deny.Add(10*time.Second), chainLedger("0badc0de", "add mod", "the corrected concern"))
	s.MustSteer("commit-gate", "backfill", "--apply")
	if row := s.LedgerRow(); row["ledger_found"] != true {
		t.Errorf("a plain backfill dropped a found link: %v", row)
	}
	s.MustSteer("commit-gate", "backfill", "--apply", "--rejoin")
	if row := s.LedgerRow(); row["ledger_found"] == true || row["ledger_miss"] != "id-conflict" {
		t.Errorf("row = %v, want --rejoin to refuse the link as id-conflict", row)
	}
}

// TestGuardChainReviewFindingsCorpus drives Suite A step 7 on the review-findings
// corpus: the class counts adjudicated chains on each side of the install timestamp,
// and the guard's firings count over the window period with no session claim.
//
// pins existing behaviour
func TestGuardChainReviewFindingsCorpus(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	s.RegisterNoVar("claude-code")
	const concern = "the change declares a variable with var"

	s.FireNoVar("sess-before")
	id := g.DenyThenRetry("sess-1", "mod.py", "add mod")
	s.SeedReplies("sess-1", time.Now(), chainLedger(id, "add mod", concern))
	s.MustSteer("commit-gate", "backfill", "--apply")
	installed := nextSecond()

	scope := []string{"--scope-repo-root", s.Repo, "--scope-harness", "claude-code", "--installed-ts", fmt.Sprint(installed)}
	match := s.MustSteer(append([]string{"fix", "match", "declares a variable with var", "--kind", "accepted"}, scope...)...)
	if !strings.Contains(match.Stdout, "registration baseline: 0/1") {
		t.Errorf("a REJECTED concern counted toward a defect baseline:\n%s", match.Stdout)
	}
	match = s.MustSteer(append([]string{"fix", "match", "declares a variable with var", "--kind", "rejected"}, scope...)...)
	if !strings.Contains(match.Stdout, "registration baseline: 1/1 chains") {
		t.Errorf("fix match did not preview a 1/1 chain baseline:\n%s", match.Stdout)
	}
	s.MustSteer(append([]string{"fix", "register", "--tier", "guard", "--hook-id", noVarHook,
		"--corpus", "review-findings", "--kind", "false-positive", "--class", "var-finding",
		"--match", "declares a variable with var",
		"--problem", "Reviewers flag var declarations the agent then rejects.",
		"--intervention", "A guard blocks var declarations before they reach review.",
		"--target", ".steer/hooks/" + noVarHook + ".py"}, scope...)...)

	s.FireNoVar("sess-after")
	id = g.DenyThenRetry("sess-2", "mod2.py", "add mod2")
	s.SeedReplies("sess-2", time.Now(), chainLedger(id, "add mod2", concern))
	s.MustSteer("commit-gate", "backfill", "--apply")
	obs := s.scoredObservation()
	for path, v := range map[string]any{
		"counts.unit":         "chain",
		"counts.split_ts":     float64(installed),
		"counts.before.hits":  float64(1),
		"counts.after.hits":   float64(1),
		"firing.applicable":   true,
		"firing.log_present":  true,
		"firing.scope":        "window_period",
		"firing.class_scoped": false,
		"firing.rows":         float64(1),
		"firing.units":        float64(0),
	} {
		if got := dig(obs, strings.Split(path, ".")...); got != v {
			t.Errorf("observation %s = %v, want %v", path, got, v)
		}
	}
}
