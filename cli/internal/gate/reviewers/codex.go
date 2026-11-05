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
	argv := []string{c.binary(), "exec", "--skip-git-repo-check", "--ephemeral", "--ignore-user-config",
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
	if failed {
		return raw
	}
	return gate.RawRun{
		Response: strings.TrimSpace(stdout),
		Status:   "SUCCESS",
		Error:    stderrTail(stderr),
	}
}
