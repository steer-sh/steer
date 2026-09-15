package gate

import (
	"regexp"
	"strconv"
	"strings"
)

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

// ledgerSectionRe reads one round's section out of a chain ledger: `**Round 3/4**` or
// `**Round 5, terminal**`. A ledger carrying these answers every round of its chain, so
// it belongs to no single round and only the resolver can join it.
var ledgerSectionRe = regexp.MustCompile(`(?im)^[ \t]*(?:[-+>]\s*)?\**\s*Round\s+\d+\s*[/,]`)

// HasRoundSections reports whether the text is a chain ledger — one report covering
// every round, rather than one round's own.
func HasRoundSections(text string) bool { return ledgerSectionRe.MatchString(text) }

const (
	// MaxConcerns bounds a row's size. It is not a claim about how many concerns a
	// round may raise: a row that hits it still reports the true total in
	// concerns_total.
	MaxConcerns     = 24
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

// arrowSplit is the template's delimiter between the concern and its resolution. Both
// sides require whitespace so an arrow inside a token such as `A->B` remains concern
// text rather than silently changing its measurement key.
var arrowSplitRe = regexp.MustCompile(`[ \t]+(?:→|->)[ \t]+`)

// ParseLedgerText turns the verdict bullets of one ledger message into the row fields
// they become.
func ParseLedgerText(text string) Row {
	// Non-nil so an empty result marshals to [] like Python's list, not null.
	concerns := []Concern{}
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
		// Cap by rune, as Python slices a str by code point; a byte cap splits a
		// multibyte char and drops a different last word on the same text.
		if r := []rune(claim); len(r) > maxConcernChars {
			claim = string(r[:maxConcernChars])
		}
		concerns = append(concerns, Concern{Verdict: parsed.Verdict, Concern: claim})
		if parsed.Verdict == "ACCEPTED" {
			accepted++
		} else {
			rejected++
		}
	}
	stored := concerns
	if len(stored) > MaxConcerns {
		stored = stored[:MaxConcerns]
	}
	row := Row{
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

// ledgerChainRe matches a chain ledger's header line. One message can carry more than
// one, so a candidate is a BLOCK of a message, never the whole message.
var ledgerChainRe = regexp.MustCompile(
	`(?im)^[ \t]*(?:[-+>]\s*)?\**\s*` + regexp.QuoteMeta(LedgerHeader) + `[^\n]*chain ledger`)

// ledgerSectionCapRe is ledgerSectionRe with the round number captured.
var ledgerSectionCapRe = regexp.MustCompile(`(?im)^[ \t]*(?:[-+>]\s*)?\**\s*Round\s+(\d+)\s*[/,]`)

// blockSubjectRe reads the commit subject a chain ledger's header line names, after
// the chain ID when the header carries one.
var blockSubjectRe = regexp.MustCompile(`(?i)chain ledger(?:\s*#[0-9a-f]+)?\s*,\s*(.+?)\s*\**\s*$`)

// blockChainIDRe reads the chain ID a chain ledger's header line carries.
var blockChainIDRe = regexp.MustCompile(`(?i)chain ledger\s*#([0-9a-f]+)\b`)

// LedgerRounds returns the rounds a chain ledger reports on. Empty for a legacy
// per-round ledger, whose one round lives in the header LedgerRound reads.
func LedgerRounds(text string) map[int]bool {
	out := map[int]bool{}
	for _, m := range ledgerSectionCapRe.FindAllStringSubmatch(text, -1) {
		if n, ok := atoiOK(m[1]); ok {
			out[n] = true
		}
	}
	return out
}

// ChainBlocks returns each chain ledger inside one message, header line first.
func ChainBlocks(text string) []string {
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

// BlockSubject returns the commit subject a chain ledger's HEADER names, or "". Only the
// first line is read: a subject quoted inside a bullet names no commit.
func BlockSubject(block string) string {
	first := block
	if idx := strings.IndexByte(block, '\n'); idx >= 0 {
		first = block[:idx]
	}
	if m := blockSubjectRe.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

// BlockChainID returns the chain ID a chain ledger's HEADER carries, lowercased, or ""
// for a header written without one.
func BlockChainID(block string) string {
	first := block
	if idx := strings.IndexByte(block, '\n'); idx >= 0 {
		first = block[:idx]
	}
	if m := blockChainIDRe.FindStringSubmatch(first); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// LedgerSection returns one round's slice of a chain ledger: its section heading up to
// the next one, or "" when the ledger carries no section for that round.
func LedgerSection(text string, firing int) string {
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
