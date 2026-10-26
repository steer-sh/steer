package reviewers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/apikey"
	"github.com/steer-sh/steer-cli/internal/gate"
)

const testKey = "sk-test-secret-0123456789"

func keyed(string) (string, apikey.Status) {
	return testKey, apikey.Status{Source: apikey.SourceFile}
}

// chatServer answers every request with status and body, and records the last
// request it saw.
func chatServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	var seen http.Request
	var payload []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r.Clone(context.Background())
		payload, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen, &payload
}

func chatReviewer(baseURL string) *ChatAPI {
	r := NewChatAPI(Options{Model: "vendor/model-x", BaseURL: baseURL})
	r.FindKey = keyed
	return r
}

func reply(finish, content string) string {
	b, _ := json.Marshal(map[string]any{
		"model": "vendor/model-x-2026",
		"choices": []any{map[string]any{
			"finish_reason": finish,
			"message":       map[string]any{"role": "assistant", "content": content},
		}},
	})
	return string(b)
}

func TestChatAPIMapsEachEndpointResult(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		outcome  gate.RunOutcome
		response string
	}{
		{"complete reply", 200, reply("stop", "NO FINDINGS"), "", "NO FINDINGS"},
		{"cut at the limit", 200, reply("length", "1. [HIGH] half a fin"), gate.OutcomeTruncated, ""},
		{"filtered", 200, reply("content_filter", "1. [HIGH] x"), gate.OutcomeExit, ""},
		{"no finish reason", 200, reply("", "NO FINDINGS"), "", "NO FINDINGS"},
		{"no finish reason, no text", 200, reply("", " "), gate.OutcomeExit, ""},
		{"no choices", 200, `{"choices":[]}`, gate.OutcomeUnparseable, ""},
		{"not JSON", 200, `<html>`, gate.OutcomeUnparseable, ""},
		{"unauthorized", 401, `{"error":{"message":"bad key"}}`, gate.OutcomeAuth, ""},
		{"forbidden", 403, `{}`, gate.OutcomeAuth, ""},
		{"rate limited", 429, `{}`, gate.OutcomeRateLimited, ""},
		{"server error", 502, `{"error":{"message":"upstream down"}}`, gate.OutcomeExit, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _, _ := chatServer(t, c.status, c.body)
			raw := chatReviewer(srv.URL).Review(context.Background(), "prompt", "")
			if raw.Outcome != c.outcome {
				t.Fatalf("Outcome = %q, want %q (detail %q)", raw.Outcome, c.outcome, raw.Detail)
			}
			if raw.Response != c.response {
				t.Fatalf("Response = %q, want %q", raw.Response, c.response)
			}
			if c.outcome == "" && raw.Status != "SUCCESS" {
				t.Fatalf("Status = %q, want SUCCESS", raw.Status)
			}
		})
	}
}

// TestChatAPIRateLimitKeepsTheEndpointMessage: a 429 names its cause, a quota on
// the key or a limit upstream, with the key removed.
func TestChatAPIRateLimitKeepsTheEndpointMessage(t *testing.T) {
	srv, _, _ := chatServer(t, 429, `{"error":{"message":"model is rate-limited upstream for `+testKey+`"}}`)
	raw := chatReviewer(srv.URL).Review(context.Background(), "prompt", "")
	if raw.Outcome != gate.OutcomeRateLimited || !strings.Contains(raw.Detail, "HTTP 429 model is rate-limited upstream") {
		t.Errorf("run = %q %q; want the endpoint message", raw.Outcome, raw.Detail)
	}
	if strings.Contains(raw.Detail, testKey) {
		t.Errorf("Detail = %q; carries the key", raw.Detail)
	}
}

func TestChatAPISendsTheKeyModelPromptAndEffort(t *testing.T) {
	srv, seen, payload := chatServer(t, 200, reply("stop", "NO FINDINGS"))
	r := chatReviewer(srv.URL + "/api/v1/")
	r.Effort = "high"
	raw := r.Review(context.Background(), "the prompt", "")
	if raw.ResponseModel != "vendor/model-x-2026" {
		t.Errorf("ResponseModel = %q, want the model the reply names", raw.ResponseModel)
	}
	if seen.URL.Path != "/api/v1/chat/completions" || seen.Method != http.MethodPost {
		t.Errorf("request = %s %s", seen.Method, seen.URL.Path)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(*payload, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "vendor/model-x" || body["reasoning_effort"] != "high" {
		t.Errorf("body = %v", body)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["content"] != "the prompt" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestChatAPIOmitsAnUnsetEffort(t *testing.T) {
	srv, _, payload := chatServer(t, 200, reply("stop", "NO FINDINGS"))
	chatReviewer(srv.URL).Review(context.Background(), "p", "")
	if strings.Contains(string(*payload), "reasoning_effort") {
		t.Fatalf("an unset effort was sent: %s", *payload)
	}
}

func TestChatAPIKeepsTheKeyOutOfEveryDetail(t *testing.T) {
	long := strings.Repeat("x", maxErrorChars-14) + testKey
	for _, msg := range []string{"invalid key " + testKey, long} {
		body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg}})
		srv, _, _ := chatServer(t, 400, string(body))
		raw := chatReviewer(srv.URL).Review(context.Background(), "p", "")
		if raw.Outcome != gate.OutcomeExit {
			t.Fatalf("Outcome = %q", raw.Outcome)
		}
		if strings.Contains(raw.Detail, testKey[:12]) {
			t.Fatalf("Detail carries the key: %q", raw.Detail)
		}
		if !strings.HasPrefix(raw.Detail, "HTTP 400") {
			t.Errorf("Detail = %q, want the status code first", raw.Detail)
		}
	}
}

func TestChatAPIAuthDetailIsTheStatusCodeOnly(t *testing.T) {
	srv, _, _ := chatServer(t, 401, `{"error":{"message":"key sk-test-secret was revoked"}}`)
	raw := chatReviewer(srv.URL).Review(context.Background(), "p", "")
	if raw.Detail != "HTTP 401" {
		t.Fatalf("Detail = %q, want the status code alone", raw.Detail)
	}
}

func TestChatAPINoKeyEndsTheRunBeforeAnyRequest(t *testing.T) {
	srv, seen, _ := chatServer(t, 200, reply("stop", "NO FINDINGS"))
	r := chatReviewer(srv.URL)
	r.FindKey = func(env string) (string, apikey.Status) {
		return "", apikey.Status{Source: apikey.SourceMissing, Env: env, Path: "/k"}
	}
	r.KeyEnv = "OPENROUTER_API_KEY"
	raw := r.Review(context.Background(), "p", "")
	if raw.Outcome != gate.OutcomeNoKey {
		t.Fatalf("Outcome = %q, want no_key", raw.Outcome)
	}
	if !strings.Contains(raw.Detail, "$OPENROUTER_API_KEY") || !strings.Contains(raw.Detail, "/k") {
		t.Errorf("Detail = %q, want the variable and the path", raw.Detail)
	}
	if seen.Method != "" {
		t.Error("a request was sent with no key")
	}
}

func TestChatAPIDeadlineIsATimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	raw := chatReviewer(srv.URL).Review(ctx, "p", "")
	if raw.Outcome != gate.OutcomeTimeout {
		t.Fatalf("Outcome = %q, want timeout (detail %q)", raw.Outcome, raw.Detail)
	}
}

func TestChatAPIRefusesARedirectToAnotherOrigin(t *testing.T) {
	other, seen, _ := chatServer(t, 200, reply("stop", "NO FINDINGS"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	raw := chatReviewer(srv.URL).Review(context.Background(), "p", "")
	if raw.Outcome != gate.OutcomeExit {
		t.Fatalf("Outcome = %q, want exit", raw.Outcome)
	}
	if seen.Method != "" {
		t.Fatal("the request followed the redirect to another origin")
	}
}

// TestChatAPIUnreachableEndpoint: a request with no HTTP reply is unreachable, apart
// from an endpoint that answered with an error.
func TestChatAPIUnreachableEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	raw := chatReviewer(addr).Review(context.Background(), "p", "")
	if raw.Outcome != gate.OutcomeUnreachable {
		t.Fatalf("Outcome = %q, want unreachable (detail %q)", raw.Outcome, raw.Detail)
	}
}

// TestChatAPIRedirectLoopIsNotUnreachable: an endpoint that answered with redirects
// sent a reply, so its failure is exit.
func TestChatAPIRedirectLoopIsNotUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	raw := chatReviewer(srv.URL).Review(context.Background(), "p", "")
	if raw.Outcome != gate.OutcomeExit {
		t.Fatalf("Outcome = %q, want exit (detail %q)", raw.Outcome, raw.Detail)
	}
}

func TestChatAPIJoinsTextParts(t *testing.T) {
	body := `{"choices":[{"finish_reason":"stop","message":{"content":[` +
		`{"type":"text","text":"NO "},{"type":"reasoning","text":"hidden"},{"type":"text","text":"FINDINGS"}]}}]}`
	srv, _, _ := chatServer(t, 200, body)
	raw := chatReviewer(srv.URL).Review(context.Background(), "p", "")
	if raw.Response != "NO FINDINGS" {
		t.Fatalf("Response = %q", raw.Response)
	}
}

func TestCheckBaseURL(t *testing.T) {
	ok := []string{"https://chat/completions", "https://openrouter.ai/api/v1", "http://localhost:11434/v1", "http://127.0.0.1:8080", "http://[::1]:8080/v1"}
	bad := []string{"http://openrouter.ai/api/v1", "https://user:pw@openrouter.ai/api/v1", "ftp://x", "https://", "https://x/v1?key=1", "https://x/v1/chat/completions?", "https://x/v1/chat/completions#", "not a url", "https://api.groq.com/openai/v1/chat/completions/"}
	for _, u := range ok {
		if err := CheckBaseURL(u); err != nil {
			t.Errorf("CheckBaseURL(%q) = %v, want accepted", u, err)
		}
	}
	for _, u := range bad {
		if err := CheckBaseURL(u); err == nil {
			t.Errorf("CheckBaseURL(%q) accepted", u)
		}
	}
}

// TestCheckBaseURLNamesTheBaseOfAFullEndpoint: a pasted endpoint URL is refused
// with the base URL the reviewer needs.
func TestCheckBaseURLNamesTheBaseOfAFullEndpoint(t *testing.T) {
	err := CheckBaseURL("https://api.groq.com/openai/v1/chat/completions")
	if err == nil || !strings.Contains(err.Error(), "use https://api.groq.com/openai/v1") {
		t.Errorf("err = %v; want the base URL named", err)
	}
	err = CheckBaseURL("https://proxy.example/tenant%2Fname/v1/chat/completions")
	if err == nil || !strings.Contains(err.Error(), "use https://proxy.example/tenant%2Fname/v1") {
		t.Errorf("err = %v; want the escaped path kept", err)
	}
}

func TestResolveRefusesAnIncompleteAPIProvider(t *testing.T) {
	if _, err := Resolve(APIProviderID, Options{Model: "m"}); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("no URL: err = %v", err)
	}
	if _, err := Resolve(APIProviderID, Options{BaseURL: "https://x/v1"}); err == nil || !strings.Contains(err.Error(), "reviewer.model") {
		t.Errorf("no model: err = %v", err)
	}
	r, err := Resolve(APIProviderID, Options{Model: "m", BaseURL: "https://api.example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ModelLabel() != "m" || gate.EndpointOf(r) != "api.example.com" {
		t.Errorf("ModelLabel = %q, Endpoint = %q", r.ModelLabel(), gate.EndpointOf(r))
	}
}

func TestProbeConfigNamesWhatIsMissing(t *testing.T) {
	saved := findKey
	t.Cleanup(func() { findKey = saved })
	findKey = func(env string) (string, apikey.Status) {
		return "", apikey.Status{Source: apikey.SourceMissing, Env: env}
	}
	full := Options{Model: "m", BaseURL: "https://x/v1"}
	cases := []struct {
		opts  Options
		unset string
	}{
		{Options{Model: "m"}, "reviewer.api.base_url"},
		{Options{Model: "m", BaseURL: "http://remote.example/v1"}, "reviewer.api.base_url"},
		{Options{BaseURL: "https://x/v1"}, "reviewer.model"},
		{full, UnsetKey},
	}
	for _, c := range cases {
		p := ProbeConfig(APIProviderID, c.opts)
		if p.State != ProbeMissing || p.Unset != c.unset {
			t.Errorf("ProbeConfig(%+v) = %+v, want missing %s", c.opts, p, c.unset)
		}
	}
	findKey = keyed
	if p := ProbeConfig(APIProviderID, full); p.State != ProbeAvailable || p.Key != "file" {
		t.Errorf("ProbeConfig with a key = %+v, want available from file", p)
	}
}

// A reply that echoes the key is not a review, and Classify copies an unusable reply
// into the run's detail, so the key must leave the response too.
func TestChatAPIKeepsTheKeyOutOfTheResponse(t *testing.T) {
	body := strings.Replace(reply("stop", "Invalid API key: "+testKey), "vendor/model-x-2026", testKey, 1)
	srv, _, _ := chatServer(t, 200, body)
	raw := chatReviewer(srv.URL).Review(context.Background(), "p", "")
	run := gate.Classify(raw)
	for name, v := range map[string]string{"response": raw.Response, "detail": run.Detail, "model": run.ResponseModel} {
		if strings.Contains(v, testKey) {
			t.Errorf("the key reached the run's %s: %q", name, v)
		}
	}
}
