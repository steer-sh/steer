package reviewers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Codex reviews through the Codex CLI in non-interactive mode. With --json it prints
// one JSON event per line: the reply is the last agent_message item, the usage rides
// on turn.completed, and a failed run ends with turn.failed. A run whose turn
// completed is a SUCCESS, and a non-zero exit is already classified by runProcess.
type Codex struct {
	Model  string
	Effort string
	Binary string
}

func NewCodex(model, effort string) *Codex { return &Codex{Model: model, Effort: effort} }

func (c *Codex) Name() string { return "codex" }

// ModelLabel is the label the instrument records. An unset model means the CLI's own
// default, which is named as such rather than borrowed from another provider's pin.
func (c *Codex) ModelLabel() string {
	if c.Model != "" {
		return c.Model
	}
	return "codex-default"
}

func (c *Codex) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "codex"
}

func (c *Codex) Review(ctx context.Context, prompt, cwd string) gate.RawRun {
	// --ephemeral keeps the run out of the user's session history: its prompt is a
	// diff, not anything the user said. --disable hooks keeps the operator's Codex
	// hooks, including the turn-end check.sh runners, out of the review session, and
	// the read-only sandbox keeps the commands it runs from writing. --disable apps
	// drops the built-in codex_apps connectors, which the sandbox does not bind.
	// --ignore-user-config drops the user's config.toml, so no MCP server it or a
	// project it trusts declares starts outside the sandbox. The login in auth.json
	// still applies; a config.toml credential store or model_provider does not. An
	// unset model or effort falls to Codex's built-in default, not to config.toml's.
	argv := []string{c.binary(), "exec", "--json", "--skip-git-repo-check", "--ephemeral", "--ignore-user-config",
		"--sandbox", "read-only", "--disable", "hooks", "--disable", "apps"}
	if c.Model != "" {
		argv = append(argv, "--model", c.Model)
	}
	// Codex spells effort as a config override, not a flag.
	if c.Effort != "" {
		argv = append(argv, "-c", "model_reasoning_effort="+c.Effort)
	}
	argv = append(argv, "-")
	stdout, stderr, raw, failed := runProcess(ctx, cwd, argv, prompt)
	stream, parsed := parseCodexStream(stdout)
	if failed {
		if raw.Outcome == gate.OutcomeExit {
			raw.Detail = withReplyText(raw.Detail, stream.failure)
		}
		raw.Usage = stream.usage
		return raw
	}
	if !parsed && strings.TrimSpace(stdout) != "" {
		return gate.RawRun{Outcome: gate.OutcomeUnparseable, Detail: strings.TrimSpace(stdout)}
	}
	detail := stream.failure
	if detail == "" && !stream.turnCompleted {
		detail = "the stream ended without turn.completed"
	}
	if detail == "" {
		detail = stderrTail(stderr)
	}
	// On a zero exit only a completed turn that did not fail is a SUCCESS; a message
	// written before a failure or a cut stream goes through salvage. A process that
	// failed returned above with runProcess's outcome, as for every CLI reviewer.
	status := "SUCCESS"
	if stream.turnFailed || !stream.turnCompleted {
		status = "ERROR"
	}
	return gate.RawRun{
		Response: strings.TrimSpace(stream.reply),
		Status:   status,
		Error:    detail,
		Usage:    stream.usage,
	}
}

// codexEvent is one line of `codex exec --json`, with only the fields the reviewer
// reads.
type codexEvent struct {
	Type string `json:"type"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage   *codexUsage `json:"usage"`
	Message string      `json:"message"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// codexStream is what a Codex run printed: the last agent message, the usage of its
// completed turn, and the message of its failed turn or error event.
type codexStream struct {
	reply         string
	usage         *gate.TokenUsage
	failure       string
	turnFailed    bool
	turnCompleted bool
}

// parseCodexStream reads the JSON Lines of a Codex run, skipping event types it does
// not know. parsed is false when no line is an event. A turn.failed message outranks
// an error event's.
func parseCodexStream(stdout string) (stream codexStream, parsed bool) {
	var errorEvent string
	for _, line := range strings.Split(stdout, "\n") {
		var event codexEvent
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) != nil || event.Type == "" {
			continue
		}
		parsed = true
		switch event.Type {
		case "item.completed":
			if event.Item != nil && event.Item.Type == "agent_message" {
				stream.reply = event.Item.Text
			}
		case "turn.completed":
			stream.turnCompleted = true
			stream.usage = event.Usage.tokenUsage()
		case "turn.failed":
			stream.turnFailed = true
			if event.Error != nil && event.Error.Message != "" {
				stream.failure = event.Error.Message
			}
		case "error":
			if event.Message != "" {
				errorEvent = event.Message
			}
		}
	}
	if stream.failure == "" {
		stream.failure = errorEvent
	}
	return stream, parsed
}
