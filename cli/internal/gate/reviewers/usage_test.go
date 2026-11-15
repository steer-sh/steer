package reviewers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// exitingBinary writes a stub that prints stdout and stderr and exits with code.
func exitingBinary(t *testing.T, stdout, stderr string, code string) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "stub")
	script := "#!/bin/sh\ncat >/dev/null\ncat <<'OUTEOF'\n" + stdout + "\nOUTEOF\ncat >&2 <<'ERREOF'\n" + stderr + "\nERREOF\nexit " + code + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return binary
}

func intp(v int) *int { return &v }

// wantUsage compares a normalized usage field by field, nil meaning not reported.
func wantUsage(t *testing.T, got *gate.TokenUsage, input, cached, output, reasoning *int, cost *float64) {
	t.Helper()
	if got == nil {
		t.Fatal("Usage = nil")
	}
	same := func(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	sameF := func(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	if !same(got.Input, input) || !same(got.CachedInput, cached) || !same(got.Output, output) ||
		!same(got.Reasoning, reasoning) || !sameF(got.CostUSD, cost) {
		show := func(v *int) any {
			if v == nil {
				return nil
			}
			return *v
		}
		t.Errorf("Usage = input %v cached %v output %v reasoning %v cost %v",
			show(got.Input), show(got.CachedInput), show(got.Output), show(got.Reasoning), got.CostUSD)
	}
}

// claudeCaptured is a real `claude -p --output-format json` reply, trimmed of fields
// the reviewer does not read.
const claudeCaptured = `{"duration_api_ms":2056,"stop_reason":"end_turn","total_cost_usd":0.015208300000000001,"usage":{"input_tokens":2,"cache_creation_input_tokens":3288,"cache_read_input_tokens":3223,"output_tokens":173,"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard"},"modelUsage":{"claude-sonnet-5-5":{"inputTokens":2,"outputTokens":173,"cacheReadInputTokens":3223,"cacheCreationInputTokens":3288,"costUSD":0.015208300000000001}},"is_error":false,"num_turns":1,"subtype":"success","result":"NO FINDINGS","type":"result"}`

// Claude's input_tokens excludes both cache terms, so the recorded input adds them,
// and the cost is the reply's own total_cost_usd.
func TestClaudeCodeRecordsTheReplyUsageAndCost(t *testing.T) {
	binary, _ := stubBinary(t, claudeCaptured)
	reviewer := NewClaudeCode("", "")
	reviewer.Binary = binary
	reviewer.UserSettings = filepath.Join(t.TempDir(), "absent.json")
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Response != "NO FINDINGS" {
		t.Fatalf("run = %#v", raw)
	}
	cost := 0.015208300000000001
	wantUsage(t, raw.Usage, intp(2+3288+3223), intp(3223), intp(173), intp(0), &cost)
}

// TestTokenUsageOmitsWhatTheProviderDidNotReport: a field the reply left out is
// absent from the ledger JSON, as is an input total missing a cache term, and a
// reported zero is written.
func TestTokenUsageOmitsWhatTheProviderDidNotReport(t *testing.T) {
	for _, tc := range []struct{ reply, want string }{
		{`{"usage":{"input_tokens":120,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":9,"output_tokens_details":{"thinking_tokens":0}}}`,
			`{"input_tokens":120,"cached_input_tokens":0,"output_tokens":9,"reasoning_tokens":0}`},
		{`{"usage":{"input_tokens":120,"cache_read_input_tokens":30,"output_tokens":9}}`,
			`{"cached_input_tokens":30,"output_tokens":9}`},
	} {
		data, err := json.Marshal(decodeClaudeUsage([]byte(tc.reply)))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != tc.want {
			t.Errorf("usage of %s = %s, want %s", tc.reply, data, tc.want)
		}
	}
}

// A failed Claude run still records what its reply says it spent.
func TestClaudeCodeFailedRunKeepsItsUsage(t *testing.T) {
	reply := `{"is_error":true,"subtype":"error_max_turns","result":"stopped","usage":{"input_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":7}}`
	reviewer := NewClaudeCode("", "")
	reviewer.Binary = exitingBinary(t, reply, "", "1")
	reviewer.UserSettings = filepath.Join(t.TempDir(), "absent.json")
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Outcome != gate.OutcomeExit {
		t.Fatalf("run = %#v", raw)
	}
	wantUsage(t, raw.Usage, intp(5), intp(0), intp(7), nil, nil)
}

// agyCaptured is a real `agy --output-format json` reply.
const agyCaptured = `{"conversation_id":"29115cde-c83e-4b45-8e1b-cf0041dc1afe","status":"SUCCESS","response":"NO FINDINGS","duration_seconds":3.022021,"num_turns":1,"usage":{"input_tokens":13080,"output_tokens":168,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":13248}}`

func TestAgyRecordsTheReplyUsage(t *testing.T) {
	binary, _ := stubBinary(t, agyCaptured)
	reviewer := NewAgy("", "")
	reviewer.Binary = binary
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Response != "NO FINDINGS" {
		t.Fatalf("run = %#v", raw)
	}
	wantUsage(t, raw.Usage, intp(13080), intp(0), intp(168), intp(0), nil)
}

// agy's total_tokens settles whether its input counts the cache reads and its
// output the thinking; a reply without it leaves an unsettled total out.
func TestAgyUsageReadsTheSplitFromTheTotal(t *testing.T) {
	cases := []struct {
		name                         string
		usage                        agyUsage
		input, cached, output, think *int
	}{
		{"cache inside input, thinking outside output",
			agyUsage{InputTokens: intp(1000), CacheReadTokens: intp(400), OutputTokens: intp(50), ThinkingTokens: intp(20), TotalTokens: intp(1070)},
			intp(1000), intp(400), intp(70), intp(20)},
		{"cache outside input, thinking inside output",
			agyUsage{InputTokens: intp(600), CacheReadTokens: intp(400), OutputTokens: intp(70), ThinkingTokens: intp(20), TotalTokens: intp(1070)},
			intp(1000), intp(400), intp(70), intp(20)},
		{"no total",
			agyUsage{InputTokens: intp(600), CacheReadTokens: intp(400), OutputTokens: intp(70), ThinkingTokens: intp(0)},
			nil, intp(400), intp(70), intp(0)},
		{"no thinking count",
			agyUsage{InputTokens: intp(600), CacheReadTokens: intp(0), OutputTokens: intp(70), TotalTokens: intp(670)},
			intp(600), intp(0), nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantUsage(t, tc.usage.tokenUsage(), tc.input, tc.cached, tc.output, tc.think, nil)
		})
	}
}

// codexCaptured is a `codex exec --json` stream. Its turn and usage lines are from a
// real run; the command item and the first agent message stand for a review that
// reads files and writes more than one message.
const codexCaptured = `{"type":"thread.started","thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a53"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Reading the file."}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"sed -n 1,40p stats.py","status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"sed -n 1,40p stats.py","aggregated_output":"def mean(values):\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"NO FINDINGS"}}
{"type":"turn.completed","usage":{"input_tokens":13092,"cached_input_tokens":11392,"cache_write_input_tokens":0,"output_tokens":7,"reasoning_output_tokens":0}}`

// The reply is the last agent message, and the usage is turn.completed's as Codex
// reports it: its input already counts the cached part.
func TestCodexReadsTheJSONStream(t *testing.T) {
	binary, _ := stubBinary(t, codexCaptured)
	reviewer := NewCodex("", "")
	reviewer.Binary = binary
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Response != "NO FINDINGS" || raw.Status != "SUCCESS" || raw.Outcome != "" {
		t.Fatalf("run = %#v", raw)
	}
	wantUsage(t, raw.Usage, intp(13092), intp(11392), intp(7), intp(0), nil)
}

// A failed Codex run names the turn.failed message after the exit code, ahead of
// the stderr tail.
func TestCodexFailedRunNamesTheTurnFailure(t *testing.T) {
	stream := `{"type":"thread.started","thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a54"}
{"type":"turn.started"}
{"type":"error","message":"stream error: unexpected status 400 Bad Request"}
{"type":"turn.failed","error":{"message":"The 'gpt-nope' model is not supported when using Codex with a ChatGPT account."}}`
	reviewer := NewCodex("gpt-nope", "")
	reviewer.Binary = exitingBinary(t, stream, "codex: reading prompt from stdin", "1")
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	want := "rc=1 The 'gpt-nope' model is not supported when using Codex with a ChatGPT account. codex: reading prompt from stdin"
	if raw.Outcome != gate.OutcomeExit || raw.Detail != want {
		t.Fatalf("run = %#v\nwant detail %q", raw, want)
	}
	if raw.Usage != nil {
		t.Errorf("a turn that never completed reported usage: %+v", raw.Usage)
	}
}

// A turn that failed after an agent message is an ERROR even on a zero exit, so the
// message is salvaged rather than counted as a completed review.
func TestCodexFailedTurnIsAnErrorOnAZeroExit(t *testing.T) {
	stream := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"NO FINDINGS"}}
{"type":"turn.failed","error":{"message":"stream disconnected before completion"}}`
	binary, _ := stubBinary(t, stream)
	reviewer := NewCodex("", "")
	reviewer.Binary = binary
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Status != "ERROR" || raw.Error != "stream disconnected before completion" {
		t.Fatalf("run = %#v", raw)
	}
	if run := gate.Classify(raw); run.Outcome != gate.OutcomeSalvagedClean {
		t.Errorf("outcome = %s, want salvaged_clean", run.Outcome)
	}
}

// A stream cut before turn.completed is an ERROR on a zero exit, so its message is
// salvaged rather than counted as a completed review.
func TestCodexStreamWithoutACompletedTurnIsAnError(t *testing.T) {
	stream := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"NO FINDINGS"}}
{"type":"error","message":"stream error: reconnecting"}`
	binary, _ := stubBinary(t, stream)
	reviewer := NewCodex("", "")
	reviewer.Binary = binary
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Status != "ERROR" || raw.Error != "stream error: reconnecting" {
		t.Fatalf("run = %#v", raw)
	}
}

// Output that holds no event is unparseable, not a review.
func TestCodexPlainTextIsUnparseable(t *testing.T) {
	binary, _ := stubBinary(t, "NO FINDINGS")
	reviewer := NewCodex("", "")
	reviewer.Binary = binary
	raw := reviewer.Review(context.Background(), "PROMPT", t.TempDir())
	if raw.Outcome != gate.OutcomeUnparseable || !strings.Contains(raw.Detail, "NO FINDINGS") {
		t.Fatalf("run = %#v", raw)
	}
}

// An API run's usage sums its requests, and a field enters the sum only when every
// reply reported it.
func TestAPIUsageSumsOnlyWhatEveryReplyReported(t *testing.T) {
	var a apiUsage
	a.add(&chatUsage{PromptTokens: intp(100), CompletionTokens: intp(10),
		PromptTokensDetails: &struct {
			CachedTokens *int `json:"cached_tokens"`
		}{intp(60)},
		CompletionTokensDetails: &struct {
			ReasoningTokens *int `json:"reasoning_tokens"`
		}{intp(4)}})
	a.add(&chatUsage{PromptTokens: intp(150), CompletionTokens: intp(20),
		PromptTokensDetails: &struct {
			CachedTokens *int `json:"cached_tokens"`
		}{intp(90)}})
	wantUsage(t, a.tokenUsage(), intp(250), intp(150), intp(30), nil, nil)
	a.add(nil)
	if got := a.tokenUsage(); got != nil {
		t.Errorf("a run with a reply that carried no usage reported %+v", got)
	}
	var none apiUsage
	if got := none.tokenUsage(); got != nil {
		t.Errorf("a run with no reply reported %+v", got)
	}
}
