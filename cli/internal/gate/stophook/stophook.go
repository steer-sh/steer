// Package stophook is the commit gate's root-Stop backstop. When the gate released a
// review sequence that blocked in this turn, and no assistant reply text holds that
// sequence's complete ledger, the Stop is refused once and the missing ledgers named.
//
// Every doubt fails open: a partial read is never proof that a ledger is absent.
package stophook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	gatecodex "github.com/steer-sh/steer-cli/internal/gate/codex"
)

// Payload is the part of a Claude Code or Codex Stop payload the backstop reads.
// StopHookActive and LastAssistantMessage are nil when the payload omits them.
type Payload struct {
	HookEventName        string  `json:"hook_event_name"`
	SessionID            string  `json:"session_id"`
	TranscriptPath       string  `json:"transcript_path"`
	PromptID             string  `json:"prompt_id"`
	TurnID               string  `json:"turn_id"`
	AgentID              string  `json:"agent_id"`
	StopHookActive       *bool   `json:"stop_hook_active"`
	LastAssistantMessage *string `json:"last_assistant_message"`
}

// Result is the backstop's answer to one hook payload.
type Result struct {
	// RootStop says the payload is a root thread's Stop event. Its caller then owes the
	// harness at most one JSON reply on stdout and no narration there.
	RootStop bool
	// Block refuses the Stop with Reason as the agent's next input.
	Block  bool
	Reason string
	// SystemMessage is an operator notice, set when the backstop cannot act.
	SystemMessage string
}

// turnKeys is the payload field that names the turn, by harness.
var turnKeys = map[string]string{"claude-code": "prompt_id", "codex": "turn_id"}

func (p Payload) turn(harness string) string {
	if harness == "codex" {
		return p.TurnID
	}
	return p.PromptID
}

// obligation is one released sequence whose ledger the turn owes.
type obligation struct {
	header   string
	chainID  string
	blocks   int
	terminal int
}

// rounds lists the section headings the ledger needs, terminal round last.
func (o obligation) rounds() []int {
	out := make([]int, 0, o.blocks+1)
	for r := 1; r <= o.blocks; r++ {
		out = append(out, r)
	}
	if o.terminal > 0 {
		out = append(out, o.terminal)
	}
	return out
}

func (o obligation) headings() string {
	var parts []string
	for r := 1; r <= o.blocks; r++ {
		parts = append(parts, fmt.Sprintf("**Round %d, blocking**", r))
	}
	if o.terminal > 0 {
		parts = append(parts, fmt.Sprintf("**Round %d, terminal**", o.terminal))
	}
	return strings.Join(parts, ", ")
}

// Check answers one hook payload against the adjudication log at logFile.
func Check(logFile string, raw []byte) Result {
	var p Payload
	if json.Unmarshal(raw, &p) != nil || p.HookEventName != "Stop" || p.AgentID != "" {
		return Result{}
	}
	res := Result{RootStop: true}
	// One chance only: a Stop that a hook already refused, or a payload that does not
	// say, is never refused again.
	if p.StopHookActive == nil || *p.StopHookActive || p.SessionID == "" {
		return res
	}
	owed, harness, unlocated, ok := obligations(logFile, p)
	if !ok {
		return res
	}
	if len(owed) == 0 {
		if unlocated != "" {
			res.SystemMessage = "Ledger backstop unavailable at this Stop: the Stop payload " +
				"supplied no usable " + turnKeys[unlocated] + "."
		}
		return res
	}
	texts, ok := replyTexts(harness, p)
	if !ok {
		return res
	}
	var missing []obligation
	for _, o := range owed {
		if !anyComplete(texts, o) {
			missing = append(missing, o)
		}
	}
	if len(missing) > 0 {
		res.Block, res.Reason = true, reason(missing)
	}
	return res
}

// obligations streams the log once and keeps the release rows this Stop's turn owns, in
// log order, one per repository and sequence. unlocated names a harness whose release
// rows share this session while the Stop names no turn. ok is false when the log could
// not be read through, or a line that does not parse is the last one or names a release
// or this session. An older torn line naming neither stays in the log for good and
// cannot hide this turn's release.
func obligations(logFile string, p Payload) (owed []obligation, harness, unlocated string, ok bool) {
	file, err := os.Open(logFile)
	if err != nil {
		return nil, "", "", false
	}
	defer file.Close()
	index := map[string]int{}
	var last []byte
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if torn(line, p.SessionID) {
			return nil, "", "", false
		}
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			last = trimmed
		}
		if len(line) > 0 && strings.Contains(string(line), gate.PhaseChainEnd) {
			if o, h, repo, owned := ownedRelease(line, p, &unlocated); owned {
				key := repo + "\x00" + o.chainID
				if i, seen := index[key]; seen {
					owed[i] = o
				} else {
					index[key] = len(owed)
					owed = append(owed, o)
				}
				harness = h
			}
		}
		if readErr == io.EOF {
			if len(last) > 0 && !json.Valid(last) {
				return nil, "", "", false
			}
			return owed, harness, unlocated, true
		}
		if readErr != nil {
			return nil, "", "", false
		}
	}
}

// torn reports whether a log line that does not parse names a release or this session.
func torn(line []byte, session string) bool {
	text := strings.TrimSpace(string(line))
	if text == "" || json.Valid([]byte(text)) {
		return false
	}
	return strings.Contains(text, gate.PhaseChainEnd) || strings.Contains(text, session)
}

// ownedRelease reports whether one log line is a release row this Stop's turn owns: the
// same root session and thread, turn and transcript, under a harness whose turn field
// the Stop names.
func ownedRelease(line []byte, p Payload, unlocated *string) (o obligation, harness, repo string, owned bool) {
	var row struct {
		Phase       string `json:"phase"`
		Harness     string `json:"harness"`
		Channel     string `json:"channel"`
		SessionID   string `json:"session_id"`
		ThreadID    string `json:"thread_id"`
		Thread      string `json:"release_thread"`
		Turn        string `json:"release_turn"`
		TurnPresent bool   `json:"release_turn_present"`
		Transcript  string `json:"transcript"`
		Repo        string `json:"repo"`
		ChainID     string `json:"chain_id"`
		Header      string `json:"ledger_header"`
		Blocks      int    `json:"blocks"`
		Terminal    int    `json:"terminal_round"`
	}
	if json.Unmarshal(line, &row) != nil || row.Phase != gate.PhaseChainEnd ||
		row.Channel != gate.ChannelPreToolUse || row.SessionID != p.SessionID {
		return obligation{}, "", "", false
	}
	if _, known := turnKeys[row.Harness]; !known {
		return obligation{}, "", "", false
	}
	turn := p.turn(row.Harness)
	if turn == "" {
		*unlocated = row.Harness
		return obligation{}, "", "", false
	}
	if !row.TurnPresent || row.Turn != turn || row.Thread != "" || row.ThreadID != "" ||
		row.Transcript == "" || row.Transcript != p.TranscriptPath ||
		row.ChainID == "" || row.Header == "" || row.Blocks < 1 {
		return obligation{}, "", "", false
	}
	o = obligation{header: row.Header, chainID: strings.ToLower(row.ChainID), blocks: row.Blocks, terminal: row.Terminal}
	return o, row.Harness, row.Repo, true
}

// replyTexts is every assistant reply text this Stop can see: each ledger text block the
// resolver's harvest reads from the transcript under this session's key, and the
// payload's last message as a candidate of its own. A ledger the harvest cannot read
// is not counted, so a refusal asks for one it can. ok is false when
// the transcript could not be read whole or does not belong to the payload.
func replyTexts(harness string, p Payload) ([]string, bool) {
	var texts []string
	switch harness {
	case "claude-code":
		msgs, err := cc.ReadLedgers(p.TranscriptPath)
		if err != nil {
			return nil, false
		}
		for _, m := range msgs {
			if m.Key == p.SessionID {
				texts = append(texts, m.Text)
			}
		}
	case "codex":
		rollout, err := gatecodex.ReadRollout(p.TranscriptPath)
		if err != nil || rollout.Threaded || rollout.Key != p.SessionID {
			return nil, false
		}
		for _, m := range rollout.Ledgers {
			texts = append(texts, m.Text)
		}
	default:
		return nil, false
	}
	if p.LastAssistantMessage != nil && *p.LastAssistantMessage != "" {
		texts = append(texts, *p.LastAssistantMessage)
	}
	return texts, true
}

// anyComplete reports whether one text holds one ledger block for o's sequence with a
// section and a verdict bullet for every round o requires.
func anyComplete(texts []string, o obligation) bool {
	for _, text := range texts {
		for _, block := range gate.ChainBlocks(text) {
			if gate.BlockChainID(block) != o.chainID {
				continue
			}
			whole := true
			for _, r := range o.rounds() {
				if section := gate.LedgerSection(block, r); section == "" || !gate.HasLedgerLine(section) {
					whole = false
					break
				}
			}
			if whole {
				return true
			}
		}
	}
	return false
}

func reason(missing []obligation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[commit-gate] This turn ended %d review sequence(s) with no complete ledger "+
		"in your reply text. Write each ledger now, as ordinary text in your final reply. "+
		"Thinking text does not count, and one ledger must not be split across messages.\n\n"+
		"Copy each header line as it stands, one header per sequence, and give each listed "+
		"round its own section with an ACCEPTED or REJECTED line for each concern:\n", len(missing))
	for _, o := range missing {
		fmt.Fprintf(&b, "\n%s\nSections: %s\n", o.header, o.headings())
	}
	b.WriteString("\nThe ledger rules are unchanged: every line is self-contained; a finding " +
		"whose premise holds is ACCEPTED even when you leave it unfixed (**ACCEPTED, not fixed**); " +
		"a REJECTED line names the premise and why it is false; each round ends with a " +
		"\"Weakest surviving\" line. The gate let these commits through; it did not check " +
		"that git made them. Do not make a new commit for this.")
	return b.String()
}
