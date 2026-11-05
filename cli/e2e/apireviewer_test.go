//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const e2eAPIKey = "sk-e2e-0123456789"

// chatStub is an OpenAI-compatible endpoint that answers every review clean and
// records the Authorization header of each request.
type chatStub struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
}

func newChatStub(t *testing.T) *chatStub {
	stub := &chatStub{}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		stub.mu.Lock()
		stub.auths = append(stub.auths, r.Header.Get("Authorization"))
		stub.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"model": "stub/answering-model",
			"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "NO FINDINGS"},
			}},
		})
	}))
	t.Cleanup(stub.Close)
	return stub
}

func (c *chatStub) Auths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.auths...)
}

// apiSandbox arms the pre-commit channel with a three-run panel of the
// openai-compatible reviewer pointed at stub.
func apiSandbox(t *testing.T, stub *chatStub) *Sandbox {
	s := NewSandbox(t)
	s.MustSteer("config", "set", "reviewer.provider", "openai-compatible")
	s.MustSteer("config", "set", "reviewer.api.base_url", stub.URL+"/v1")
	s.MustSteer("config", "set", "reviewer.model", "stub/model")
	s.MustSteer("config", "set", "reviewer.runs", "3")
	s.MustSteer("config", "set", "reviewer.timeout", "10")
	s.MustSteer("commit-gate", "install", "--precommit")
	return s
}

// TestAPIReviewerReviewsACommitWithTheKeyFile runs a commit through the API reviewer
// with the key in its file, and reads the endpoint and the answering model back from
// the ledger.
//
// pins existing behaviour
func TestAPIReviewerReviewsACommitWithTheKeyFile(t *testing.T) {
	t.Parallel()
	stub := newChatStub(t)
	s := apiSandbox(t, stub)
	dir := filepath.Join(s.Home, ".steer", "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reviewer-api-key"), []byte(e2eAPIKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := s.MustSteer("reviewer", "key", "status").Stdout; strings.TrimSpace(out) != "file" {
		t.Fatalf("key status = %q, want file", out)
	}

	before := s.Head()
	s.StageCode("mod.py")
	r := s.Run(s.Repo, "", nil, "git", "commit", "-m", "add mod")
	if r.Code != 0 || s.Head() == before {
		t.Fatalf("commit exited %d:\n%s", r.Code, r.Output())
	}
	auths := stub.Auths()
	if len(auths) != 3 {
		t.Fatalf("the endpoint saw %d requests, want 3", len(auths))
	}
	for _, a := range auths {
		if a != "Bearer "+e2eAPIKey {
			t.Errorf("Authorization = %q", a)
		}
	}
	rows := s.LedgerRows()
	if len(rows) != 1 || rows[0]["phase"] != "clean" {
		t.Fatalf("ledger rows = %v, want one clean row", rows)
	}
	host := strings.TrimPrefix(stub.URL, "http://")
	if got := dig(rows[0], "instrument_fields", "endpoint"); got != host {
		t.Errorf("instrument endpoint = %v, want %s", got, host)
	}
	if got := dig(rows[0], "instrument_fields", "model"); got != "stub/model" {
		t.Errorf("instrument model = %v, want the configured model", got)
	}
	if got := dig(rows[0], "reviewer_response_models", "stub/answering-model"); got != float64(3) {
		t.Errorf("reviewer_response_models = %v", rows[0]["reviewer_response_models"])
	}
	data, _ := os.ReadFile(filepath.Join(s.Home, ".steer", "config.json"))
	if strings.Contains(string(data), e2eAPIKey) {
		t.Error("config.json carries the key")
	}
}

// TestAPIReviewerWithNoKeyRecordsNoKey checks that a missing key sends no request
// and leaves no_key in the ledger, where a hook environment that lacks the variable
// becomes visible.
//
// pins existing behaviour
func TestAPIReviewerWithNoKeyRecordsNoKey(t *testing.T) {
	t.Parallel()
	stub := newChatStub(t)
	s := apiSandbox(t, stub)
	s.MustSteer("config", "set", "reviewer.api.key_env", "STEER_E2E_API_KEY")
	s.StageCode("mod.py")
	r := s.Run(s.Repo, "", nil, "git", "commit", "-m", "add mod")
	if n := len(stub.Auths()); n != 0 {
		t.Fatalf("the endpoint saw %d requests with no key", n)
	}
	rows := s.LedgerRows()
	if len(rows) == 0 {
		t.Fatalf("no ledger row; commit output:\n%s", r.Output())
	}
	if got := dig(rows[len(rows)-1], "reviewer_outcomes", "no_key"); got != float64(3) {
		t.Errorf("reviewer_outcomes = %v, want three no_key", rows[len(rows)-1]["reviewer_outcomes"])
	}
	status := s.Steer("status")
	if !strings.Contains(status.Output(), "steer reviewer key set") {
		t.Errorf("status does not name the key command:\n%s", status.Output())
	}

	s.Setenv("STEER_E2E_API_KEY", e2eAPIKey)
	if out := s.MustSteer("reviewer", "key", "status").Stdout; strings.TrimSpace(out) != "env STEER_E2E_API_KEY" {
		t.Errorf("key status with the variable set = %q", out)
	}
}

// TestAPIKeySettingIsRefused checks that a guessed key setting fails, names the
// command that reads the key with no echo, and writes nothing.
//
// pins existing behaviour
func TestAPIKeySettingIsRefused(t *testing.T) {
	t.Parallel()
	s := NewSandbox(t)
	r := s.Steer("config", "set", "reviewer.api.key", e2eAPIKey)
	if r.Code == 0 || !strings.Contains(r.Output(), "steer reviewer key set") {
		t.Fatalf("config set reviewer.api.key exited %d:\n%s", r.Code, r.Output())
	}
	if data, err := os.ReadFile(filepath.Join(s.Home, ".steer", "config.json")); err == nil &&
		strings.Contains(string(data), e2eAPIKey) {
		t.Error("config.json carries the key")
	}
	if r := s.Run(s.Repo, "", nil, "steer", "reviewer", "key", "set"); r.Code == 0 ||
		!strings.Contains(r.Output(), "not a terminal") {
		t.Errorf("key set from a pipe exited %d:\n%s", r.Code, r.Output())
	}
}
