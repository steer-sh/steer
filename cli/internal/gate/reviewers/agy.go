package reviewers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Agy is the default reviewer: `agy --model <m> --output-format json -p <prompt>`.
// It reports a denied tool call as status ERROR while still returning the review the
// model wrote, which is why Classify salvages a body carrying findings.
type Agy struct {
	Model string
	// Binary defaults to "agy". Overridable so a test can point at a stub.
	Binary string
}

func NewAgy(model string) *Agy { return &Agy{Model: model} }

func (a *Agy) Name() string { return "agy" }

// ModelLabel is the pin the instrument records.
func (a *Agy) ModelLabel() string {
	if a.Model != "" {
		return a.Model
	}
	return "agy-default"
}

func (a *Agy) binary() string {
	if a.Binary != "" {
		return a.Binary
	}
	return "agy"
}

type agyReply struct {
	Response string `json:"response"`
	Status   string `json:"status"`
	Error    any    `json:"error"`
}

func (a *Agy) Review(ctx context.Context, prompt, cwd string) gate.RawRun {
	argv := []string{a.binary()}
	if a.Model != "" {
		argv = append(argv, "--model", a.Model)
	}
	argv = append(argv, "--output-format", "json", "-p", prompt)
	stdout, stderr, raw, failed := runProcess(ctx, cwd, argv, "")
	if failed {
		return raw
	}
	var reply agyReply
	if err := json.Unmarshal([]byte(stdout), &reply); err != nil {
		detail := strings.TrimSpace(stdout)
		if detail == "" {
			detail = strings.TrimSpace(stderr)
		}
		return gate.RawRun{Outcome: gate.OutcomeUnparseable, Detail: detail}
	}
	return gate.RawRun{
		Response: reply.Response,
		Status:   reply.Status,
		Error:    stringify(reply.Error),
	}
}

func stringify(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}
