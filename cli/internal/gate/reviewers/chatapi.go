package reviewers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/steer-sh/steer-cli/internal/apikey"
	"github.com/steer-sh/steer-cli/internal/gate"
)

// ChatAPI reviews through an OpenAI-compatible chat-completions endpoint: one POST
// with tools off, or a tool loop over the commit view with them on.
//
// The Panel calls one ChatAPI from several goroutines, so Review changes no field.
type ChatAPI struct {
	Model   string
	Effort  string
	BaseURL string
	KeyEnv  string
	// Tools is the tool mode, one of ToolModes; "" is off.
	Tools string
	// Client defaults to one that refuses a redirect to another origin.
	// Overridable so a test can point at a local server.
	Client *http.Client
	// FindKey defaults to apikey.Find. Overridable so a test reads no operator file.
	FindKey func(keyEnv string) (string, apikey.Status)
}

func NewChatAPI(o Options) *ChatAPI {
	return &ChatAPI{Model: o.Model, Effort: o.Effort, BaseURL: o.BaseURL, KeyEnv: o.KeyEnv, Tools: o.Tools}
}

func (c *ChatAPI) Name() string { return APIProviderID }

// ModelLabel is the configured model. The model the endpoint reports goes to the
// run's ResponseModel, because an endpoint can route one name to another model.
func (c *ChatAPI) ModelLabel() string { return c.Model }

// Endpoint is the base URL's host, which the instrument records. The path and
// query stay out of the ledger.
func (c *ChatAPI) Endpoint() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// maxResponseBytes bounds the body read from the endpoint. A review reply is a few
// kilobytes; the bound keeps a misbehaving server from filling memory.
const maxResponseBytes = 8 << 20

// maxErrorChars bounds the error message a run's detail keeps from a reply.
const maxErrorChars = 200

// validateChatAPI refuses a configuration the reviewer cannot send with. It reads no
// key: a missing key is a run outcome, so the ledger records it.
func validateChatAPI(o Options) error {
	if o.BaseURL == "" {
		return errors.New("the openai-compatible reviewer needs a base URL: steer config set reviewer.api.base_url <url>")
	}
	if err := CheckBaseURL(o.BaseURL); err != nil {
		return err
	}
	if o.Model == "" {
		return errors.New("the openai-compatible reviewer needs a model: steer config set reviewer.model <model>")
	}
	if o.Tools != "" && !slices.Contains(ToolModes, o.Tools) {
		return fmt.Errorf("reviewer.api.tools %q is not one of %s", o.Tools, strings.Join(ToolModes, ", "))
	}
	return nil
}

// chatCompletionsPath is the endpoint path the reviewer appends to the base URL.
const chatCompletionsPath = "/chat/completions"

// CheckBaseURL requires https, except on the loopback host a local model server
// uses, and refuses user info, which would put a secret in the config file, and
// a path that already ends in the endpoint path.
func CheckBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("reviewer.api.base_url %q is not a URL: %w", raw, err)
	}
	if u.User != nil {
		return errors.New("reviewer.api.base_url carries user info; put the key in the key file instead: " + apikey.SetCommand)
	}
	if u.Host == "" {
		return fmt.Errorf("reviewer.api.base_url %q names no host", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return fmt.Errorf("reviewer.api.base_url %q is plain http; use https, or a localhost server", raw)
		}
	default:
		return fmt.Errorf("reviewer.api.base_url %q must use https", raw)
	}
	// A bare "?" or "#" parses to an empty query or fragment, so the raw text decides.
	if strings.ContainsAny(raw, "?#") {
		return fmt.Errorf("reviewer.api.base_url %q carries a query or fragment; give the base path only", raw)
	}
	// The reviewer appends the endpoint path itself; providers' docs often show the full URL.
	// raw carries no query or fragment here, so it ends in the path, and trimming
	// it keeps the path's escapes.
	if strings.HasSuffix(strings.TrimRight(u.EscapedPath(), "/"), chatCompletionsPath) {
		return fmt.Errorf("reviewer.api.base_url %q ends in %s, which the reviewer adds itself; use %s",
			raw, chatCompletionsPath, strings.TrimSuffix(strings.TrimRight(raw, "/"), chatCompletionsPath))
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *ChatAPI) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{CheckRedirect: sameOriginRedirect}
}

// sameOriginRedirect refuses a redirect to another scheme, host or port, so the
// key never follows the request to a server the operator did not name.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return fmt.Errorf("refused a redirect to another origin (%s)", req.URL.Host)
	}
	return nil
}

type chatRequest struct {
	Model           string           `json:"model"`
	Messages        []chatMessage    `json:"messages"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	Tools           []map[string]any `json:"tools,omitempty"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content is null for an assistant turn that only calls tools.
	Content    *string    `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Reasoning and ReasoningDetails go back on an assistant turn as the endpoint sent them.
	Reasoning        json.RawMessage `json:"reasoning,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

func textMessage(role, text string) chatMessage {
	return chatMessage{Role: role, Content: &text}
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON string holding an object; it is echoed back as it came.
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// argumentText is the call's arguments as JSON object text. Some servers send the
// object itself where the protocol has a string holding it.
func (t toolCall) argumentText() string {
	var text string
	if json.Unmarshal(t.Function.Arguments, &text) == nil {
		return text
	}
	return string(t.Function.Arguments)
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          json.RawMessage `json:"content"`
			ToolCalls        []toolCall      `json:"tool_calls"`
			Reasoning        json.RawMessage `json:"reasoning"`
			ReasoningDetails json.RawMessage `json:"reasoning_details"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens            int `json:"prompt_tokens"`
		CompletionTokens        int `json:"completion_tokens"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

// The loop's caps. The tool output caps are in viewtools.go; all of them hold inside
// the review timeout.
const (
	// maxToolCalls bounds the calls one run executes, each call of a multi-call turn
	// counted.
	maxToolCalls = 24
	// maxToolTurns bounds the requests that offer tools; one more asks for the review.
	maxToolTurns = 8
)

// toolsSystemPrompt tells the model what the tools read. The review prompt itself
// is unchanged, so the prompt digest still names it.
const toolsSystemPrompt = "You can call read_file, grep and list_files to read the repository " +
	"as this commit lands it. Read only what the review needs, then answer in the format " +
	"the user message asks for. A note in the diff that a file's hunks are not visible, or that " +
	"forbids a finding about that file, describes the diff text only: you can read that file " +
	"with the tools. Its contents include code this commit did not change, so raise a finding " +
	"on it only when what you read shows a defect in this commit's change."

// budgetSpentPrompt asks for the review once the tool caps are reached.
const budgetSpentPrompt = "The tool budget of this review is spent. Give the review now, " +
	"in the format the first message asks for."

// WantsView reports whether the tool mode offers tools, so the panel builds a view.
func (c *ChatAPI) WantsView() bool { return c.Tools == "auto" || c.Tools == "required" }

func (c *ChatAPI) Review(ctx context.Context, prompt, cwd string) gate.RawRun {
	return c.ReviewView(ctx, prompt, cwd, nil)
}

// ReviewView runs one review. With tools off it is one request; otherwise it is the
// tool loop over view.
func (c *ChatAPI) ReviewView(ctx context.Context, prompt, cwd string, view *gate.CommitView) gate.RawRun {
	find := c.FindKey
	if find == nil {
		find = apikey.Find
	}
	key, status := find(c.KeyEnv)
	if key == "" {
		return gate.RawRun{Outcome: gate.OutcomeNoKey, Detail: status.Missing()}
	}
	usage := &gate.TokenUsage{}
	var run gate.RawRun
	if c.WantsView() {
		run = c.converse(ctx, prompt, key, view, usage)
	} else {
		run = c.once(ctx, prompt, key, usage)
	}
	if *usage != (gate.TokenUsage{}) {
		run.Usage = usage
	}
	// Every field the endpoint wrote: Classify copies an unusable reply into the
	// run's detail, and the ledger records the model it names.
	run.Response = redact(run.Response, key)
	run.ResponseModel = redact(run.ResponseModel, key)
	run.Detail = redact(run.Detail, key)
	run.Error = redact(run.Error, key)
	return run
}

// once sends the prompt alone and maps the reply.
func (c *ChatAPI) once(ctx context.Context, prompt, key string, usage *gate.TokenUsage) gate.RawRun {
	parsed, fail := c.post(ctx, key, chatRequest{
		Model: c.Model, ReasoningEffort: c.Effort,
		Messages: []chatMessage{textMessage("user", prompt)},
	}, usage)
	if fail != nil {
		return *fail
	}
	return finalRun(parsed)
}

// NoViewFallback is the fallback reason of a run that had no commit view, so it
// offered the endpoint no tools.
const NoViewFallback = "git could not build the commit view"

// converse is the tool loop. Each turn that calls tools gets every call answered; a
// turn without calls ends the run through finalRun. After the caps, one request
// without tools asks for the review, and a reply that still calls tools is
// tool_budget. Tool calls and text between turns never reach Classify.
func (c *ChatAPI) converse(ctx context.Context, prompt, key string, view *gate.CommitView, usage *gate.TokenUsage) gate.RawRun {
	if view == nil {
		if c.Tools == "required" {
			return gate.RawRun{Outcome: gate.OutcomeExit,
				Detail: "reviewer.api.tools is required, and git could not build the commit view",
				Tools:  &gate.ToolUse{Mode: "none", Fallback: NoViewFallback}}
		}
		run := c.once(ctx, prompt, key, usage)
		run.Tools = &gate.ToolUse{Mode: "none", Fallback: NoViewFallback}
		return run
	}
	use := &gate.ToolUse{Mode: "tools"}
	tools := &viewTools{view: view}
	messages := []chatMessage{textMessage("system", toolsSystemPrompt), textMessage("user", prompt)}
	for turn := 1; ; turn++ {
		offer := turn <= maxToolTurns && use.Calls < maxToolCalls && !tools.spent()
		req := chatRequest{Model: c.Model, ReasoningEffort: c.Effort, Messages: messages}
		if offer {
			req.Tools = toolSpecs
		} else {
			req.Messages = append(messages, textMessage("user", budgetSpentPrompt))
		}
		parsed, fail := c.post(ctx, key, req, usage)
		if fail != nil {
			if turn == 1 && c.Tools == "auto" && refusesTools(*fail) {
				// One retry without tools, inside the same deadline.
				run := c.once(ctx, prompt, key, usage)
				run.Tools = &gate.ToolUse{Mode: "none", Fallback: fail.Detail}
				return run
			}
			fail.Tools = use
			return *fail
		}
		choice := parsed.Choices[0]
		calls := choice.Message.ToolCalls
		// A cut or filtered turn may hold a partial call; it ends the run as finalRun
		// maps its finish reason, so no part of it runs.
		switch choice.FinishReason {
		case "tool_calls", "stop", "":
		default:
			run := finalRun(parsed)
			run.Tools = use
			return run
		}
		if len(calls) == 0 {
			run := finalRun(parsed)
			run.Tools = use
			return run
		}
		if !offer {
			return gate.RawRun{Outcome: gate.OutcomeToolBudget, ResponseModel: parsed.Model, Tools: use,
				Detail: fmt.Sprintf("the model still called tools after %d calls in %d turns", use.Calls, use.Turns)}
		}
		// Every call of a turn needs a reply, so a turn over the call cap would send a
		// refusal per call; it ends the run instead.
		if len(calls) > maxToolCalls {
			return gate.RawRun{Outcome: gate.OutcomeToolBudget, ResponseModel: parsed.Model, Tools: use,
				Detail: fmt.Sprintf("the model asked for %d tool calls in one turn", len(calls))}
		}
		use.Turns++
		assistant := chatMessage{Role: "assistant", ToolCalls: echoed(calls),
			Reasoning: sentValue(choice.Message.Reasoning), ReasoningDetails: sentValue(choice.Message.ReasoningDetails)}
		if text, ok := messageText(choice.Message.Content); ok && text != "" {
			assistant.Content = &text
		}
		messages = append(messages, assistant)
		for _, call := range calls {
			var result string
			if use.Calls >= maxToolCalls {
				result = "refused: the tool call budget of this review is spent; give the review now"
				use.Refused++
			} else {
				use.Calls++
				args := call.argumentText()
				var refused bool
				result, refused = tools.call(ctx, call.Function.Name, args)
				if refused {
					use.Refused++
				} else if call.Function.Name == "read_file" && !strings.HasPrefix(result, toolErrorPrefix) &&
					len(use.Files) < gate.MaxToolFiles {
					var a struct {
						Path string `json:"path"`
					}
					if json.Unmarshal([]byte(args), &a) == nil && a.Path != "" {
						use.Files = append(use.Files, a.Path)
					}
				}
			}
			reply := textMessage("tool", result)
			reply.ToolCallID = call.ID
			messages = append(messages, reply)
		}
		use.Bytes = tools.bytes
	}
}

// echoed is calls as the next request sends them back: arguments as the JSON string
// the protocol has, whichever form the server sent.
func echoed(calls []toolCall) []toolCall {
	out := make([]toolCall, len(calls))
	for i, call := range calls {
		out[i] = call
		text, _ := json.Marshal(call.argumentText())
		out[i].Function.Arguments = text
	}
	return out
}

// sentValue is raw, or nil for a JSON null, which then stays out of the next request.
func sentValue(raw json.RawMessage) json.RawMessage {
	if string(raw) == "null" {
		return nil
	}
	return raw
}

// refusesTools reads a first-request failure as the endpoint refusing the tools
// field: a 400, 404 or 422 whose message names tools. Auth, rate limits and
// timeouts are not.
func refusesTools(run gate.RawRun) bool {
	if run.Outcome != gate.OutcomeExit {
		return false
	}
	code, _, _ := strings.Cut(run.Detail, " ")
	if code != "HTTP" {
		return false
	}
	status := strings.Fields(run.Detail)
	return len(status) > 1 && slices.Contains([]string{"400", "404", "422"}, status[1]) &&
		strings.Contains(strings.ToLower(run.Detail), "tool")
}

// post sends one request. It returns the parsed reply, which holds at least one
// choice, or the run that ends the review. The reply's token usage adds to usage.
func (c *ChatAPI) post(ctx context.Context, key string, request chatRequest, usage *gate.TokenUsage) (*chatResponse, *gate.RawRun) {
	fail := func(outcome gate.RunOutcome, detail string) (*chatResponse, *gate.RawRun) {
		return nil, &gate.RawRun{Outcome: outcome, Detail: detail}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fail(gate.OutcomeExit, "encode the request: "+err.Error())
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + chatCompletionsPath
	// replied tells an endpoint that sent any HTTP reply, a redirect included,
	// from one that sent none.
	var replied atomic.Bool
	traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotFirstResponseByte: func() { replied.Store(true) },
	})
	req, err := http.NewRequestWithContext(traced, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fail(gate.OutcomeExit, "build the request: "+err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fail(gate.OutcomeTimeout, "no response before the deadline")
		}
		if replied.Load() {
			return fail(gate.OutcomeExit, "request failed: "+err.Error())
		}
		return fail(gate.OutcomeUnreachable, "request failed: "+err.Error())
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return fail(gate.OutcomeTimeout, "no response before the deadline")
		}
		return fail(gate.OutcomeExit, "read the response: "+err.Error())
	}
	if len(data) > maxResponseBytes {
		return fail(gate.OutcomeUnparseable, "the response exceeds "+strconv.Itoa(maxResponseBytes)+" bytes")
	}

	code := "HTTP " + strconv.Itoa(resp.StatusCode)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// The status code alone: an auth error body can echo part of the key.
		return fail(gate.OutcomeAuth, code)
	case resp.StatusCode == http.StatusTooManyRequests:
		// The message tells a quota on the key from a limit upstream of a router.
		return fail(gate.OutcomeRateLimited, withMessage(code, data, key))
	case resp.StatusCode != http.StatusOK:
		return fail(gate.OutcomeExit, withMessage(code, data, key))
	}

	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fail(gate.OutcomeUnparseable, "the response is not JSON: "+err.Error())
	}
	if u := parsed.Usage; u != nil {
		usage.Prompt += u.PromptTokens
		usage.Completion += u.CompletionTokens
		usage.Reasoning += u.CompletionTokensDetails.ReasoningTokens
	}
	if len(parsed.Choices) == 0 {
		detail := "the response holds no choices"
		if msg := errorMessage(data, key); msg != "" {
			detail += ": " + msg
		}
		return nil, &gate.RawRun{Outcome: gate.OutcomeUnparseable, Detail: detail, ResponseModel: parsed.Model}
	}
	return &parsed, nil
}

// finalRun maps the reply that ends a review. Only a complete reply reaches
// Classify. A cut or filtered reply carries no Response, so salvage can never count
// a half review as a review.
func finalRun(parsed *chatResponse) gate.RawRun {
	choice := parsed.Choices[0]
	switch choice.FinishReason {
	case "stop", "":
	case "length":
		return gate.RawRun{Outcome: gate.OutcomeTruncated,
			Detail: "the reply reached the output limit", ResponseModel: parsed.Model}
	default:
		return gate.RawRun{Outcome: gate.OutcomeExit,
			Detail: "finish_reason=" + strconv.Quote(choice.FinishReason), ResponseModel: parsed.Model}
	}
	text, ok := messageText(choice.Message.Content)
	if !ok {
		return gate.RawRun{Outcome: gate.OutcomeUnparseable,
			Detail: "the reply content is neither text nor text parts", ResponseModel: parsed.Model}
	}
	text = strings.TrimSpace(text)
	// Some local servers send no finish_reason; only a reply with text then counts
	// as complete.
	if choice.FinishReason == "" && text == "" {
		return gate.RawRun{Outcome: gate.OutcomeExit,
			Detail: `finish_reason="" with no reply text`, ResponseModel: parsed.Model}
	}
	return gate.RawRun{Response: text, Status: "SUCCESS", ResponseModel: parsed.Model}
}

// messageText reads a message's content: a string, or the array of typed parts some
// servers return, whose text parts it joins. A null content is an empty reply.
func messageText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", false
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String(), true
}

// withMessage appends the endpoint's error message, if any, to code.
func withMessage(code string, data []byte, key string) string {
	if msg := errorMessage(data, key); msg != "" {
		return code + " " + msg
	}
	return code
}

// errorMessage is the `error.message` field of a reply, with the key removed before
// the cut, so that a cut cannot leave part of it; "" when the reply has none. The
// rest of the body stays out of the ledger.
func errorMessage(data []byte, key string) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) != nil {
		return ""
	}
	msg := redactMasked(redact(strings.Join(strings.Fields(body.Error.Message), " "), key), key)
	if len(msg) > maxErrorChars {
		msg = strings.ToValidUTF8(msg[:maxErrorChars], "") + "…"
	}
	return msg
}

// redactMasked replaces each word of a single-spaced message that holds the start of
// the key, or a mask character and the end of the key. An end alone can be a plain word.
func redactMasked(text, key string) string {
	if len(key) < 12 {
		return text
	}
	words := strings.Split(text, " ")
	for i, word := range words {
		masked := strings.ContainsAny(word, "*…") || strings.Contains(word, "...")
		if strings.Contains(word, key[:6]) || (masked && strings.Contains(word, key[len(key)-4:])) {
			words[i] = "[redacted]"
		}
	}
	return strings.Join(words, " ")
}

// redact removes the key from a string that leaves the adapter.
func redact(text, key string) string {
	if key == "" || text == "" {
		return text
	}
	return strings.ReplaceAll(text, key, "[redacted]")
}
