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
	"net/url"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/apikey"
	"github.com/steer-sh/steer-cli/internal/gate"
)

// ChatAPI reviews through one POST to an OpenAI-compatible chat-completions
// endpoint. It reads no file and runs no tool: the prompt is the whole review input.
//
// The Panel calls one ChatAPI from several goroutines, so Review changes no field.
type ChatAPI struct {
	Model   string
	Effort  string
	BaseURL string
	KeyEnv  string
	// Client defaults to one that refuses a redirect to another origin.
	// Overridable so a test can point at a local server.
	Client *http.Client
	// FindKey defaults to apikey.Find. Overridable so a test reads no operator file.
	FindKey func(keyEnv string) (string, apikey.Status)
}

func NewChatAPI(o Options) *ChatAPI {
	return &ChatAPI{Model: o.Model, Effort: o.Effort, BaseURL: o.BaseURL, KeyEnv: o.KeyEnv}
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
	return nil
}

// CheckBaseURL requires https, except on the loopback host a local model server
// uses, and refuses user info, which would put a secret in the config file.
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
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("reviewer.api.base_url %q carries a query or fragment; give the base path only", raw)
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
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *ChatAPI) Review(ctx context.Context, prompt, cwd string) gate.RawRun {
	find := c.FindKey
	if find == nil {
		find = apikey.Find
	}
	key, status := find(c.KeyEnv)
	if key == "" {
		return gate.RawRun{Outcome: gate.OutcomeNoKey, Detail: status.Missing()}
	}
	run := c.send(ctx, prompt, key)
	// Every field the endpoint wrote: Classify copies an unusable reply into the
	// run's detail, and the ledger records the model it names.
	run.Response = redact(run.Response, key)
	run.ResponseModel = redact(run.ResponseModel, key)
	run.Detail = redact(run.Detail, key)
	run.Error = redact(run.Error, key)
	return run
}

func (c *ChatAPI) send(ctx context.Context, prompt, key string) gate.RawRun {
	body, err := json.Marshal(chatRequest{
		Model:           c.Model,
		Messages:        []chatMessage{{Role: "user", Content: prompt}},
		ReasoningEffort: c.Effort,
	})
	if err != nil {
		return gate.RawRun{Outcome: gate.OutcomeExit, Detail: "encode the request: " + err.Error()}
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return gate.RawRun{Outcome: gate.OutcomeExit, Detail: "build the request: " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return gate.RawRun{Outcome: gate.OutcomeTimeout, Detail: "no response before the deadline"}
		}
		return gate.RawRun{Outcome: gate.OutcomeExit, Detail: "request failed: " + err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return gate.RawRun{Outcome: gate.OutcomeTimeout, Detail: "no response before the deadline"}
		}
		return gate.RawRun{Outcome: gate.OutcomeExit, Detail: "read the response: " + err.Error()}
	}
	if len(data) > maxResponseBytes {
		return gate.RawRun{Outcome: gate.OutcomeUnparseable,
			Detail: "the response exceeds " + strconv.Itoa(maxResponseBytes) + " bytes"}
	}

	code := "HTTP " + strconv.Itoa(resp.StatusCode)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// The status code alone: an auth error body can echo part of the key.
		return gate.RawRun{Outcome: gate.OutcomeAuth, Detail: code}
	case resp.StatusCode == http.StatusTooManyRequests:
		return gate.RawRun{Outcome: gate.OutcomeRateLimited, Detail: code}
	case resp.StatusCode != http.StatusOK:
		detail := code
		if msg := errorMessage(data, key); msg != "" {
			detail += " " + msg
		}
		return gate.RawRun{Outcome: gate.OutcomeExit, Detail: detail}
	}

	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return gate.RawRun{Outcome: gate.OutcomeUnparseable, Detail: "the response is not JSON: " + err.Error()}
	}
	if len(parsed.Choices) == 0 {
		detail := "the response holds no choices"
		if msg := errorMessage(data, key); msg != "" {
			detail += ": " + msg
		}
		return gate.RawRun{Outcome: gate.OutcomeUnparseable, Detail: detail, ResponseModel: parsed.Model}
	}
	choice := parsed.Choices[0]
	// Only a complete reply reaches Classify. A cut or filtered reply carries no
	// Response, so salvage can never count a half review as a review.
	switch choice.FinishReason {
	case "stop":
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
	return gate.RawRun{Response: strings.TrimSpace(text), Status: "SUCCESS", ResponseModel: parsed.Model}
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
	msg := redact(strings.Join(strings.Fields(body.Error.Message), " "), key)
	if len(msg) > maxErrorChars {
		msg = strings.ToValidUTF8(msg[:maxErrorChars], "") + "…"
	}
	return msg
}

// redact removes the key from a string that leaves the adapter.
func redact(text, key string) string {
	if key == "" || text == "" {
		return text
	}
	return strings.ReplaceAll(text, key, "[redacted]")
}
