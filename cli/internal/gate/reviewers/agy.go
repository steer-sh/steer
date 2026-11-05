package reviewers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Agy reviews through `agy --model <m> --output-format json -p <prompt>`.
//
// agy calls tools on its own, and the gate grants it no permission to: the operator's
// agy settings are the only boundary. A denial does not stop the run. It leaves either
// status on a body that may be a full review or only the refusal, so the status names
// the branch Classify takes and never decides on its own that a review happened.
type Agy struct {
	Model  string
	Effort string
	// Binary defaults to "agy". Overridable so a test can point at a stub.
	Binary string
}

// agyTimeoutMargin keeps agy's own wall clock above the panel's.
const agyTimeoutMargin = 60 * time.Second

func NewAgy(model, effort string) *Agy { return &Agy{Model: model, Effort: effort} }

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
	if a.Effort != "" {
		argv = append(argv, "--effort", a.Effort)
	}
	// agy's own print-timeout defaults to 5m and becomes the shorter clock the moment
	// the panel's deadline passes it. The panel's deadline stays the authoritative one,
	// so agy is given a wall clock above the context it runs under.
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			argv = append(argv, "--print-timeout", (remaining + agyTimeoutMargin).Round(time.Second).String())
		}
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
