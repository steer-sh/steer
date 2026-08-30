package cc

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// The resolver reconciles the self-reported ledger for retry rows in THIS machine's
// shard. Its evidence is ~/.claude/projects, so it rules only on rows whose transcript
// is local, and its neighbour scans (denyTSFor, chainEndTS) are valid only within one
// machine's stream. A port of ~/.claude/hooks/commit-review/backfill_ledger.py; run it
// whenever the numbers should be current, it is idempotent.

const (
	// ledgerRequiredTS is when the ledger became mandatory; a deny before it required
	// no ledger. ledgerRoundTS is when the round marker entered the template;
	// ledgerChainTS is when the ledger's unit became the chain.
	ledgerRequiredTS = 1785700359
	ledgerRoundTS    = 1786301489
	ledgerChainTS    = 1787541947

	// A lock older than this is a crashed run's leftover and is taken over.
	backfillLockStale = 300 // seconds
)

// ledgerChainRe matches a chain ledger's header line. One message can carry more than
// one, so a candidate is a BLOCK of a message, never the whole message.
var ledgerChainRe = regexp.MustCompile(
	`(?im)^[ \t]*(?:[-+>]\s*)?\**\s*` + regexp.QuoteMeta(LedgerHeader) + `[^\n]*chain ledger`)

// ledgerSectionCapRe is ledgerSectionRe with the round number captured.
var ledgerSectionCapRe = regexp.MustCompile(`(?im)^[ \t]*(?:[-+>]\s*)?\**\s*Round\s+(\d+)\s*[/,]`)

// blockSubjectRe reads the commit subject a chain ledger's header line names.
var blockSubjectRe = regexp.MustCompile(`(?i)chain ledger,\s*(.+?)\s*\**\s*$`)

// BackfillOptions are the resolver's four flags, matching the Python CLI.
type BackfillOptions struct {
	Apply     bool
	IfDue     bool
	Quiet     bool
	Reresolve bool
}

// resolverRow is one adjudication row, kept as a map because the schema is append-only
// across two writers and read by key.
type resolverRow = map[string]any

// ledgerMsg is one real ledger found in a transcript.
type ledgerMsg struct {
	when    float64
	session string
	path    string
	text    string
}

// RunBackfill resolves the local shard, optionally rewriting it, and returns a process
// exit code (always 0 — the resolver fails open). projectsDir is ~/.claude/projects;
// now supplies the wall clock (nil uses time.Now).
func RunBackfill(cfg gate.Config, projectsDir string, opts BackfillOptions, out io.Writer, now func() float64) int {
	r := &resolver{cfg: cfg, projectsDir: projectsDir, quiet: opts.Quiet, out: out, now: now}
	if r.now == nil {
		r.now = func() float64 { return float64(time.Now().Unix()) }
	}
	log := cfg.LogFile()
	if _, err := os.Stat(log); err != nil {
		r.say("no log at %s", log)
		return 0
	}
	lines := splitLedgerLines(log)
	seen := len(lines)
	rows := make([]resolverRow, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if row := decodeResolverRow(line); row != nil {
			rows = append(rows, row)
		}
	}
	if opts.Reresolve {
		r.reopen(rows)
	}
	if opts.IfDue && !r.pending(rows) {
		r.say("nothing due")
		return 0
	}
	if !r.acquireLock() {
		r.say("another resolver run holds the lock")
		return 0
	}
	defer r.releaseLock()
	return r.resolveAndWrite(rows, seen, opts.Apply)
}

type resolver struct {
	cfg         gate.Config
	projectsDir string
	quiet       bool
	out         io.Writer
	now         func() float64
	// ledgersOverride replaces the transcript harvest in tests. Production leaves it
	// nil and reads ~/.claude/projects.
	ledgersOverride func() []ledgerMsg
}

func (r *resolver) allLedgers() []ledgerMsg {
	if r.ledgersOverride != nil {
		return r.ledgersOverride()
	}
	return r.ledgerMessages()
}

func (r *resolver) say(format string, args ...any) {
	if r.quiet || r.out == nil {
		return
	}
	fmt.Fprintf(r.out, format+"\n", args...)
}

func (r *resolver) staleAfter() float64 { return r.cfg.StaleAfter.Seconds() }

// spanFor bounds how long after a deny a ledger of each join type may still be claimed.
func (r *resolver) spanFor(join string) float64 {
	switch join {
	case "chain":
		return 8 * r.staleAfter()
	case "round":
		return 4 * r.staleAfter()
	default:
		return r.staleAfter()
	}
}

func (r *resolver) lockPath() string { return filepath.Join(r.cfg.StateDir, "backfill.lock") }

// acquireLock reports whether this run may rewrite the log. A stale lock is taken over;
// an unlockable state behaves as before the lock existed.
func (r *resolver) acquireLock() bool {
	path := r.lockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return true
	}
	if info, err := os.Stat(path); err == nil {
		if r.now()-float64(info.ModTime().Unix()) > backfillLockStale {
			os.Remove(path)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false
		}
		return true
	}
	fmt.Fprintf(f, "%d", os.Getpid())
	f.Close()
	return true
}

func (r *resolver) releaseLock() { os.Remove(r.lockPath()) }

// ledgerMessages returns every real ledger in every transcript, sorted by (when,
// session, path, text).
func (r *resolver) ledgerMessages() []ledgerMsg {
	var out []ledgerMsg
	matches, _ := filepath.Glob(filepath.Join(r.projectsDir, "*", "*.jsonl"))
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, LedgerHeader) {
				continue
			}
			var row struct {
				Type        string            `json:"type"`
				IsSidechain bool              `json:"isSidechain"`
				SessionID   string            `json:"sessionId"`
				Timestamp   string            `json:"timestamp"`
				Message     transcriptContent `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				continue
			}
			if row.Type != "assistant" || row.IsSidechain {
				continue
			}
			when := isoEpoch(row.Timestamp)
			if when == nil {
				continue
			}
			for _, text := range textBlocks(row.Message.Content) {
				if strings.Contains(text, LedgerHeader) && HasLedgerLine(text) {
					out = append(out, ledgerMsg{*when, row.SessionID, p, text})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.when != b.when {
			return a.when < b.when
		}
		if a.session != b.session {
			return a.session < b.session
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.text < b.text
	})
	return out
}

// transcriptContent is the message subset ledgerMessages reads.
type transcriptContent struct {
	Content json.RawMessage `json:"content"`
}

// ledgerRounds returns the rounds a chain ledger reports on. Empty for a legacy
// per-round ledger, whose one round lives in the header LedgerRound reads.
func ledgerRounds(text string) map[int]bool {
	out := map[int]bool{}
	for _, m := range ledgerSectionCapRe.FindAllStringSubmatch(text, -1) {
		if n, ok := atoiOK(m[1]); ok {
			out[n] = true
		}
	}
	return out
}

// chainBlocks returns each chain ledger inside one message, header line first.
func chainBlocks(text string) []string {
	locs := ledgerChainRe.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	starts := make([]int, len(locs))
	for i, loc := range locs {
		starts[i] = loc[0]
	}
	out := make([]string, len(starts))
	for i, s := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		out[i] = text[s:end]
	}
	return out
}

// blockSubject returns the commit subject a chain ledger's HEADER names, or "". Only the
// first line is read: a subject quoted inside a bullet names no commit.
func blockSubject(block string) string {
	first := block
	if idx := strings.IndexByte(block, '\n'); idx >= 0 {
		first = block[:idx]
	}
	if m := blockSubjectRe.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

// ledgerSection returns one round's slice of a chain ledger: its section heading up to
// the next one, or "" when the ledger carries no section for that round.
func ledgerSection(text string, firing int) string {
	locs := ledgerSectionCapRe.FindAllStringSubmatchIndex(text, -1)
	type bound struct {
		start int
		n     int
	}
	var bounds []bound
	for _, loc := range locs {
		n, ok := atoiOK(text[loc[2]:loc[3]])
		if !ok {
			continue
		}
		bounds = append(bounds, bound{loc[0], n})
	}
	for k, b := range bounds {
		if b.n == firing {
			end := len(text)
			if k+1 < len(bounds) {
				end = bounds[k+1].start
			}
			return text[b.start:end]
		}
	}
	return ""
}

// expectsLedger reports whether a row demanded a ledger: every retry, and a cap whose
// terminal review had findings to adjudicate.
func expectsLedger(row resolverRow) bool {
	switch pyStr(row["phase"]) {
	case "retry":
		return true
	case "cap":
		n, _ := pyInt(row["finding_slots"])
		return n > 0
	}
	return false
}

// anchorTS is when the findings this row's ledger answers were handed to the agent. A
// cap row IS that moment; a retry row answers the deny before it.
func anchorTS(rows []resolverRow, i int) (float64, bool) {
	if pyStr(rows[i]["phase"]) == "cap" {
		return pyNum(rows[i]["ts"])
	}
	return denyTSFor(rows, i)
}

// denyTSFor is the deny this retry answers: deny_ts if the row carries it, else the
// nearest preceding deny row.
func denyTSFor(rows []resolverRow, i int) (float64, bool) {
	if v, ok := pyNum(rows[i]["deny_ts"]); ok && v != 0 {
		return v, true
	}
	for j := i - 1; j >= 0; j-- {
		if pyStr(rows[j]["phase"]) == "deny" {
			return pyNum(rows[j]["ts"])
		}
	}
	return 0, false
}

// chainEndTS is when this row's chain stops being the live one. A non-review or clean
// phase is not a chain start and bounds nothing.
func (r *resolver) chainEndTS(rows []resolverRow, i int, session string, firing int) float64 {
	for j := i + 1; j < len(rows); j++ {
		row := rows[j]
		phase := pyStr(row["phase"])
		if gate.NonReviewPhases[phase] || phase == "clean" {
			continue
		}
		if session != "" && pyStr(row["session_id"]) != session {
			continue
		}
		f, _ := pyInt(row["firing"])
		if f == 1 || f == firing {
			if ts, ok := pyNum(row["ts"]); ok {
				return ts
			}
			return math.Inf(1)
		}
	}
	return math.Inf(1)
}

// pickLedger returns the ledger answering row i and how it was joined: "chain",
// "round" or "window". A miss returns (nil, join).
func (r *resolver) pickLedger(rows []resolverRow, i int, denyTS float64, ledgers []ledgerMsg) (*ledgerMsg, string) {
	row := rows[i]
	session := pyStr(row["session_id"])
	firing, hasFiring := pyInt(row["firing"])
	firingTruthy := hasFiring && firing != 0

	inWindow := func(span float64) []ledgerMsg {
		var out []ledgerMsg
		for _, x := range ledgers {
			if denyTS <= x.when && x.when <= denyTS+span && (session == "" || x.session == session) {
				out = append(out, x)
			}
		}
		return out
	}

	if firingTruthy && denyTS >= ledgerChainTS {
		bound := r.chainEndTS(rows, i, session, firing)
		subject := pyStr(row["subject"])
		var named, bounded []ledgerMsg
		for _, x := range inWindow(r.spanFor("chain")) {
			for _, block := range chainBlocks(x.text) {
				if !ledgerRounds(block)[firing] {
					continue
				}
				got := blockSubject(block)
				if subject != "" && got != "" && got != subject {
					continue
				}
				cand := ledgerMsg{x.when, x.session, x.path, block}
				if subject != "" && got == subject {
					named = append(named, cand)
				} else if x.when < bound {
					bounded = append(bounded, cand)
				}
			}
		}
		if len(named) > 0 {
			return &named[len(named)-1], "chain"
		}
		if len(bounded) > 0 {
			return &bounded[len(bounded)-1], "chain"
		}
		var mine []ledgerMsg
		for _, x := range inWindow(r.spanFor("round")) {
			if n, ok := LedgerRound(x.text); x.when < bound && ok && n == firing {
				mine = append(mine, x)
			}
		}
		if len(mine) > 0 {
			return &mine[len(mine)-1], "round"
		}
		return nil, "chain"
	}

	if firingTruthy && denyTS >= ledgerRoundTS {
		bound := r.chainEndTS(rows, i, session, firing)
		var mine []ledgerMsg
		for _, x := range inWindow(r.spanFor("round")) {
			if n, ok := LedgerRound(x.text); x.when < bound && ok && n == firing {
				mine = append(mine, x)
			}
		}
		if len(mine) > 0 {
			return &mine[len(mine)-1], "round"
		}
		return nil, "round"
	}

	cand := inWindow(r.spanFor("window"))
	if len(cand) > 0 {
		return &cand[len(cand)-1], "window"
	}
	return nil, "window"
}

// pending reports whether some retry row is unresolved and its ledger has had time to
// land. It reads only the log, so the transcript scan is skipped on the ordinary turn.
func (r *resolver) pending(rows []resolverRow) bool {
	now := r.now()
	for i, row := range rows {
		if !expectsLedger(row) || pyTruthy(row["ledger_found"]) || pyTruthy(row["backfilled"]) {
			continue
		}
		if denyTS, ok := anchorTS(rows, i); ok && now >= denyTS+gate.LedgerGrace {
			return true
		}
	}
	return false
}

// reopen clears the resolved-miss stamp so a stamped miss is examined again. The marker
// is stripped before the log is written.
func (r *resolver) reopen(rows []resolverRow) {
	n := 0
	for _, row := range rows {
		found, hasFound := row["ledger_found"]
		b, isBool := found.(bool)
		isFalse := hasFound && isBool && !b
		if isFalse && pyTruthy(row["backfilled"]) {
			row["_recheck"] = true
			delete(row, "backfilled")
			n++
		}
	}
	r.say("reopened %d stamped miss(es)", n)
}

func (r *resolver) resolveAndWrite(rows []resolverRow, seenLines int, apply bool) int {
	ledgers := r.allLedgers()
	r.say("%d rows, %d ledgers found across transcripts\n", len(rows), len(ledgers))

	changed := 0
	for i, row := range rows {
		if !expectsLedger(row) || pyTruthy(row["ledger_found"]) || pyTruthy(row["backfilled"]) {
			continue
		}
		denyTS, ok := anchorTS(rows, i)
		if !ok {
			continue
		}

		if denyTS < ledgerRequiredTS {
			row["ledger_expected"] = false
			row["backfilled"] = true
			changed++
			r.say("  ts=%v deny=%d  pre-cutoff, no ledger required", row["ts"], int64(denyTS))
			continue
		}

		row["ledger_expected"] = true
		cand, join := r.pickLedger(rows, i, denyTS, ledgers)
		if cand == nil {
			if r.now() < denyTS+r.spanFor(join) {
				if popRecheck(row) {
					row["backfilled"] = true
				}
				r.say("  ts=%v deny=%d  window still open, skipping", row["ts"], int64(denyTS))
				continue
			}
			if popRecheck(row) {
				row["ledger_join"] = join
				row["backfilled"] = true
				continue
			}
			row["ledger_found"] = false
			row["ledger_join"] = join
			row["backfilled"] = true
			changed++
			r.say("  ts=%v deny=%d  EXPECTED a ledger (round %v), none found", row["ts"], int64(denyTS), row["firing"])
			continue
		}

		delete(row, "_recheck")
		text := cand.text
		firing, _ := pyInt(row["firing"])
		if join == "chain" {
			text = ledgerSection(text, firing)
		}
		for k, v := range ParseLedgerText(text) {
			row[k] = v
		}
		if join == "chain" {
			row["ledger_round"] = firing
		}
		row["ledger_found"] = true
		row["ledger_ts"] = int64(cand.when)
		row["ledger_join"] = join
		row["backfilled"] = true
		if cand.session != "" && pyStr(row["session_id"]) == "" {
			row["session_id"] = cand.session
		}
		changed++
		r.say("  ts=%v deny=%d  ledger at %d join=%s: %v/%v",
			row["ts"], int64(denyTS), int64(cand.when), join,
			row["self_reported_accepted"], row["self_reported_rejected"])
	}

	for _, row := range rows {
		delete(row, "_recheck")
	}
	r.say("\n%d rows to update", changed)
	if changed == 0 {
		return 0
	}
	if !apply {
		r.say("dry run -- pass --apply to write")
		return 0
	}
	if err := r.writeRows(rows, seenLines); err != nil {
		r.say("write failed: %v", err)
	}
	return 0
}

// writeRows rewrites the shard, backing it up first and carrying through any rows the
// hook appended under the snapshot.
func (r *resolver) writeRows(rows []resolverRow, seenLines int) error {
	log := r.cfg.LogFile()
	backup := log + fmt.Sprintf(".bak.%d", int64(r.now()))
	if data, err := os.ReadFile(log); err == nil {
		if err := os.WriteFile(backup, data, 0o644); err != nil {
			return err
		}
	}
	tail := splitLedgerLines(log)
	if seenLines < len(tail) {
		tail = tail[seenLines:]
	} else {
		tail = nil
	}
	var sb strings.Builder
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			return err
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	for _, x := range tail {
		if strings.TrimSpace(x) != "" {
			sb.WriteString(x)
			sb.WriteByte('\n')
		}
	}
	tmp := log + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, log); err != nil {
		return err
	}
	appended := ""
	if len(tail) > 0 {
		appended = fmt.Sprintf("; %d row(s) appended during the run", len(tail))
	}
	r.say("written; backup at %s%s", filepath.Base(backup), appended)
	return nil
}

// CountUnresolved counts local-shard rows that owe a ledger the resolver has not yet
// stamped: a retry or cap the resolver would still act on. Read-only — status reads it
// to report resolver debt without invoking the resolver.
func CountUnresolved(cfg gate.Config) int {
	n := 0
	for _, line := range splitLedgerLines(cfg.LogFile()) {
		row := decodeResolverRow(line)
		if row == nil {
			continue
		}
		if expectsLedger(row) && !pyTruthy(row["ledger_found"]) && !pyTruthy(row["backfilled"]) {
			n++
		}
	}
	return n
}

// --- helpers -----------------------------------------------------------------

func splitLedgerLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(data)
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func decodeResolverRow(line string) resolverRow {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil
	}
	return m
}

func popRecheck(row resolverRow) bool {
	had := pyTruthy(row["_recheck"])
	delete(row, "_recheck")
	return had
}

func atoiOK(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// pyNum mirrors Python's numeric read: json.Number, float or int yields a value; every
// other type (including a missing key's nil) yields ok=false.
func pyNum(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func pyInt(v any) (int, bool) {
	f, ok := pyNum(v)
	return int(f), ok
}

func pyStr(v any) string {
	s, _ := v.(string)
	return s
}

// pyTruthy mirrors Python truthiness for the value types the ledger carries.
func pyTruthy(v any) bool {
	switch n := v.(type) {
	case nil:
		return false
	case bool:
		return n
	case string:
		return n != ""
	case json.Number:
		f, _ := n.Float64()
		return f != 0
	case float64:
		return n != 0
	case int:
		return n != 0
	case int64:
		return n != 0
	case []any:
		return len(n) > 0
	case map[string]any:
		return len(n) > 0
	}
	return v != nil
}
