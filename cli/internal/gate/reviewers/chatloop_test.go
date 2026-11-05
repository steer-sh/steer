package reviewers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// scripted answers the nth request with the nth reply, repeating the last, and
// records every request body.
type scripted struct {
	mu       sync.Mutex
	replies  []scriptedReply
	requests []map[string]any
}

type scriptedReply struct {
	status int
	body   string
}

func scriptedServer(t *testing.T, replies ...scriptedReply) (*httptest.Server, *scripted) {
	t.Helper()
	s := &scripted{replies: replies}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, body)
		rep := s.replies[min(n, len(s.replies)-1)]
		s.mu.Unlock()
		w.WriteHeader(rep.status)
		io.WriteString(w, rep.body)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func ok(body string) scriptedReply { return scriptedReply{200, body} }

// callsReply is a turn that calls read_file once per path.
func callsReply(paths ...string) string {
	var calls []any
	for i, p := range paths {
		args, _ := json.Marshal(map[string]string{"path": p})
		calls = append(calls, map[string]any{"id": fmt.Sprintf("call_%d", i), "type": "function",
			"function": map[string]any{"name": "read_file", "arguments": string(args)}})
	}
	b, _ := json.Marshal(map[string]any{
		"model": "vendor/model-x-2026",
		"choices": []any{map[string]any{"finish_reason": "tool_calls",
			"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}}},
		"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10},
	})
	return string(b)
}

func loopReviewer(baseURL, mode string) *ChatAPI {
	r := chatReviewer(baseURL)
	r.Tools = mode
	return r
}

func loopView(t *testing.T) *gate.CommitView {
	t.Helper()
	root := toolRepo(t, map[string]string{"a.go": "package a // staged\n"})
	writeRepoFile(t, root, "a.go", "package a // unstaged\n")
	view, ok := gate.NewCommitView(gate.Defaults(), root, gate.DiffSpec{StagedOnly: true})
	if !ok {
		t.Fatal("NewCommitView failed")
	}
	return view
}

func messagesOf(req map[string]any) []map[string]any {
	var out []map[string]any
	for _, m := range req["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

// TestToolLoopReadsTheViewAndAnswers: a turn that calls read_file gets the file as the
// commit lands it, answered by call id, and the next turn's reply is the review.
func TestToolLoopReadsTheViewAndAnswers(t *testing.T) {
	srv, s := scriptedServer(t, ok(callsReply("a.go")), ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "the prompt", "", loopView(t))
	if raw.Response != "NO FINDINGS" || raw.Status != "SUCCESS" {
		t.Fatalf("run = %+v", raw)
	}
	if len(s.requests) != 2 {
		t.Fatalf("%d requests, want 2", len(s.requests))
	}
	first := s.requests[0]
	if first["tools"] == nil || messagesOf(first)[0]["role"] != "system" {
		t.Errorf("the first request offers no tools or no system message: %v", first)
	}
	msgs := messagesOf(s.requests[1])
	tool := msgs[len(msgs)-1]
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_0" ||
		!strings.Contains(tool["content"].(string), "staged") || strings.Contains(tool["content"].(string), "unstaged") {
		t.Errorf("tool reply = %v", tool)
	}
	if assistant := msgs[len(msgs)-2]; assistant["role"] != "assistant" || assistant["tool_calls"] == nil {
		t.Errorf("the assistant turn is not echoed: %v", assistant)
	}
	use := raw.Tools
	if use == nil || use.Mode != "tools" || use.Calls != 1 || use.Turns != 1 || len(use.Files) != 1 || use.Bytes == 0 {
		t.Errorf("Tools = %+v", use)
	}
	if raw.Usage == nil || raw.Usage.Prompt != 100 || raw.Usage.Completion != 10 {
		t.Errorf("Usage = %+v", raw.Usage)
	}
}

// TestToolLoopStopsAtTheTurnCap: a model that only calls tools gets maxToolTurns
// turns with tools, then one request without them, and the run is tool_budget.
func TestToolLoopStopsAtTheTurnCap(t *testing.T) {
	srv, s := scriptedServer(t, ok(callsReply("a.go")))
	raw := loopReviewer(srv.URL, "required").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Outcome != gate.OutcomeToolBudget || raw.Response != "" {
		t.Fatalf("run = %+v", raw)
	}
	if len(s.requests) != maxToolTurns+1 {
		t.Fatalf("%d requests, want %d", len(s.requests), maxToolTurns+1)
	}
	last := s.requests[maxToolTurns]
	msgs := messagesOf(last)
	if last["tools"] != nil || msgs[len(msgs)-1]["content"] != budgetSpentPrompt {
		t.Errorf("the last request offers tools or does not ask for the review")
	}
}

// TestToolLoopAnswersEveryCallPastTheCallCap: calls past maxToolCalls are refused,
// every call still gets a reply, and the next turn offers no tools.
func TestToolLoopAnswersEveryCallPastTheCallCap(t *testing.T) {
	repeat := func(n int) []string {
		paths := make([]string, n)
		for i := range paths {
			paths[i] = "a.go"
		}
		return paths
	}
	srv, s := scriptedServer(t, ok(callsReply(repeat(20)...)), ok(callsReply(repeat(10)...)),
		ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Response != "NO FINDINGS" || raw.Tools.Calls != maxToolCalls || raw.Tools.Refused != 6 {
		t.Fatalf("run = %+v, tools %+v", raw, raw.Tools)
	}
	replies := 0
	for _, m := range messagesOf(s.requests[2]) {
		if m["role"] == "tool" {
			replies++
		}
	}
	if replies != 30 {
		t.Errorf("%d tool replies, want 30", replies)
	}
	if s.requests[2]["tools"] != nil {
		t.Error("the turn after the call cap still offers tools")
	}
}

// TestToolLoopEndsATurnOverTheCallCap: one turn with more calls than the cap ends the
// run before any call runs, rather than answer each with a refusal.
func TestToolLoopEndsATurnOverTheCallCap(t *testing.T) {
	paths := make([]string, maxToolCalls+1)
	for i := range paths {
		paths[i] = "a.go"
	}
	srv, s := scriptedServer(t, ok(callsReply(paths...)), ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Outcome != gate.OutcomeToolBudget || raw.Tools.Calls != 0 || len(s.requests) != 1 {
		t.Errorf("run = %+v, tools %+v, %d requests", raw, raw.Tools, len(s.requests))
	}
}

// TestToolLoopDoesNotFallBackOnATimeoutNamingTools: a 408 is a failure even when its
// message names tools.
func TestToolLoopDoesNotFallBackOnATimeoutNamingTools(t *testing.T) {
	srv, s := scriptedServer(t, scriptedReply{408, `{"error":{"message":"tool request timed out"}}`},
		ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Outcome != gate.OutcomeExit || len(s.requests) != 1 {
		t.Errorf("run = %+v after %d requests", raw, len(s.requests))
	}
}

// TestToolLoopAutoFallsBackOnceWhenToolsAreRefused: a first-request 4xx naming tools
// repeats the request once without tools; required fails instead.
func TestToolLoopAutoFallsBackOnceWhenToolsAreRefused(t *testing.T) {
	refusal := scriptedReply{404, `{"error":{"message":"No endpoints found that support tool use"}}`}
	srv, s := scriptedServer(t, refusal, ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Response != "NO FINDINGS" || raw.Tools == nil || raw.Tools.Mode != "none" ||
		!strings.Contains(raw.Tools.Fallback, "support tool use") {
		t.Fatalf("auto: run = %+v, tools %+v", raw, raw.Tools)
	}
	if len(s.requests) != 2 || s.requests[1]["tools"] != nil {
		t.Errorf("auto: %d requests; the retry must send no tools", len(s.requests))
	}

	srv, s = scriptedServer(t, refusal, ok(reply("stop", "NO FINDINGS")))
	raw = loopReviewer(srv.URL, "required").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Outcome != gate.OutcomeExit || len(s.requests) != 1 {
		t.Errorf("required: run = %+v after %d requests", raw, len(s.requests))
	}
}

// TestToolLoopDoesNotFallBackOnARateLimit: a 429 is a failure, not a refusal of tools.
func TestToolLoopDoesNotFallBackOnARateLimit(t *testing.T) {
	srv, s := scriptedServer(t, scriptedReply{429, `{"error":{"message":"tool quota"}}`}, ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Outcome != gate.OutcomeRateLimited || len(s.requests) != 1 {
		t.Errorf("run = %+v after %d requests", raw, len(s.requests))
	}
}

// TestToolLoopWithoutAView: required fails before any request; auto sends the prompt
// alone and records why.
func TestToolLoopWithoutAView(t *testing.T) {
	srv, s := scriptedServer(t, ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "required").ReviewView(context.Background(), "p", "", nil)
	if raw.Outcome != gate.OutcomeExit || len(s.requests) != 0 {
		t.Errorf("required: run = %+v after %d requests", raw, len(s.requests))
	}
	raw = loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", nil)
	if raw.Response != "NO FINDINGS" || raw.Tools == nil || raw.Tools.Mode != "none" || s.requests[0]["tools"] != nil {
		t.Errorf("auto: run = %+v, tools %+v", raw, raw.Tools)
	}
}

// TestToolsOffSendsThePromptAlone: with tools off the request carries no tools and no
// system message, and the reviewer asks for no view.
func TestToolsOffSendsThePromptAlone(t *testing.T) {
	srv, s := scriptedServer(t, ok(reply("stop", "NO FINDINGS")))
	r := loopReviewer(srv.URL, "off")
	if r.WantsView() {
		t.Error("tools off asks for a view")
	}
	r.ReviewView(context.Background(), "p", "", loopView(t))
	msgs := messagesOf(s.requests[0])
	if s.requests[0]["tools"] != nil || len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Errorf("request = %v", s.requests[0])
	}
}

// TestToolLoopRunsNoCallOfACutTurn: a turn cut at the output limit with a partial
// call is truncated, and its call never runs.
func TestToolLoopRunsNoCallOfACutTurn(t *testing.T) {
	cut := strings.Replace(callsReply("a.go"), `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1)
	srv, s := scriptedServer(t, ok(cut), ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Outcome != gate.OutcomeTruncated || raw.Tools.Calls != 0 || len(s.requests) != 1 {
		t.Errorf("run = %+v, tools %+v, %d requests", raw, raw.Tools, len(s.requests))
	}
}

// TestToolLoopEchoesObjectArgumentsAsAString: a server that sends arguments as an
// object gets them back as the JSON string the protocol has; a failed read is not
// listed as a file read.
func TestToolLoopEchoesObjectArgumentsAsAString(t *testing.T) {
	body := `{"model":"m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,` +
		`"tool_calls":[{"id":"c1","type":"function","function":{"name":"read_file","arguments":{"path":"a.go"}}},` +
		`{"id":"c2","type":"function","function":{"name":"read_file","arguments":{"path":"missing.go"}}}]}}]}`
	srv, s := scriptedServer(t, ok(body), ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Response != "NO FINDINGS" || len(raw.Tools.Files) != 1 || raw.Tools.Files[0] != "a.go" {
		t.Fatalf("run = %+v, tools %+v", raw, raw.Tools)
	}
	msgs := messagesOf(s.requests[1])
	calls := msgs[2]["tool_calls"].([]any)
	args := calls[0].(map[string]any)["function"].(map[string]any)["arguments"]
	if args != `{"path":"a.go"}` {
		t.Errorf("echoed arguments = %#v, want the JSON string", args)
	}
}

// TestToolLoopAsksForTheReviewOnceTheBytesAreSpent: a run that fills the output budget
// before the call and turn caps gets the request without tools next.
func TestToolLoopAsksForTheReviewOnceTheBytesAreSpent(t *testing.T) {
	root := toolRepo(t, map[string]string{"big.txt": strings.Repeat("0123456789abcdef\n", 4000)})
	view, okView := gate.NewCommitView(gate.Defaults(), root, gate.DiffSpec{StagedOnly: true})
	if !okView {
		t.Fatal("NewCommitView failed")
	}
	paths := make([]string, maxRunToolBytes/maxCallBytes+1)
	for i := range paths {
		paths[i] = "big.txt"
	}
	srv, s := scriptedServer(t, ok(callsReply(paths...)), ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "auto").ReviewView(context.Background(), "p", "", view)
	if raw.Response != "NO FINDINGS" || len(s.requests) != 2 || s.requests[1]["tools"] != nil {
		t.Errorf("run = %+v after %d requests; the second must offer no tools", raw, len(s.requests))
	}
}

func TestChatAPILoopSendsReasoningBackOnTheAssistantTurn(t *testing.T) {
	first := `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,` +
		`"reasoning":"look at a.go","reasoning_details":[{"type":"reasoning.text","text":"look at a.go"}],` +
		`"tool_calls":[{"id":"c1","type":"function","function":{"name":"list_files","arguments":"{}"}}]}}]}`
	srv, s := scriptedServer(t, ok(first), ok(reply("stop", "NO FINDINGS")))
	raw := loopReviewer(srv.URL, "required").ReviewView(context.Background(), "p", "", loopView(t))
	if raw.Response != "NO FINDINGS" {
		t.Fatalf("run = %+v", raw)
	}
	assistant := s.requests[1]["messages"].([]any)[2].(map[string]any)
	if assistant["reasoning"] != "look at a.go" {
		t.Errorf("reasoning = %v", assistant["reasoning"])
	}
	details, _ := assistant["reasoning_details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["type"] != "reasoning.text" {
		t.Errorf("reasoning_details = %v", assistant["reasoning_details"])
	}
}

func TestChatAPILoopSendsNoReasoningFieldsTheEndpointDidNotFill(t *testing.T) {
	first := `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,` +
		`"reasoning":null,` +
		`"tool_calls":[{"id":"c1","type":"function","function":{"name":"list_files","arguments":"{}"}}]}}]}`
	srv, s := scriptedServer(t, ok(first), ok(reply("stop", "NO FINDINGS")))
	loopReviewer(srv.URL, "required").ReviewView(context.Background(), "p", "", loopView(t))
	assistant := s.requests[1]["messages"].([]any)[2].(map[string]any)
	for _, field := range []string{"reasoning", "reasoning_details"} {
		if _, sent := assistant[field]; sent {
			t.Errorf("%s was sent: %v", field, assistant[field])
		}
	}
}

func TestChatAPISystemPromptOpensTheFilesTheDiffBudgetHid(t *testing.T) {
	srv, s := scriptedServer(t, ok(reply("stop", "NO FINDINGS")))
	loopReviewer(srv.URL, "required").ReviewView(context.Background(), "p", "", loopView(t))
	system := s.requests[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "hunks are not visible") || !strings.Contains(system, "a defect in this commit's change") {
		t.Errorf("system prompt = %q", system)
	}
}
