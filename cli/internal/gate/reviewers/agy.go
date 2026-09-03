package reviewers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Agy is the default reviewer. It runs with its tools enabled inside agy's own
// sandbox, so the reviewer can read the files around the diff rather than review the
// diff alone. The sandbox flag and the auto-approval flag are one decision and are
// never passed apart. A deny rule in the user's agy settings still wins over the
// auto-approval, and reads back as a permission failure inside a complete review.
//
// It reports a denied tool call as status ERROR while still returning the review the
// model wrote, which is why Classify salvages a body carrying findings.
type Agy struct {
	Model  string
	Effort string
	// Binary defaults to "agy". Overridable so a test can point at a stub.
	Binary string
}

// agyTimeoutMargin keeps agy's own wall clock above the panel's.
const agyTimeoutMargin = 60 * time.Second

// sandboxArgs enable the reviewer's tools behind agy's sandbox boundary.
var sandboxArgs = []string{"--sandbox", "--dangerously-skip-permissions"}

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
	argv := append([]string{a.binary()}, sandboxArgs...)
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
