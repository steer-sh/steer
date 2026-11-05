package stophook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// fixture is one session's log and transcript on disk.
type fixture struct {
	t          *testing.T
	dir        string
	log        string
	transcript string
	harness    string
}

func newFixture(t *testing.T, harness string) *fixture {
	t.Helper()
	dir := t.TempDir()
	return &fixture{t: t, dir: dir, log: filepath.Join(dir, "adjudication.jsonl"),
		transcript: filepath.Join(dir, "session.jsonl"), harness: harness}
}

// release appends a release row; fields override the defaults of a root-thread release
// in turn "turn-1" of session "s1".
func (f *fixture) release(chainID string, blocks int, fields map[string]any) {
	f.t.Helper()
	row := map[string]any{
		"ts": 1, "phase": gate.PhaseChainEnd, "harness": f.harness, "channel": gate.ChannelPreToolUse,
		"session_id": "s1", "release_thread": "", "release_turn": "turn-1", "release_turn_present": true,
		"transcript": f.transcript, "repo": "/repo", "chain_id": chainID, "blocks": blocks,
		"ledger_header": header(chainID, "add mod"),
	}
	for k, v := range fields {
		row[k] = v
	}
	data, _ := json.Marshal(row)
	file, err := os.OpenFile(f.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	file.Write(append(data, '\n'))
}

// replies writes the transcript: one assistant message per text, in the harness's shape.
func (f *fixture) replies(texts ...string) {
	f.t.Helper()
	var lines []string
	if f.harness == "codex" {
		lines = append(lines, `{"timestamp":"2026-10-09T10:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"s1","id":"s1"}}`)
	}
	for i, text := range texts {
		lines = append(lines, f.assistantLine(i+1, "text", text))
	}
	f.write(lines...)
}

func (f *fixture) assistantLine(i int, kind, text string) string {
	var row map[string]any
	if f.harness == "codex" {
		itemType := "output_text"
		if kind != "text" {
			itemType = kind
		}
		row = map[string]any{"timestamp": "2026-10-09T10:00:00.000Z", "ordinal": i, "type": "response_item",
			"payload": map[string]any{"type": "message", "role": "assistant", "phase": "final_answer",
				"content": []any{map[string]any{"type": itemType, "text": text}}}}
	} else {
		block := map[string]any{"type": kind, kind: text}
		row = map[string]any{"type": "assistant", "sessionId": "s1", "timestamp": "2026-10-09T10:00:00.000Z",
			"message": map[string]any{"role": "assistant", "content": []any{block}}}
	}
	data, _ := json.Marshal(row)
	return string(data)
}

func (f *fixture) write(lines ...string) {
	f.t.Helper()
	if err := os.WriteFile(f.transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// stop runs the check on a root Stop of turn "turn-1"; fields override the payload.
func (f *fixture) stop(fields map[string]any) Result {
	f.t.Helper()
	payload := map[string]any{"hook_event_name": "Stop", "session_id": "s1",
		"transcript_path": f.transcript, "stop_hook_active": false}
	if f.harness == "codex" {
		payload["turn_id"] = "turn-1"
	} else {
		payload["prompt_id"] = "turn-1"
	}
	for k, v := range fields {
		if v == nil {
			delete(payload, k)
		} else {
			payload[k] = v
		}
	}
	raw, _ := json.Marshal(payload)
	return Check(f.log, raw)
}

func header(chainID, subject string) string {
	return fmt.Sprintf("**%s — review sequence #%s, %s**", gate.LedgerHeader, chainID, subject)
}

// ledger is a complete ledger for chainID with one section per round: blocking
// rounds first, then a terminal one when terminal is above zero.
func ledger(chainID string, blocks, terminal int) string {
	var b strings.Builder
	b.WriteString(header(chainID, "add mod") + "\n\n")
	section := func(r int, kind string) {
		fmt.Fprintf(&b, "**Round %d, %s** — 3 reviewers, 3 raw findings, 1 distinct concerns.\n\n"+
			"- **REJECTED** — the loop never ends → the premise is false: n is bounded\n"+
			"- Weakest surviving: none survived.\n\n", r, kind)
	}
	for r := 1; r <= blocks; r++ {
		section(r, "blocking")
	}
	if terminal > 0 {
		section(terminal, "terminal")
	}
	return b.String()
}

var harnesses = []string{"claude-code", "codex"}

func TestAReleaseWithNoLedgerInReplyTextBlocksOnce(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			f := newFixture(t, h)
			f.release("ab12", 2, nil)
			f.replies("Done; the commit landed.")
			res := f.stop(nil)
			if !res.RootStop || !res.Block {
				t.Fatalf("result = %+v, want a block", res)
			}
			for _, want := range []string{header("ab12", "add mod"), "**Round 1, blocking**, **Round 2, blocking**",
				"Thinking text does not count", "Do not make a new commit"} {
				if !strings.Contains(res.Reason, want) {
					t.Errorf("the reason does not name %q:\n%s", want, res.Reason)
				}
			}
			for _, active := range []any{true, nil} {
				if res := f.stop(map[string]any{"stop_hook_active": active}); res.Block || !res.RootStop {
					t.Errorf("stop_hook_active %v: result = %+v, want a root Stop with no block", active, res)
				}
			}
		})
	}
}

func TestACompleteLedgerInReplyTextSatisfiesTheRelease(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			f := newFixture(t, h)
			f.release("ab12", 2, nil)
			f.release("cd34", 1, map[string]any{"terminal_round": 2})
			f.replies("Here are both ledgers.\n\n" + ledger("ab12", 2, 0) + "\n" + ledger("cd34", 1, 2))
			if res := f.stop(nil); res.Block || res.SystemMessage != "" {
				t.Errorf("result = %+v, want no reply", res)
			}
		})
	}
}

func TestOnlyReplyTextCountsAsALedger(t *testing.T) {
	text := ledger("ab12", 1, 0)
	for _, tc := range []struct {
		name    string
		harness string
		line    func(f *fixture) string
	}{
		{"claude-code thinking", "claude-code", func(f *fixture) string { return f.assistantLine(1, "thinking", text) }},
		{"claude-code user text", "claude-code", func(f *fixture) string {
			data, _ := json.Marshal(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-10-09T10:00:00.000Z",
				"message": map[string]any{"role": "user", "content": text}})
			return string(data)
		}},
		{"claude-code tool output", "claude-code", func(f *fixture) string {
			data, _ := json.Marshal(map[string]any{"type": "user", "sessionId": "s1", "timestamp": "2026-10-09T10:00:00.000Z",
				"message": map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": text}}}})
			return string(data)
		}},
		{"codex reasoning", "codex", func(f *fixture) string {
			data, _ := json.Marshal(map[string]any{"timestamp": "2026-10-09T10:00:00.000Z", "ordinal": 1, "type": "response_item",
				"payload": map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": text}}}})
			return string(data)
		}},
		{"codex analysis channel", "codex", func(f *fixture) string {
			data, _ := json.Marshal(map[string]any{"timestamp": "2026-10-09T10:00:00.000Z", "ordinal": 1, "type": "response_item",
				"payload": map[string]any{"type": "message", "role": "assistant", "channel": "analysis",
					"content": []any{map[string]any{"type": "output_text", "text": text}}}})
			return string(data)
		}},
		{"codex tool output", "codex", func(f *fixture) string {
			data, _ := json.Marshal(map[string]any{"timestamp": "2026-10-09T10:00:00.000Z", "ordinal": 1, "type": "response_item",
				"payload": map[string]any{"type": "function_call_output", "call_id": "c1", "output": text}})
			return string(data)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.harness)
			f.release("ab12", 1, nil)
			var lines []string
			if tc.harness == "codex" {
				lines = append(lines, `{"timestamp":"2026-10-09T10:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"s1","id":"s1"}}`)
			}
			f.write(append(lines, tc.line(f))...)
			if res := f.stop(nil); !res.Block {
				t.Errorf("result = %+v, want a block", res)
			}
		})
	}
}

func TestAnIncompleteLedgerDoesNotSatisfyTheRelease(t *testing.T) {
	complete := ledger("ab12", 2, 0)
	round2 := strings.Index(complete, "**Round 2")
	for _, tc := range []struct {
		name     string
		terminal int
		replies  []string
	}{
		{"header only", 0, []string{header("ab12", "add mod") + "\n\nI rejected everything."}},
		{"a bare sequence mention", 0, []string{"See review sequence #ab12 above."}},
		{"a missing round", 0, []string{complete[:round2]}},
		{"a section with no verdict line", 0, []string{strings.Replace(complete, "- **REJECTED**", "- rejected", 1)}},
		{"rounds split across messages", 0, []string{complete[:round2], header("ab12", "add mod") + "\n\n" + complete[round2:]}},
		{"no terminal round", 3, []string{complete}},
		{"another sequence's ledger", 0, []string{ledger("ef56", 2, 0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "claude-code")
			fields := map[string]any{}
			if tc.terminal > 0 {
				fields["terminal_round"] = tc.terminal
			}
			f.release("ab12", 2, fields)
			f.replies(tc.replies...)
			if res := f.stop(nil); !res.Block {
				t.Errorf("result = %+v, want a block", res)
			}
		})
	}
}

func TestTheLastAssistantMessageIsItsOwnCandidate(t *testing.T) {
	complete := ledger("ab12", 2, 0)
	round2 := strings.Index(complete, "**Round 2")
	f := newFixture(t, "codex")
	f.release("ab12", 2, nil)
	f.replies("Committed.")
	if res := f.stop(map[string]any{"last_assistant_message": complete}); res.Block {
		t.Errorf("a complete ledger in last_assistant_message still blocked: %+v", res)
	}
	f.replies(complete[:round2])
	if res := f.stop(map[string]any{"last_assistant_message": header("ab12", "add mod") + "\n\n" + complete[round2:]}); !res.Block {
		t.Errorf("halves in the transcript and the payload were joined: %+v", res)
	}
}

func TestOnlyThisTurnsRootReleasesAreOwed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
	}{
		{"an earlier turn", map[string]any{"release_turn": "turn-0"}},
		{"another session", map[string]any{"session_id": "s2"}},
		{"a Claude Code subagent", map[string]any{"release_thread": "agent-1"}},
		{"a Codex child thread", map[string]any{"thread_id": "thread-9"}},
		{"another transcript", map[string]any{"transcript": "/elsewhere.jsonl"}},
		{"no recorded turn", map[string]any{"release_turn": "", "release_turn_present": false}},
		{"the pre-commit channel", map[string]any{"channel": gate.ChannelPreCommit}},
		{"another harness", map[string]any{"harness": "pi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "claude-code")
			f.release("ab12", 1, tc.fields)
			f.replies("Committed.")
			if res := f.stop(nil); res.Block || !res.RootStop {
				t.Errorf("result = %+v, want a root Stop with no block", res)
			}
		})
	}
}

func TestSequencesAreOwedByRepositoryAndID(t *testing.T) {
	f := newFixture(t, "claude-code")
	f.release("ab12", 1, map[string]any{"repo": "/one"})
	f.release("cd34", 1, map[string]any{"repo": "/two"})
	f.replies(ledger("ab12", 1, 0))
	res := f.stop(nil)
	if !res.Block || strings.Contains(res.Reason, header("ab12", "add mod")) || !strings.Contains(res.Reason, header("cd34", "add mod")) {
		t.Errorf("result = %+v, want a block naming cd34 alone", res)
	}
}

func TestEveryDoubtFailsOpen(t *testing.T) {
	t.Run("missing log", func(t *testing.T) {
		f := newFixture(t, "claude-code")
		f.replies("Committed.")
		if res := f.stop(nil); res.Block {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("missing transcript", func(t *testing.T) {
		f := newFixture(t, "claude-code")
		f.release("ab12", 1, nil)
		if res := f.stop(nil); res.Block {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("truncated ledger line", func(t *testing.T) {
		f := newFixture(t, "claude-code")
		f.release("ab12", 1, nil)
		line := f.assistantLine(1, "text", ledger("ab12", 1, 0))
		f.write(line[:len(line)-10])
		if res := f.stop(nil); res.Block {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("codex rollout of another session", func(t *testing.T) {
		f := newFixture(t, "codex")
		f.release("ab12", 1, nil)
		f.write(`{"timestamp":"2026-10-09T10:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"s9","id":"s9"}}`)
		if res := f.stop(nil); res.Block {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("malformed payload", func(t *testing.T) {
		f := newFixture(t, "claude-code")
		f.release("ab12", 1, nil)
		if res := Check(f.log, []byte("{not json")); res.Block || res.RootStop {
			t.Errorf("result = %+v", res)
		}
		if res := Check(f.log, nil); res.Block || res.RootStop {
			t.Errorf("empty input: result = %+v", res)
		}
	})
}

func TestAStopWithoutItsTurnFieldNamesTheGap(t *testing.T) {
	f := newFixture(t, "claude-code")
	f.release("ab12", 1, nil)
	f.replies("Committed.")
	res := f.stop(map[string]any{"prompt_id": nil})
	if res.Block || res.SystemMessage != "Ledger backstop unavailable at this Stop: the Stop payload supplied no usable prompt_id." {
		t.Errorf("result = %+v, want the unavailable notice and no block", res)
	}
}

func TestOnlyARootStopIsChecked(t *testing.T) {
	f := newFixture(t, "codex")
	f.release("ab12", 1, nil)
	f.replies("Committed.")
	for _, fields := range []map[string]any{
		{"hook_event_name": "SubagentStop"},
		{"agent_id": "thread-9"},
		{"hook_event_name": "PreToolUse"},
	} {
		if res := f.stop(fields); res.Block || res.RootStop {
			t.Errorf("%v: result = %+v, want no root Stop", fields, res)
		}
	}
}

// ccRow is a Claude Code assistant row holding one text block of message id.
func ccRow(id, text string, fields map[string]any) string {
	row := map[string]any{"type": "assistant", "sessionId": "s1", "timestamp": "2026-10-09T10:00:00.000Z",
		"message": map[string]any{"id": id, "role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}}
	for k, v := range fields {
		if v == nil {
			delete(row, k)
		} else {
			row[k] = v
		}
	}
	data, _ := json.Marshal(row)
	return string(data)
}

// codexMessage is a Codex assistant message line with one output_text item per text.
func codexMessage(fields map[string]any, texts ...string) string {
	items := make([]any, len(texts))
	for i, text := range texts {
		items[i] = map[string]any{"type": "output_text", "text": text}
	}
	row := map[string]any{"timestamp": "2026-10-09T10:00:00.000Z", "ordinal": 1, "type": "response_item",
		"payload": map[string]any{"type": "message", "role": "assistant", "content": items}}
	for k, v := range fields {
		if v == nil {
			delete(row, k)
		} else {
			row[k] = v
		}
	}
	data, _ := json.Marshal(row)
	return string(data)
}

const codexMeta = `{"timestamp":"2026-10-09T10:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"s1","id":"s1"}}`

// The resolver reads one text block at a time, so a ledger split across one message's
// blocks is refused: Claude Code writes each block as its own row under the message's
// id, and Codex keeps several text items in one message.
func TestALedgerSplitAcrossTextBlocksIsRefused(t *testing.T) {
	complete := ledger("ab12", 2, 0)
	round2 := strings.Index(complete, "**Round 2")
	cc := newFixture(t, "claude-code")
	cc.release("ab12", 2, nil)
	cc.write(ccRow("msg-1", complete[:round2], nil), ccRow("msg-1", complete[round2:], nil))
	if res := cc.stop(nil); !res.Block {
		t.Errorf("claude-code: a ledger split across one message's rows satisfied the Stop: %+v", res)
	}
	cc.write(ccRow("msg-1", complete, nil))
	if res := cc.stop(nil); res.Block {
		t.Errorf("claude-code: the ledger in one block still blocked: %+v", res)
	}

	codex := newFixture(t, "codex")
	codex.release("ab12", 2, nil)
	codex.write(codexMeta, codexMessage(nil, complete[:round2], complete[round2:]))
	if res := codex.stop(nil); !res.Block {
		t.Errorf("codex: a ledger split across one message's items satisfied the Stop: %+v", res)
	}
}

// A ledger with no readable timestamp is a shape the resolver cannot place, so the
// check fails open rather than refuse it.
func TestAnUndatedLedgerFailsOpen(t *testing.T) {
	cc := newFixture(t, "claude-code")
	cc.release("ab12", 1, nil)
	cc.write(ccRow("msg-1", ledger("ab12", 1, 0), map[string]any{"timestamp": nil}))
	if res := cc.stop(nil); res.Block {
		t.Errorf("claude-code: result = %+v", res)
	}
	codex := newFixture(t, "codex")
	codex.release("ab12", 1, nil)
	codex.write(codexMeta, codexMessage(map[string]any{"timestamp": "not a time"}, ledger("ab12", 1, 0)))
	if res := codex.stop(nil); res.Block {
		t.Errorf("codex: result = %+v", res)
	}
}

// A torn line is an incomplete read even when the tear falls before the header text.
func TestATornLineBeforeTheHeaderFailsOpen(t *testing.T) {
	cc := newFixture(t, "claude-code")
	cc.release("ab12", 1, nil)
	line := ccRow("msg-2", ledger("ab12", 1, 0), nil)
	cc.write(ccRow("msg-1", "Committed.", nil), line[:strings.Index(line, gate.LedgerHeader)])
	if res := cc.stop(nil); res.Block {
		t.Errorf("claude-code: result = %+v", res)
	}
	codex := newFixture(t, "codex")
	codex.release("ab12", 1, nil)
	line = codexMessage(nil, ledger("ab12", 1, 0))
	codex.write(codexMeta, line[:strings.Index(line, gate.LedgerHeader)])
	if res := codex.stop(nil); res.Block {
		t.Errorf("codex: result = %+v", res)
	}
}

// A torn log line fails open when it could hide this session's release: the last line,
// or one naming a release or the session. An older torn line naming neither does not.
func TestATornReleaseRowFailsOpen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		torn  string
		block bool
	}{
		{"a torn last line", `{"ts":2,"phase":"del`, false},
		{"a torn release row", `{"ts":2,"phase":"chain-end","harness":"claude` + "\n" + `{"ts":3,"phase":"clean"}`, false},
		{"a torn row of this session", `{"session_id":"s1","ts":2` + "\n" + `{"ts":3,"phase":"clean"}`, false},
		{"an older torn row of another kind", `{"ts":0,"phase":"de` + "\n" + `{"ts":3,"phase":"clean"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "claude-code")
			f.release("ab12", 1, nil)
			file, err := os.OpenFile(f.log, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			file.WriteString(tc.torn)
			file.Close()
			f.replies("Committed.")
			if res := f.stop(nil); res.Block != tc.block {
				t.Errorf("result = %+v, want block %v", res, tc.block)
			}
		})
	}
}

// A ledger in a row of another session is one the resolver cannot join to this
// session's rows, so it does not count.
func TestALedgerUnderAnotherSessionDoesNotCount(t *testing.T) {
	f := newFixture(t, "claude-code")
	f.release("ab12", 1, nil)
	f.write(ccRow("msg-1", ledger("ab12", 1, 0), map[string]any{"sessionId": "s2"}))
	if res := f.stop(nil); !res.Block {
		t.Errorf("another session's ledger satisfied the Stop: %+v", res)
	}
}
