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

	"github.com/steer-sh/steer-cli/internal/gate"
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

// TestReportRefreshReadsALedgerNoStopHookReadYet checks that a refresh run right after a
// review joins the agent's ledger before it writes the queue, as no turn-end resolver
// has run since the ledger was written.
//
// pins existing behaviour
func TestReportRefreshReadsALedgerNoStopHookReadYet(t *testing.T) {
	t.Parallel()
	s, g := newNativeGate(t, "claude-code", ".claude/settings.json", "commit-gate review")
	id := g.DenyThenRetry("sess-1", "mod.py", "add mod")
	s.SeedReplies("sess-1", time.Now(), chainLedger(id, "add mod", "the finding names no failing input"))

	s.MustSteer("report", "refresh", "--days", "7")
	if row := s.LedgerRow(); row["ledger_found"] != true || row["chain_id"] != id {
		t.Fatalf("refresh wrote the queue before the ledger joined chain %s: %v", id, row)
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
	register := append([]string{"fix", "register", "--tier", "guard", "--hook-id", noVarHook,
		"--corpus", "review-findings", "--kind", "false-positive", "--class", "var-finding",
		"--match", "declares a variable with var",
		"--problem", "Reviewers flag var declarations the agent then rejects.",
		"--intervention", "A guard blocks var declarations before they reach review.",
		"--target", ".steer/hooks/" + noVarHook + ".py"}, scope...)
	if r := s.Steer(register...); r.Code == 0 || !strings.Contains(r.Output(), "already blocked work on this machine") {
		t.Fatalf("register accepted a guard whose block precedes the cutoff:\n%s", r.Output())
	}
	s.MustSteer(append(register, "--force")...)

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

// backstopSession is the session the backstop cases run in; Codex reads it as the
// rollout's own id.
const backstopSession = "0199a000-0000-7000-8000-000000000042"

// sessionTranscript is where the harness keeps session's transcript, which its
// payloads name as transcript_path.
func (g *NativeGate) sessionTranscript(session string) string {
	if g.harness == "codex" {
		now := time.Now().UTC()
		return filepath.Join(g.s.Home, ".codex", "sessions", now.Format("2006/01/02"),
			"rollout-"+now.Format("2006-01-02T15-04-05")+"-"+session+".jsonl")
	}
	return g.s.SessionPath(session)
}

// turnEntry is one assistant message: reply text, or reasoning when thought is set.
type turnEntry struct {
	text    string
	thought bool
}

// writeTurn writes session's transcript in the harness's shape: one user prompt, then
// one assistant message per entry.
func (g *NativeGate) writeTurn(session string, entries ...turnEntry) {
	g.s.t.Helper()
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	var lines []map[string]any
	if g.harness == "codex" {
		lines = append(lines, map[string]any{"timestamp": ts, "ordinal": 0, "type": "session_meta",
			"payload": map[string]any{"id": session, "session_id": session, "cwd": g.s.Repo}})
		for i, e := range entries {
			payload := map[string]any{"type": "message", "role": "assistant", "phase": "final_answer",
				"content": []any{map[string]any{"type": "output_text", "text": e.text}}}
			if e.thought {
				payload = map[string]any{"type": "reasoning",
					"summary": []any{map[string]any{"type": "summary_text", "text": e.text}}}
			}
			lines = append(lines, map[string]any{"timestamp": ts, "ordinal": i + 1, "type": "response_item", "payload": payload})
		}
	} else {
		lines = append(lines, map[string]any{"type": "user", "sessionId": session, "cwd": g.s.Repo, "timestamp": ts,
			"message": map[string]any{"role": "user", "content": "commit both changes"}})
		for _, e := range entries {
			block := map[string]any{"type": "text", "text": e.text}
			if e.thought {
				block = map[string]any{"type": "thinking", "thinking": e.text}
			}
			lines = append(lines, map[string]any{"type": "assistant", "sessionId": session, "cwd": g.s.Repo, "timestamp": ts,
				"message": map[string]any{"role": "assistant", "content": []any{block}}})
		}
	}
	g.s.writeTranscript(g.transcript, lines)
}

// releaseChain runs one review sequence: a staged file draws a finding from every
// reviewer, then the unchanged retry releases the commit. It returns the header line
// the deny told the agent to copy, and the retry's reply.
func (g *NativeGate) releaseChain(session, file, subject string) (string, HookReply) {
	g.s.t.Helper()
	g.s.Script("finding", "finding", "finding")
	g.s.StageCode(file)
	command := "git -C " + g.s.Repo + " commit -m '" + subject + "'"
	deny := g.hookAs(session, command, g.s.Repo)
	m := ledgerHeaderRe.FindString(deny.Text())
	if !deny.Denied() || m == "" {
		g.s.t.Fatalf("the first review did not deny with a ledger header for %q:\n%s", subject, deny.Text())
	}
	g.s.Script("clean")
	retry := g.hookAs(session, command, g.s.Repo)
	if retry.Denied() || !strings.Contains(retry.Text(), "the review sequence ended") {
		g.s.t.Fatalf("the unchanged retry did not end the sequence:\n%s", retry.Text())
	}
	return m, retry
}

// ResolverStopReply is the parsed reply of the resolver's Stop hook, and its raw stdout.
type ResolverStopReply struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	System   string `json:"systemMessage"`
	Raw      string `json:"-"`
}

// stop sends a Stop payload through the registered resolver command. fields override
// a root Stop of the gate's turn with stop_hook_active false; a nil value drops a field.
func (g *NativeGate) stop(session string, fields map[string]any) ResolverStopReply {
	g.s.t.Helper()
	config := ".claude/settings.json"
	if g.harness == "codex" {
		config = ".codex/hooks.json"
	}
	command := g.s.RegisteredCommand(filepath.Join(g.s.Repo, config), "commit-gate backfill")
	payload := map[string]any{"hook_event_name": "Stop", "session_id": session, "cwd": g.s.Repo,
		"transcript_path": g.transcript, g.turnKey(): g.turn, "stop_hook_active": false}
	for k, v := range fields {
		if v == nil {
			delete(payload, k)
		} else {
			payload[k] = v
		}
	}
	raw, _ := json.Marshal(payload)
	r := g.s.Run(g.s.Repo, string(raw), nil, "sh", "-c", command)
	if r.Code != 0 {
		g.s.t.Fatalf("the Stop hook exited %d:\n%s", r.Code, r.Output())
	}
	reply := ResolverStopReply{Raw: r.Stdout}
	out := strings.TrimSpace(r.Stdout)
	if out == "" {
		return reply
	}
	if strings.Count(out, "\n") != 0 || json.Unmarshal([]byte(out), &reply) != nil {
		g.s.t.Fatalf("the Stop hook's stdout is not one JSON reply:\n%s", r.Stdout)
	}
	reply.Raw = r.Stdout
	return reply
}

// TestStopBackstopAsksOnceForLedgersLeftInReasoning drives two review sequences to
// release in one turn on each harness, with their ledgers only in reasoning. The root
// Stop refuses once and names both headers; a final reply holding both ledgers ends the
// turn and reconciles each row; a later turn and a subagent Stop are never refused.
//
// pins existing behaviour
func TestStopBackstopAsksOnceForLedgersLeftInReasoning(t *testing.T) {
	t.Parallel()
	for _, h := range nativeHarnesses {
		t.Run(h.harness, func(t *testing.T) {
			t.Parallel()
			s, g := newNativeGate(t, h.harness, h.config, h.sub)
			g.turn, g.transcript = "turn-1", g.sessionTranscript(backstopSession)
			first, _ := g.releaseChain(backstopSession, "mod.py", "add mod")
			second, _ := g.releaseChain(backstopSession, "mod2.py", "add mod2")
			idOf := func(header string) string { return ledgerHeaderRe.FindStringSubmatch(header)[1] }
			ledgerA := chainLedger(idOf(first), "add mod", "the first concern")
			ledgerB := chainLedger(idOf(second), "add mod2", "the second concern")
			g.writeTurn(backstopSession, turnEntry{ledgerA, true}, turnEntry{ledgerB, true}, turnEntry{"Both commits are in.", false})

			// A resolver lock another run holds does not delay or stop the check.
			lock := filepath.Join(s.Home, ".steer", "state", "commit-review", "backfill.lock")
			release, held, err := gate.TryLockFile(lock)
			if err != nil || !held {
				t.Fatalf("hold the resolver lock: held=%v err=%v", held, err)
			}
			block := g.stop(backstopSession, nil)
			release()
			if block.Decision != "block" || !strings.Contains(block.Reason, first) || !strings.Contains(block.Reason, second) {
				t.Fatalf("the root Stop did not block naming both headers:\n%s", block.Raw)
			}
			if !strings.Contains(block.Reason, "Thinking text does not count") {
				t.Errorf("the reason does not say reasoning is not a reply:\n%s", block.Reason)
			}
			for name, fields := range map[string]map[string]any{
				"a continued Stop":    {"stop_hook_active": true},
				"an unflagged Stop":   {"stop_hook_active": nil},
				"a later turn's Stop": {g.turnKey(): "turn-2"},
				"a subagent Stop":     {"hook_event_name": "SubagentStop", "agent_id": "agent-1"},
			} {
				if r := g.stop(backstopSession, fields); r.Decision == "block" {
					t.Errorf("%s was refused:\n%s", name, r.Raw)
				}
			}
			if r := g.stop(backstopSession, map[string]any{g.turnKey(): nil}); r.Decision == "block" ||
				!strings.Contains(r.System, "Ledger backstop unavailable at this Stop") {
				t.Errorf("a Stop naming no turn = %+v, want the unavailable notice and no block", r)
			}

			g.writeTurn(backstopSession, turnEntry{ledgerA, true}, turnEntry{ledgerB, true}, turnEntry{"Both commits are in.", false},
				turnEntry{"The ledgers:\n\n" + ledgerA + "\n" + ledgerB, false})
			if r := g.stop(backstopSession, nil); r.Decision == "block" || r.System != "" {
				t.Fatalf("a final reply holding both ledgers still drew a reply:\n%s", r.Raw)
			}
			s.MustSteer("commit-gate", "backfill", "--apply")
			found := map[string]string{}
			for _, row := range s.LedgerRows() {
				if row["phase"] == "retry" {
					found[row["chain_id"].(string)] = fmt.Sprint(row["ledger_found"], " ", concernsOf(row))
				}
			}
			want := map[string]string{
				idOf(first):  "true [REJECTED: the first concern]",
				idOf(second): "true [REJECTED: the second concern]",
			}
			if fmt.Sprint(found) != fmt.Sprint(want) {
				t.Errorf("reconciled retry rows = %v, want %v", found, want)
			}
		})
	}
}

// TestAReleaseWithoutATurnIDTellsTheOperator checks that a release whose PreToolUse
// payload names no turn tells the operator the backstop cannot act on it, and that the
// root Stop then refuses nothing.
//
// pins existing behaviour
func TestAReleaseWithoutATurnIDTellsTheOperator(t *testing.T) {
	t.Parallel()
	for _, h := range nativeHarnesses {
		t.Run(h.harness, func(t *testing.T) {
			t.Parallel()
			_, g := newNativeGate(t, h.harness, h.config, h.sub)
			g.transcript = g.sessionTranscript(backstopSession)
			_, retry := g.releaseChain(backstopSession, "mod.py", "add mod")
			if !strings.Contains(retry.System, "Ledger backstop unavailable for this sequence: PreToolUse supplied no usable "+g.turnKey()+".") {
				t.Errorf("the release's operator line does not name the missing turn:\n%s", retry.Raw)
			}
			g.writeTurn(backstopSession, turnEntry{"Committed.", false})
			g.turn = "turn-1"
			if r := g.stop(backstopSession, nil); r.Decision == "block" {
				t.Errorf("a release with no recorded turn was enforced:\n%s", r.Raw)
			}
		})
	}
}
