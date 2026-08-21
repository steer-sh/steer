package cc

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// harnessTagRe matches only the OPENING tag. The harness writes its own text into
// user turns — hook output, system reminders, the stdout of a slash command — and none
// of it is the user asking for anything. Stripping it needs the closing tag to be the
// SAME tag, which is a backreference RE2 cannot express, so stripHarnessText below
// pairs the tags by hand rather than accepting any closer.
var harnessTagRe = regexp.MustCompile(`<(system-reminder|local-command-[a-z]+|command-[a-z]+)>`)

// stripHarnessText removes each `<tag>...</tag>` block, shortest body first, exactly
// as the non-greedy Python pattern did.
func stripHarnessText(text string) string {
	var out strings.Builder
	for pos := 0; pos < len(text); {
		m := harnessTagRe.FindStringSubmatchIndex(text[pos:])
		if m == nil {
			out.WriteString(text[pos:])
			break
		}
		open, bodyStart := pos+m[0], pos+m[1]
		tag := text[pos+m[2] : pos+m[3]]
		closer := strings.Index(text[bodyStart:], "</"+tag+">")
		if closer < 0 {
			// No matching closer: this opening tag is ordinary text, so keep it and
			// resume the scan after it rather than swallowing the rest of the turn.
			out.WriteString(text[pos:bodyStart])
			pos = bodyStart
			continue
		}
		out.WriteString(text[pos:open])
		pos = bodyStart + closer + len("</"+tag+">")
	}
	return out.String()
}

// transcriptRow is the subset of a session JSONL row the gate reads.
type transcriptRow struct {
	Type        string          `json:"type"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

type transcriptMessage struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		InputTokens              *int `json:"input_tokens"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// ReadTranscript returns the session file's lines. An unreadable transcript yields
// nothing rather than an error: the gate's contract is to fail open on a commit.
func ReadTranscript(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func parseRows(lines []string) []transcriptRow {
	rows := make([]transcriptRow, 0, len(lines))
	for _, line := range lines {
		var row transcriptRow
		// A line that parses to a primitive is valid JSON and has no fields; the
		// decoder rejects it here, which is the guard every reader below assumes.
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

// textBlocks extracts the text of a message's content, which is either a bare string
// or a list of typed blocks. `content: null` is a real row shape — a cancelled turn
// writes one — and it must yield nothing rather than fail the whole row.
func textBlocks(content json.RawMessage) []string {
	if len(content) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return []string{text}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			out = append(out, block.Text)
		}
	}
	return out
}

// HumanTurns returns the prompts the user typed, in order, with the harness's own text
// removed. Each turn keeps its timestamp.
func HumanTurns(rows []transcriptRow) []gate.Turn {
	var turns []gate.Turn
	for _, row := range rows {
		if row.Type != "user" || row.IsSidechain || row.IsMeta {
			continue
		}
		var message transcriptMessage
		if err := json.Unmarshal(row.Message, &message); err != nil {
			continue
		}
		joined := strings.Join(textBlocks(message.Content), "\n")
		text := strings.TrimSpace(stripHarnessText(joined))
		if text == "" {
			continue
		}
		turns = append(turns, gate.Turn{TS: isoEpoch(row.Timestamp), Text: text})
	}
	return turns
}

func isoEpoch(ts string) *float64 {
	if ts == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil
	}
	seconds := float64(parsed.UnixNano()) / 1e9
	return &seconds
}

// AuthorFields is the author side of a round: which model wrote the diff, under how much
// context, from what task. None of the three survives the session, so all are stamped at
// write time rather than reconstructed later.
func AuthorFields(ledger *gate.Ledger, cfg gate.Config, transcript, session string) gate.Row {
	if transcript == "" {
		return gate.Row{}
	}
	fields := gate.Row{"transcript": transcript}
	lines := ReadTranscript(transcript)
	for key, value := range ledger.SavePrefix(transcript, lines) {
		fields[key] = value
	}
	rows := parseRows(lines)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Type != "assistant" || row.IsSidechain {
			continue
		}
		var message transcriptMessage
		if err := json.Unmarshal(row.Message, &message); err != nil || message.Model == "" {
			continue
		}
		fields["author_model"] = message.Model
		if message.Usage != nil {
			// A key PRESENT and null is a real shape, so each term defaults to zero
			// rather than failing the sum and costing the whole row.
			fields["context_tokens"] = deref(message.Usage.InputTokens) +
				deref(message.Usage.CacheReadInputTokens) +
				deref(message.Usage.CacheCreationInputTokens)
		}
		break
	}

	turns := HumanTurns(rows)
	if len(turns) > 0 {
		last := turns[len(turns)-1].Text
		if len(last) > cfg.MaxTaskChars {
			last = last[:cfg.MaxTaskChars]
		}
		fields["task"] = last
		fields["task_turns"] = len(turns)
		for key, value := range ledger.SaveTask(session, turns) {
			fields[key] = value
		}
	}
	return fields
}

func deref(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// RepoFields is where the diff was written: the repo, the revision under it, and the
// harness. A replay reconstructs the starting tree as base_sha plus the saved diff, and
// dirty_files is what says whether that reconstruction is exact.
func RepoFields(cwd string) gate.Row {
	if cwd == "" {
		return gate.Row{}
	}
	fields := gate.Row{}
	if top, ok := gate.Git(cwd, 5*time.Second, "rev-parse", "--show-toplevel"); ok {
		fields["repo"] = strings.TrimSpace(top)
	}
	if head, ok := gate.Git(cwd, 5*time.Second, "rev-parse", "HEAD"); ok {
		sha := strings.TrimSpace(head)
		fields["base_sha"] = sha
		// A later rebase, amend or dropped branch makes this commit unreachable and
		// git reaps the tree the row's diff is a delta against. The ref costs one
		// object reference and is invisible to ordinary git use.
		gate.Git(cwd, 5*time.Second, "update-ref", "refs/commit-review/"+sha, sha)
	}
	if status, ok := gate.Git(cwd, 5*time.Second, "status", "--porcelain"); ok {
		count := 0
		for _, line := range strings.Split(status, "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		fields["dirty_files"] = count
	}
	if home, err := os.UserHomeDir(); err == nil {
		if harness, ok := gate.Git(home+"/.claude", 5*time.Second, "rev-parse", "HEAD"); ok {
			fields["harness_sha"] = strings.TrimSpace(harness)
		}
	}
	return fields
}

// --- the agent's self-reported ledger ----------------------------------------

// LedgerHeader is the phrase the deny and cap templates instruct the agent to write.
// It is required in the SAME message as the verdict bullets: the bullet shape alone
// matches ordinary prose about the gate — a session discussing this very gate contains
// `**ACCEPTED**` in running text — and a forged row corrupts the exact dataset this
// exists to build. A missed ledger is recoverable from the transcript later; a
// fabricated one is not distinguishable from a real one afterwards.
const LedgerHeader = "Commit gate fired"

// ledgerRoundRe reads the round marker off the HEADER LINE, which is what files a
// ledger against the round it answers. Optional in the pattern because ledgers written
// before it was required carry no round; a row with a null round is one of those, not
// a mismatch.
var ledgerRoundRe = regexp.MustCompile(
	`(?im)^[ \t]*(?:[-+>]\s*)?\**\s*` + regexp.QuoteMeta(LedgerHeader) + `[^\n]*?\(\s*round\s*(\d+)`)

const (
	// 24 clears the densest ledger on record (16 concerns) with headroom. The cap
	// bounds a row's size; it is not a claim about how many concerns a round may
	// raise, so a row that hits it still reports the true total in concerns_total.
	maxConcerns     = 24
	maxConcernChars = 300
)

// Concern is one adjudicated finding as the agent reported it.
type Concern struct {
	Verdict string `json:"verdict"`
	Concern string `json:"concern"`
}

// LedgerRound is the round a ledger message names, or ok=false when it names none.
func LedgerRound(text string) (int, bool) {
	m := ledgerRoundRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseLedgerLine is `^[ \t]*[-*][ \t]*\*\*(ACCEPTED|REJECTED)\*\*[ \t]*(?:[—–:]|-(?!>))?[ \t]*(.+?)[ \t]*$`
// hand-rolled. The separator alternative needs `-(?!>)`, which RE2 cannot express: a
// hyphen followed by `>` is the head of an ASCII `->` arrow, and consuming it leaves
// the concern parsed as `> ...` with the arrow split never firing.
func parseLedgerLine(line string) (Concern, bool) {
	rest := strings.TrimLeft(line, " \t")
	if rest == "" || (rest[0] != '-' && rest[0] != '*') {
		return Concern{}, false
	}
	rest = strings.TrimLeft(rest[1:], " \t")
	if !strings.HasPrefix(rest, "**") {
		return Concern{}, false
	}
	rest = rest[2:]
	var verdict string
	switch {
	case strings.HasPrefix(rest, "ACCEPTED"):
		verdict = "ACCEPTED"
	case strings.HasPrefix(rest, "REJECTED"):
		verdict = "REJECTED"
	default:
		return Concern{}, false
	}
	rest = rest[len(verdict):]
	if !strings.HasPrefix(rest, "**") {
		return Concern{}, false
	}
	rest = strings.TrimLeft(rest[2:], " \t")
	for _, sep := range []string{"—", "–", ":"} {
		if strings.HasPrefix(rest, sep) {
			rest = rest[len(sep):]
			goto separatorDone
		}
	}
	// `-(?!>)`: a hyphen followed by `>` is the head of an ASCII `->` arrow. Consuming
	// it would leave the concern parsed as `> ...` with the arrow split never firing.
	if strings.HasPrefix(rest, "-") && !strings.HasPrefix(rest, "->") {
		rest = rest[1:]
	}
separatorDone:
	rest = strings.TrimRight(strings.TrimLeft(rest, " \t"), " \t")
	if rest == "" {
		return Concern{}, false
	}
	return Concern{Verdict: verdict, Concern: rest}, true
}

// arrowSplit is the template's `→`, or its ASCII spelling, separating the concern from
// what was done about it.
var arrowSplitRe = regexp.MustCompile(`\s*(?:→|->)\s*`)

// ParseLedgerText turns the verdict bullets of one ledger message into the row fields
// they become.
func ParseLedgerText(text string) gate.Row {
	var concerns []Concern
	accepted, rejected := 0, 0
	for _, line := range strings.Split(text, "\n") {
		parsed, ok := parseLedgerLine(line)
		if !ok {
			continue
		}
		// Keep the concern, not the resolution: an integer alone is unreadable a month
		// later. Everything downstream of the arrow is what was done, not the claim.
		claim := arrowSplitRe.Split(parsed.Concern, 2)[0]
		claim = strings.TrimSpace(claim)
		if len(claim) > maxConcernChars {
			claim = claim[:maxConcernChars]
		}
		concerns = append(concerns, Concern{Verdict: parsed.Verdict, Concern: claim})
		if parsed.Verdict == "ACCEPTED" {
			accepted++
		} else {
			rejected++
		}
	}
	stored := concerns
	if len(stored) > maxConcerns {
		stored = stored[:maxConcerns]
	}
	row := gate.Row{
		"self_reported_accepted": accepted,
		"self_reported_rejected": rejected,
		// concerns_total, because the counts run over the FULL list while the stored
		// text is capped. Rows carrying more verdicts than concerns read as a parse
		// bug; without this field a reader cannot tell truncation from one.
		"concerns_total": len(concerns),
		"concerns":       stored,
	}
	if round, ok := LedgerRound(text); ok {
		row["ledger_round"] = round
	} else {
		row["ledger_round"] = nil
	}
	return row
}

// HasLedgerLine reports whether any line of the text is a verdict bullet.
func HasLedgerLine(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if _, ok := parseLedgerLine(line); ok {
			return true
		}
	}
	return false
}

// ReadLedger recovers the agent's ACCEPT/REJECT ledger for a fired gate from the
// transcript. This is the SELF-REPORTED half of the retry row and is not an oracle.
//
// A nil return is itself signal, not just a parse failure: the template calls the report
// mandatory, so a fired gate with no ledger means the operator was told nothing.
func ReadLedger(transcript string, sinceTS float64, firing int) gate.Row {
	if transcript == "" {
		return nil
	}
	lines := ReadTranscript(transcript)
	if len(lines) == 0 {
		return nil
	}
	var latest, marked string
	for _, line := range lines {
		// Cheap prefilter: skip the JSON parse for the rows that cannot match.
		if !strings.Contains(line, LedgerHeader) {
			continue
		}
		var row transcriptRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		// A subagent's text never reached the operator, so it is not a report to them
		// no matter what it contains.
		if row.Type != "assistant" || row.IsSidechain {
			continue
		}
		when := isoEpoch(row.Timestamp)
		if when == nil || *when < sinceTS {
			continue
		}
		var message transcriptMessage
		if err := json.Unmarshal(row.Message, &message); err != nil {
			continue
		}
		for _, text := range textBlocks(message.Content) {
			if !strings.Contains(text, LedgerHeader) || !HasLedgerLine(text) {
				continue
			}
			// A ledger naming a DIFFERENT round answers a different round, so it is not
			// this row's ledger no matter how close in time it landed. An unmarked
			// ledger still counts: the marker is newer than some of the transcripts.
			got, hasRound := LedgerRound(text)
			if firing > 0 && hasRound && got != firing {
				continue
			}
			// Last qualifying message wins: if the agent restates the ledger after
			// further work, the restatement is the more accurate account.
			latest = text
			if hasRound {
				marked = text
			}
		}
	}
	// A ledger naming this round beats an unmarked one that merely came later.
	if marked != "" {
		latest = marked
	}
	if latest == "" {
		return nil
	}
	return ParseLedgerText(latest)
}
