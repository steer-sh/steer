package reviewers

import (
	"context"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Codex reviews through the Codex CLI in non-interactive mode. It prints the reply on
// stdout as plain text rather than JSON, so a completed process is a SUCCESS and a
// non-zero exit is already classified by runProcess.
type Codex struct {
	Model  string
	Binary string
}

func NewCodex(model string) *Codex { return &Codex{Model: model} }

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
	argv := []string{c.binary(), "exec", "--skip-git-repo-check"}
	if c.Model != "" {
		argv = append(argv, "--model", c.Model)
	}
	argv = append(argv, "-")
	stdout, stderr, raw, failed := runProcess(ctx, cwd, argv, prompt)
	if failed {
		return raw
	}
	return gate.RawRun{
		Response: strings.TrimSpace(stdout),
		Status:   "SUCCESS",
		Error:    strings.TrimSpace(stderr),
	}
}
