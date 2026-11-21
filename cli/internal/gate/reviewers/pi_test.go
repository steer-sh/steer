package reviewers

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// piStub is a fake pi. A review call records its argv, its stdin and the system
// prompt file it was given, prints stream, sleeps for sleep and exits with exit.
// `pi auth check` prints auth and exits 1.
type piStub struct {
	binary, dir string
}

func newPiStub(t *testing.T, stream string, exit int, sleep, auth string) piStub {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stream", stream)
	write("auth", auth)
	script := `#!/bin/sh
D='` + dir + `'
if [ "$1" = auth ]; then printf '%s\n' "$@" > "$D/auth-argv"; cat "$D/auth"; exit 1; fi
printf '%s\n' "$@" > "$D/argv"
cat > "$D/stdin"
prev=
for a in "$@"; do
  if [ "$prev" = --system-prompt ]; then cp "$a" "$D/system"; echo "$a" > "$D/system-path"; fi
  prev=$a
done
cat "$D/stream"
sleep ` + sleep + `
exit ` + string(rune('0'+exit)) + "\n"
	write("pi", script)
	if err := os.Chmod(filepath.Join(dir, "pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	return piStub{binary: filepath.Join(dir, "pi"), dir: dir}
}

func (s piStub) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return string(data)
}

func (s piStub) review(t *testing.T, ctx context.Context, model, effort string) gate.RawRun {
	t.Helper()
	reviewer := NewPi(model, effort)
	reviewer.Binary = s.binary
	return reviewer.Review(ctx, "PROMPT", t.TempDir())
}

const piSettled = `{"type":"agent_end","willRetry":false}
{"type":"agent_settled","aborted":false}`

func piAssistant(text, stop, extra string) string {
	return `{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},` +
		`{"type":"text","text":"` + text + `"}],"stopReason":"` + stop + `"` + extra +
		`,"usage":{"input":100,"output":20,"cacheRead":30,"cacheWrite":5,"reasoning":8,"totalTokens":155}}}`
}

// The review gets only Pi's read tools, no session, no discovered resource
// and no project-local file, with Steer's system prompt in place of any SYSTEM.md or
// APPEND_SYSTEM.md. The model and effort reach Pi as --model and --thinking.
func TestPiRendersAPromptOnlyArgvWithThePromptOnStdin(t *testing.T) {
	base := []string{"--mode", "json", "--no-session", "--tools", "read,grep,find,ls", "--no-extensions", "--no-mcp",
		"--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve", "--offline",
		"--system-prompt", "SYSTEM", "--append-system-prompt", "APPEND"}
	cases := []struct {
		model, effort string
		tail          []string
	}{
		{"openai-codex/gpt-6-luna", "high", []string{"--model", "openai-codex/gpt-6-luna", "--thinking", "high"}},
		{"", "", nil},
	}
	for _, c := range cases {
		stub := newPiStub(t, piAssistant("NO FINDINGS", "stop", "")+"\n"+piSettled, 0, "0", "")
		if run := stub.review(t, context.Background(), c.model, c.effort); run.Status != "SUCCESS" {
			t.Fatalf("Review = %#v; want SUCCESS", run)
		}
		argv := recordedArgv(t, filepath.Join(stub.dir, "argv"))
		for i := range argv {
			if i > 0 && argv[i-1] == "--system-prompt" {
				argv[i] = "SYSTEM"
			}
			if i > 0 && argv[i-1] == "--append-system-prompt" {
				argv[i] = "APPEND"
			}
		}
		assertArgv(t, argv, append(append([]string{}, base...), c.tail...))
		if got := stub.read(t, "stdin"); got != "PROMPT" {
			t.Fatalf("stdin = %q; want the prompt", got)
		}
		if got := stub.read(t, "system"); got != piSystemPrompt {
			t.Fatalf("system prompt = %q", got)
		}
		path := strings.TrimSpace(stub.read(t, "system-path"))
		if !filepath.IsAbs(path) {
			t.Fatalf("--system-prompt %q; want an absolute path", path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("prompt file %s outlived the review: %v", path, err)
		}
	}
}

// Pi exits zero after an assistant error, an abort or a cut stream, and agent_end can
// precede a retry. Only a settled stream whose last message stopped is a SUCCESS.
func TestPiSucceedsOnlyOnASettledStop(t *testing.T) {
	retried := `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error",` +
		`"errorMessage":"529 overloaded","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}}
{"type":"agent_end","willRetry":true}
{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":1,"errorMessage":"529 overloaded"}
{"type":"auto_retry_end","success":true,"attempt":2}
`
	cases := []struct {
		name, stream     string
		status, response string
		errorHas         string
		outcome          gate.RunOutcome
	}{
		{name: "stop", stream: piAssistant("NO FINDINGS", "stop", "") + "\n" + piSettled,
			status: "SUCCESS", response: "NO FINDINGS"},
		{name: "retried then stopped", stream: retried + piAssistant("NO FINDINGS", "stop", "") + "\n" + piSettled,
			status: "SUCCESS", response: "NO FINDINGS"},
		{name: "assistant error", stream: piAssistant("", "error", `,"errorMessage":"401 unauthorized"`) + "\n" + piSettled,
			status: "ERROR", errorHas: "401 unauthorized"},
		{name: "retries exhausted", stream: piAssistant("", "error", `,"errorMessage":"529 overloaded"`) +
			"\n" + `{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"gave up after 3 attempts"}` + "\n" + piSettled,
			status: "ERROR", errorHas: "gave up after 3 attempts"},
		{name: "aborted", stream: piAssistant("partial", "aborted", "") + "\n" + `{"type":"agent_settled","aborted":true}`,
			status: "ERROR", response: "partial", errorHas: "aborted"},
		{name: "truncated", stream: piAssistant("1. [HIGH] a.go:1 cut", "length", "") + "\n" + piSettled,
			status: "ERROR", response: "1. [HIGH] a.go:1 cut", errorHas: "output limit"},
		{name: "agent_end before a retry that never came", stream: piAssistant("NO FINDINGS", "stop", "") +
			"\n" + `{"type":"agent_end","willRetry":true}`, status: "ERROR", response: "NO FINDINGS", errorHas: "agent_settled"},
		{name: "not JSON", stream: "Error: something", outcome: gate.OutcomeUnparseable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := newPiStub(t, c.stream, 0, "0", "").review(t, context.Background(), "", "")
			if run.Outcome != c.outcome || run.Status != c.status || run.Response != c.response ||
				!strings.Contains(run.Error, c.errorHas) {
				t.Fatalf("Review = %#v", run)
			}
		})
	}
}

// Every assistant message is one billed attempt, a retried one included. Pi's input
// excludes both cache terms and its output includes reasoning.
func TestPiUsageSumsEveryAttemptWithTheCacheInInput(t *testing.T) {
	stream := piAssistant("", "error", `,"errorMessage":"529"`) + "\n" +
		piAssistant("NO FINDINGS", "stop", `,"responseModel":"gpt-6-luna-2026"`) + "\n" + piSettled
	run := newPiStub(t, stream, 0, "0", "").review(t, context.Background(), "", "")
	u := run.Usage
	if u == nil || u.Input == nil || *u.Input != 270 || *u.CachedInput != 60 || *u.Output != 40 ||
		*u.Reasoning != 16 || u.CostUSD != nil {
		t.Fatalf("usage = %+v", u)
	}
	if run.ResponseModel != "gpt-6-luna-2026" {
		t.Fatalf("ResponseModel = %q", run.ResponseModel)
	}
}

// Pi's exit message opens its stderr and document paths close it, so a tail drops
// the cause. A failed run names what `pi auth check` says about the model instead.
func TestPiExitDetailNamesTheAuthCheck(t *testing.T) {
	stub := newPiStub(t, "", 1, "0", `{"status":"not_ready","provider":"groq","reason":"credentials_not_configured"}`)
	run := stub.review(t, context.Background(), "groq/llama", "")
	if run.Outcome != gate.OutcomeExit ||
		!strings.HasPrefix(run.Detail, "rc=1 pi auth check: not_ready for groq (credentials_not_configured)") {
		t.Fatalf("run = %#v", run)
	}
	assertArgv(t, recordedArgv(t, filepath.Join(stub.dir, "auth-argv")),
		[]string{"auth", "check", "--model", "groq/llama", "--json", "--no-refresh"})
}

// The panel's deadline stops Pi, and the run is a timeout, not an exit.
func TestPiTimesOutAtTheDeadline(t *testing.T) {
	stub := newPiStub(t, "", 0, "30", "")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if run := stub.review(t, ctx, "", ""); run.Outcome != gate.OutcomeTimeout {
		t.Fatalf("run = %#v; want a timeout", run)
	}
}
