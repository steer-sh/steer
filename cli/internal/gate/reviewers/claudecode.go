package reviewers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// ClaudeCode reviews through `claude -p --output-format json`. The reply carries an
// is_error flag beside the result text, which maps onto the same salvage rule as
// agy's ERROR status.
type ClaudeCode struct {
	Model  string
	Binary string
}

func NewClaudeCode(model string) *ClaudeCode { return &ClaudeCode{Model: model} }

func (c *ClaudeCode) Name() string { return "claude-code" }

// ModelLabel is the label the instrument records; an unset model means the CLI's own default.
func (c *ClaudeCode) ModelLabel() string {
	if c.Model != "" {
		return c.Model
	}
	return "claude-code-default"
}

func (c *ClaudeCode) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "claude"
}

type claudeReply struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
}

func (c *ClaudeCode) Review(ctx context.Context, prompt, cwd string) gate.RawRun {
	argv := []string{c.binary(), "-p", "--output-format", "json"}
	if c.Model != "" {
		argv = append(argv, "--model", c.Model)
	}
	stdout, stderr, raw, failed := runProcess(ctx, cwd, argv, prompt)
	if failed {
		return raw
	}
	var reply claudeReply
	if err := json.Unmarshal([]byte(stdout), &reply); err != nil {
		detail := strings.TrimSpace(stdout)
		if detail == "" {
			detail = strings.TrimSpace(stderr)
		}
		return gate.RawRun{Outcome: gate.OutcomeUnparseable, Detail: detail}
	}
	status := "SUCCESS"
	if reply.IsError {
		status = "ERROR"
	}
	return gate.RawRun{Response: reply.Result, Status: status, Error: reply.Subtype}
}
