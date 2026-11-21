package reviewers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Pi reviews through `pi --mode json` with the prompt on stdin. Its only tools read
// files, and they read any path the process can, not only the repository. It saves
// no session and loads no discovered resource or project-local configuration. Pi
// exits zero after an assistant error or abort, and agent_end can precede a retry,
// so only a settled, unaborted stream whose last message stopped is a SUCCESS.
type Pi struct {
	Model  string
	Effort string
	// Binary defaults to "pi". Overridable so a test can point at a stub.
	Binary string
}

// piReadTools are the tools a Pi review may call.
const piReadTools = "read,grep,find,ls"

// piSystemPrompt replaces Pi's coding-assistant prompt and any SYSTEM.md.
const piSystemPrompt = "You review a code change. The user message holds your complete instructions " +
	"and the change itself. Your working directory is the repository: you may read, search and list " +
	"its files to check the change, and you cannot change anything.\n"

// piAuthCheckTimeout bounds the readiness check a failed run makes.
const piAuthCheckTimeout = 15 * time.Second

func NewPi(model, effort string) *Pi { return &Pi{Model: model, Effort: effort} }

func (p *Pi) Name() string { return "pi" }

// ModelLabel is the label the instrument records; an unset model means Pi's own default.
func (p *Pi) ModelLabel() string {
	if p.Model != "" {
		return p.Model
	}
	return "pi-default"
}

func (p *Pi) binary() string {
	if p.Binary != "" {
		return p.Binary
	}
	return "pi"
}

// promptFiles writes the system prompt and an empty appended prompt to a private
// directory and returns their paths. Pi reads a prompt flag as a file when the path
// exists, and a flag skips its discovery of SYSTEM.md and APPEND_SYSTEM.md; an
// absolute path keeps a file of the same name in the checkout from being read.
func promptFiles() (dir, system, appended string, err error) {
	dir, err = os.MkdirTemp("", "steer-review-pi-*")
	if err != nil {
		return "", "", "", err
	}
	system = filepath.Join(dir, "system.md")
	appended = filepath.Join(dir, "append.md")
	if err = os.WriteFile(system, []byte(piSystemPrompt), 0o600); err == nil {
		err = os.WriteFile(appended, nil, 0o600)
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", "", "", err
	}
	return dir, system, appended, nil
}

func (p *Pi) Review(ctx context.Context, prompt, cwd string) gate.RawRun {
	dir, system, appended, err := promptFiles()
	if err != nil {
		return gate.RawRun{Outcome: gate.OutcomeSpawn, Detail: "write the Pi prompt files: " + err.Error()}
	}
	defer os.RemoveAll(dir)
	// The user's auth.json, models.json and settings.json still apply: they hold the
	// login, custom endpoints and retry settings. --offline skips Pi's version check,
	// catalog refresh and package updates; a provider still refreshes its OAuth token.
	argv := []string{p.binary(), "--mode", "json", "--no-session", "--tools", piReadTools,
		"--no-extensions", "--no-mcp", "--no-skills", "--no-prompt-templates", "--no-themes",
		"--no-context-files", "--no-approve", "--offline",
		"--system-prompt", system, "--append-system-prompt", appended}
	if p.Model != "" {
		argv = append(argv, "--model", p.Model)
	}
	if p.Effort != "" {
		argv = append(argv, "--thinking", p.Effort)
	}
	stdout, stderr, raw, failed := runProcess(ctx, cwd, argv, prompt)
	stream, parsed := parsePiStream(stdout)
	if failed {
		if raw.Outcome == gate.OutcomeExit {
			note := stream.failure
			if auth := p.authCheck(ctx, cwd); auth != "" {
				note = auth
			}
			raw.Detail = withReplyText(raw.Detail, note)
		}
		raw.Usage = stream.usage
		return raw
	}
	if !parsed && strings.TrimSpace(stdout) != "" {
		return gate.RawRun{Outcome: gate.OutcomeUnparseable, Detail: strings.TrimSpace(stdout)}
	}
	status, detail := "SUCCESS", ""
	switch {
	case !stream.settled:
		status, detail = "ERROR", "the stream ended without agent_settled"
	case stream.aborted:
		status, detail = "ERROR", "the run was aborted"
	case stream.stopReason == "length":
		status, detail = "ERROR", "the reply stopped at the model's output limit"
	case stream.stopReason != "stop":
		status, detail = "ERROR", "stopReason="+stream.stopReason
	}
	if status == "ERROR" {
		if stream.failure != "" {
			detail = stream.failure
		} else if detail == "" {
			detail = stderrTail(stderr)
		}
	}
	return gate.RawRun{
		Response:      strings.TrimSpace(stream.reply),
		Status:        status,
		Error:         detail,
		Usage:         stream.usage,
		ResponseModel: stream.responseModel,
	}
}

// authCheck asks Pi whether the configured model's provider has credentials, and
// returns a line for a run's detail when it is not ready. Pi's own exit message
// opens its stderr, ahead of document paths that a tail keeps instead. It returns ""
// for an unset model, a ready provider, or a check that does not answer.
func (p *Pi) authCheck(ctx context.Context, cwd string) string {
	if p.Model == "" || ctx.Err() != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, piAuthCheckTimeout)
	defer cancel()
	stdout, _, _, _ := runProcess(ctx, cwd,
		[]string{p.binary(), "auth", "check", "--model", p.Model, "--json", "--no-refresh"}, "")
	var check struct {
		Status   string `json:"status"`
		Provider string `json:"provider"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &check) != nil || check.Status == "" || check.Status == "ready" {
		return ""
	}
	line := "pi auth check: " + check.Status
	if check.Provider != "" {
		line += " for " + check.Provider
	}
	if check.Reason != "" {
		line += " (" + check.Reason + ")"
	}
	return line
}

// piEvent is one line of `pi --mode json`, with only the fields the reviewer reads.
type piEvent struct {
	Type       string     `json:"type"`
	Message    *piMessage `json:"message"`
	Aborted    bool       `json:"aborted"`
	Success    *bool      `json:"success"`
	FinalError string     `json:"finalError"`
}

type piMessage struct {
	Role          string          `json:"role"`
	Content       json.RawMessage `json:"content"`
	StopReason    string          `json:"stopReason"`
	ErrorMessage  string          `json:"errorMessage"`
	ResponseModel string          `json:"responseModel"`
	Usage         *piUsage        `json:"usage"`
}

// text joins the message's text blocks. A user message's string content is not an
// assistant reply, so a content that is no block list has none.
func (m *piMessage) text() string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// piStream is what a Pi run printed: the last assistant message's text, stop reason
// and model, the usage of every assistant message, and how the run settled.
type piStream struct {
	reply         string
	stopReason    string
	responseModel string
	failure       string
	usage         *gate.TokenUsage
	settled       bool
	aborted       bool
}

// parsePiStream reads the JSON Lines of a Pi run, skipping event types it does not
// know. parsed is false when no line is an event. Each assistant message_end is one
// billed attempt, a retried one included, so the usage sums them all. A failed final
// retry's error outranks the last message's.
func parsePiStream(stdout string) (stream piStream, parsed bool) {
	var usage piUsageSum
	var retryError string
	for _, line := range strings.Split(stdout, "\n") {
		var event piEvent
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) != nil || event.Type == "" {
			continue
		}
		parsed = true
		switch event.Type {
		case "message_end":
			m := event.Message
			if m == nil || m.Role != "assistant" {
				continue
			}
			usage.add(m.Usage)
			stream.reply = m.text()
			stream.stopReason = m.StopReason
			stream.responseModel = m.ResponseModel
			stream.failure = m.ErrorMessage
		case "auto_retry_end":
			if event.Success != nil && !*event.Success {
				retryError = event.FinalError
			}
		case "agent_settled":
			stream.settled = true
			stream.aborted = event.Aborted
		}
	}
	if retryError != "" {
		stream.failure = retryError
	}
	stream.usage = usage.tokenUsage()
	return stream, parsed
}
