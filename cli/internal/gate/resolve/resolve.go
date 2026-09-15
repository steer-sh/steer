// Package resolve reconciles the self-reported ledger for rows that owe one, joining
// this machine's shard to the harnesses' transcripts. The join engine is
// harness-neutral: a Harvest supplies the ledgers, and only a harvest knows a
// transcript format.
package resolve

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// The resolver rules only on rows whose transcript is local, and its neighbour scans
// (denyTSFor, chainEndTS) are valid only within one machine's stream. It is idempotent;
// run it whenever the numbers should be current.

const (
	// ledgerRequiredTS is when the ledger became mandatory; a deny before it required
	// no ledger. ledgerRoundTS is when the round marker entered the template;
	// ledgerChainTS is when the ledger's unit became the chain.
	ledgerRequiredTS = 1785700359
	ledgerRoundTS    = 1786301489
	ledgerChainTS    = 1787541947

	// A lock older than this is a crashed run's leftover and is taken over.
	backfillLockStale = 300 // seconds

	// backupKeep is how many shard backups a rewrite leaves beside the shard.
	backupKeep = 10

	// rescanAfter is how long an --if-due run waits after an applied scan before it
	// harvests again when no row is new and no deadline has passed.
	rescanAfter = 600 // seconds
)

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

// LedgerMsg is one real ledger a harvest found in a transcript.
type LedgerMsg struct {
	// When is the epoch second the agent wrote it.
	When float64
	// Key is the thread the ledger answers for: the root session, or a subagent
	// thread where the harness gates inside one. It joins to a row's own key.
	Key string
	// Path is the transcript file, kept for the operator, not for the join.
	Path string
	Text string
}

// Harvest returns every ledger one harness's transcripts hold. Only a harvest knows a
// transcript format; the join below reads none.
type Harvest func() []LedgerMsg

// Result is what one resolve did, for a caller that is not a process. The narration
// on `out` is the operator's account of the run; this is the same run's outcome in
// fields, so a caller with no terminal to print to does not have to read its own
// prose back.
type Result struct {
	// Changed counts the rows the resolve stamped. A dry run counts what it would
	// have stamped and writes none.
	Changed int
	// Locked reports that another resolve held the lock, so this one did nothing.
	// The turn-end hook runs the same engine, so a hand-started run meets it.
	Locked bool
	// Err is a write that failed. The resolve fails open: the shard is whatever it
	// was, and the backup beside it is intact.
	Err error
}

// RunBackfill resolves the local shard against every harvest, optionally rewriting it,
// and returns a process exit code (always 0 — the resolver fails open). now supplies
// the wall clock (nil uses time.Now).
func RunBackfill(cfg gate.Config, harvests []Harvest, opts BackfillOptions, out io.Writer, now func() float64) int {
	Backfill(cfg, harvests, opts, out, now)
	return 0
}

// Backfill is the same run, reported in fields. RunBackfill wraps it, so the two
// can never resolve differently.
func Backfill(cfg gate.Config, harvests []Harvest, opts BackfillOptions, out io.Writer, now func() float64) Result {
	r := &resolver{cfg: cfg, harvests: harvests, quiet: opts.Quiet, out: out, now: now}
	if r.now == nil {
		r.now = func() float64 { return float64(time.Now().Unix()) }
	}
	log := cfg.LogFile()
	if _, err := os.Stat(log); err != nil {
		r.say("no log at %s", log)
		return Result{}
	}
	// Taken before the log is read, so a row appended after the read is newer than the
	// scan the record names.
	started := r.now()
	lines := splitLedgerLines(log)
	seen := len(lines)
	rows := make([]resolverRow, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if row := DecodeRow(line); row != nil {
			rows = append(rows, row)
		}
	}
	if opts.Reresolve {
		r.reopen(rows)
	}
	if opts.IfDue && !r.pending(rows) {
		r.say("nothing due")
		return Result{}
	}
	if !r.acquireLock() {
		r.say("another resolver run holds the lock")
		return Result{Locked: true}
	}
	defer r.releaseLock()
	res := r.resolveAndWrite(rows, seen, opts.Apply)
	if opts.Apply && res.Err == nil {
		r.recordScan(started)
	}
	return res
}

func (r *resolver) scanPath() string { return filepath.Join(r.cfg.StateDir, "backfill.scanned") }

// lastScan is when the last applied scan started, or ok=false when none is recorded.
func (r *resolver) lastScan() (float64, bool) {
	if r.cfg.StateDir == "" {
		return 0, false
	}
	data, err := os.ReadFile(r.scanPath())
	if err != nil {
		return 0, false
	}
	ts, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	return ts, err == nil
}

// recordScan notes an applied scan's start. A failed write leaves --if-due scanning on
// every due turn, as it did before the record existed.
func (r *resolver) recordScan(ts float64) {
	if r.cfg.StateDir == "" {
		return
	}
	os.WriteFile(r.scanPath(), []byte(strconv.FormatInt(int64(ts), 10)), 0o644)
}

type resolver struct {
	cfg      gate.Config
	harvests []Harvest
	quiet    bool
	out      io.Writer
	now      func() float64
}

// allLedgers runs every harvest and orders the union, so the result does not depend on
// which harness was read first.
func (r *resolver) allLedgers() []LedgerMsg {
	var out []LedgerMsg
	for _, harvest := range r.harvests {
		out = append(out, harvest()...)
	}
	SortLedgers(out)
	return out
}

// SortLedgers orders a harvest by (when, key, path, text). A harvest calls it so its
// own output is stable on its own; RunBackfill calls it again over the union.
func SortLedgers(msgs []LedgerMsg) {
	sort.Slice(msgs, func(i, j int) bool {
		a, b := msgs[i], msgs[j]
		if a.When != b.When {
			return a.When < b.When
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Text < b.Text
	})
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

// ExpectsLedger reports whether a row demanded a ledger: every retry, and a cap whose
// terminal review reached the agent. Findings written as prose score zero slots and are
// counted by reviews_off_format, which a cap with no review at all never carries.
func ExpectsLedger(row map[string]any) bool {
	switch pyStr(row["phase"]) {
	case "retry":
		return true
	case "cap":
		n, _ := pyInt(row["finding_slots"])
		off, _ := pyInt(row["reviews_off_format"])
		return n > 0 || off > 0
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
		if session != "" && joinKey(row) != session {
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

// joinKey is the thread a row's ledger was written in. A harness that gates inside a
// subagent stamps thread_id, and two threads of one session share a session_id — so
// keying on the session alone would let one thread claim the other's ledger.
func joinKey(row resolverRow) string {
	if thread := pyStr(row["thread_id"]); thread != "" {
		return thread
	}
	return pyStr(row["session_id"])
}

// chainIDs numbers the chain each row belongs to, or -1 for a non-review row. A chain
// is a run within one thread and repository, opened by a deny or clean at firing 1;
// the gate keys its pending state on the actor and the repository, so one thread can
// interleave the rounds of two repositories' chains.
func chainIDs(rows []resolverRow) []int {
	ids := make([]int, len(rows))
	current := map[string]int{}
	for i, row := range rows {
		ids[i] = -1
		phase := pyStr(row["phase"])
		if gate.NonReviewPhases[phase] {
			continue
		}
		stream := joinKey(row) + "\x00" + pyStr(row["repo"])
		f, _ := pyInt(row["firing"])
		id, ok := current[stream]
		if !ok || ((phase == "deny" || phase == "clean") && f <= 1) {
			id = i
			current[stream] = id
		}
		ids[i] = id
	}
	return ids
}

// chainSubjects is every subject the rows of chain id carry. Each row stamps the
// message of its own attempt, and the header the agent copies comes from a deny, so a
// chain whose message changed between rounds answers to each of them.
func chainSubjects(rows []resolverRow, ids []int, id int) map[string]bool {
	out := map[string]bool{}
	if id < 0 {
		return out
	}
	for j, row := range rows {
		if ids[j] == id {
			if s := pyStr(row["subject"]); s != "" {
				out[s] = true
			}
		}
	}
	return out
}

// nameOwner returns the one chain of thread key that carries subject name, was handed
// findings, and could still claim a ledger written at when. ok is false when none or
// several do: a name that two chains share identifies neither. A chain of clean rows
// was handed no findings, so it owes no ledger and shares no name.
func (r *resolver) nameOwner(rows []resolverRow, ids []int, key, name string, when float64) (int, bool) {
	type span struct{ start, end float64 }
	spans := map[int]*span{}
	named := map[int]bool{}
	handed := map[int]bool{}
	for j, row := range rows {
		id := ids[j]
		if id < 0 || joinKey(row) != key {
			continue
		}
		if phase := pyStr(row["phase"]); phase == "deny" || phase == "cap" {
			handed[id] = true
		}
		ts, ok := pyNum(row["ts"])
		if !ok {
			continue
		}
		if s := spans[id]; s == nil {
			spans[id] = &span{ts, ts}
		} else {
			s.start, s.end = math.Min(s.start, ts), math.Max(s.end, ts)
		}
		if pyStr(row["subject"]) == name {
			named[id] = true
		}
	}
	owner, n := -1, 0
	for id := range named {
		if !handed[id] {
			continue
		}
		if s := spans[id]; s.start <= when && when <= s.end+r.spanFor("chain") {
			owner, n = id, n+1
		}
	}
	return owner, n == 1
}

// joinFor is the join a row takes by its era, and so the span its miss waits out.
func joinFor(row resolverRow, denyTS float64) string {
	firing, ok := pyInt(row["firing"])
	switch {
	case ok && firing != 0 && denyTS >= ledgerChainTS:
		return "chain"
	case ok && firing != 0 && denyTS >= ledgerRoundTS:
		return "round"
	}
	return "window"
}

// pickLedger returns the ledger answering row i and how it was joined: "chain",
// "round" or "window". A miss returns (nil, join).
func (r *resolver) pickLedger(rows []resolverRow, i int, denyTS float64, ledgers []LedgerMsg) (*LedgerMsg, string) {
	row := rows[i]
	session := joinKey(row)
	firing, _ := pyInt(row["firing"])
	join := joinFor(row, denyTS)

	inWindow := func(span float64) []LedgerMsg {
		var out []LedgerMsg
		for _, x := range ledgers {
			if denyTS <= x.When && x.When <= denyTS+span && (session == "" || x.Key == session) {
				out = append(out, x)
			}
		}
		return out
	}

	if join == "chain" {
		bound := r.chainEndTS(rows, i, session, firing)
		ids := chainIDs(rows)
		subjects := chainSubjects(rows, ids, ids[i])
		chainID := strings.ToLower(pyStr(row["chain_id"]))
		var named, bounded []LedgerMsg
		for _, x := range inWindow(r.spanFor("chain")) {
			for _, block := range gate.ChainBlocks(x.Text) {
				if !gate.LedgerRounds(block)[firing] {
					continue
				}
				cand := LedgerMsg{When: x.When, Key: x.Key, Path: x.Path, Text: block}
				// A chain ID names one chain outright. A header without one falls to the
				// subject below; a header with one never answers a chain that has none.
				if id := gate.BlockChainID(block); id != "" {
					if id == chainID {
						named = append(named, cand)
					}
					continue
				}
				got := gate.BlockSubject(block)
				switch {
				case got == "" || len(subjects) == 0:
				case !subjects[got]:
					continue
				case session != "":
					// A name lets a block past the bound, so it must name exactly one
					// chain. A row with no thread gets no name: its window spans every
					// session, and the git channel stamps one subject on every commit.
					if owner, ok := r.nameOwner(rows, ids, session, got, x.When); ok && owner == ids[i] {
						named = append(named, cand)
					}
					continue
				}
				if x.When < bound {
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
		var mine []LedgerMsg
		for _, x := range inWindow(r.spanFor("round")) {
			if n, ok := gate.LedgerRound(x.Text); x.When < bound && ok && n == firing {
				mine = append(mine, x)
			}
		}
		if len(mine) > 0 {
			return &mine[len(mine)-1], "round"
		}
		return nil, "chain"
	}

	if join == "round" {
		bound := r.chainEndTS(rows, i, session, firing)
		var mine []LedgerMsg
		for _, x := range inWindow(r.spanFor("round")) {
			if n, ok := gate.LedgerRound(x.Text); x.When < bound && ok && n == firing {
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

// pending reports whether an --if-due run should harvest: some row is unresolved, its
// ledger has had time to land, and the last applied scan cannot have seen what this
// one would. It reads only the log and the scan record, so the transcript scan is
// skipped on the ordinary turn.
func (r *resolver) pending(rows []resolverRow) bool {
	now := r.now()
	last, scanned := r.lastScan()
	for i, row := range rows {
		if !ExpectsLedger(row) || pyTruthy(row["ledger_found"]) || pyTruthy(row["backfilled"]) {
			continue
		}
		denyTS, ok := anchorTS(rows, i)
		if !ok || now < denyTS+gate.LedgerGrace {
			continue
		}
		if !scanned || now-last >= rescanAfter {
			return true
		}
		if ts, ok := pyNum(row["ts"]); ok && ts >= last {
			return true
		}
		// A scan inside the grace may predate the ledger, which usually lands then.
		if last < denyTS+gate.LedgerGrace {
			return true
		}
		if deadline := denyTS + r.spanFor(joinFor(row, denyTS)); last < deadline && deadline <= now {
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

func (r *resolver) resolveAndWrite(rows []resolverRow, seenLines int, apply bool) Result {
	ledgers := r.allLedgers()
	r.say("%d rows, %d ledgers found across transcripts\n", len(rows), len(ledgers))

	changed := 0
	for i, row := range rows {
		if !ExpectsLedger(row) || pyTruthy(row["ledger_found"]) || pyTruthy(row["backfilled"]) {
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
		text := cand.Text
		firing, _ := pyInt(row["firing"])
		if join == "chain" {
			text = gate.LedgerSection(text, firing)
		}
		for k, v := range gate.ParseLedgerText(text) {
			row[k] = v
		}
		if join == "chain" {
			row["ledger_round"] = firing
		}
		row["ledger_found"] = true
		row["ledger_ts"] = int64(cand.When)
		row["ledger_join"] = join
		row["backfilled"] = true
		if cand.Key != "" && pyStr(row["session_id"]) == "" && pyStr(row["thread_id"]) == "" {
			row["session_id"] = cand.Key
		}
		changed++
		r.say("  ts=%v deny=%d  ledger at %d join=%s: %v/%v",
			row["ts"], int64(denyTS), int64(cand.When), join,
			row["self_reported_accepted"], row["self_reported_rejected"])
	}

	for _, row := range rows {
		delete(row, "_recheck")
	}
	r.say("\n%d rows to update", changed)
	if changed == 0 {
		return Result{}
	}
	if !apply {
		r.say("dry run -- pass --apply to write")
		return Result{Changed: changed}
	}
	if err := r.writeRows(rows, seenLines); err != nil {
		r.say("write failed: %v", err)
		return Result{Changed: changed, Err: err}
	}
	return Result{Changed: changed}
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
	pruneBackups(log, backupKeep)
	appended := ""
	if len(tail) > 0 {
		appended = fmt.Sprintf("; %d row(s) appended during the run", len(tail))
	}
	r.say("written; backup at %s%s", filepath.Base(backup), appended)
	return nil
}

// pruneBackups removes all but the newest keep backups of the shard, ordered by the
// epoch in their names. A name with no epoch is left alone.
func pruneBackups(log string, keep int) {
	matches, _ := filepath.Glob(log + ".bak.*")
	type backup struct {
		path string
		ts   int64
	}
	var backups []backup
	for _, path := range matches {
		ts, err := strconv.ParseInt(strings.TrimPrefix(path, log+".bak."), 10, 64)
		if err != nil {
			continue
		}
		backups = append(backups, backup{path, ts})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].ts > backups[j].ts })
	for i := keep; i < len(backups); i++ {
		os.Remove(backups[i].path)
	}
}

// CountUnresolved counts local-shard rows that owe a ledger the resolver has not yet
// stamped: a retry or cap the resolver would still act on. Read-only — status reads it
// to report resolver debt without invoking the resolver.
func CountUnresolved(cfg gate.Config) int {
	n := 0
	for _, line := range splitLedgerLines(cfg.LogFile()) {
		row := DecodeRow(line)
		if row == nil {
			continue
		}
		if ExpectsLedger(row) && !pyTruthy(row["ledger_found"]) && !pyTruthy(row["backfilled"]) {
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

// DecodeRow reads one ledger line the way the resolver does: numbers stay
// json.Number, so a rewrite reproduces the original text rather than a float's
// rendering of it.
func DecodeRow(line string) map[string]any {
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
