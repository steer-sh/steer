package reviewers

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate"
)

// ClaudeCode reviews through `claude -p --output-format json`. The reply carries an
// is_error flag beside the result text, which maps onto the same salvage rule as
// agy's ERROR status.
type ClaudeCode struct {
	Model  string
	Effort string
	Binary string
	// UserSettings defaults to the global Claude Code settings.json. Overridable so a
	// test reads no operator file.
	UserSettings string
}

func NewClaudeCode(model, effort string) *ClaudeCode {
	return &ClaudeCode{Model: model, Effort: effort}
}

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

// authSettingsKeys are the user settings a review needs to sign in. --restricted skips
// the settings files that carry them, and --settings still applies under it.
var authSettingsKeys = []string{"env", "apiKeyHelper"}

// authSettings writes the user settings' sign-in keys to a temporary file readable
// only by the user, and returns its path. It returns "" when the settings carry none
// or cannot be read; the caller removes the file.
func (c *ClaudeCode) authSettings() string {
	path := c.UserSettings
	if path == "" {
		var err error
		if path, err = claude.GetSettingsFile(claude.GlobalScope); err != nil {
			return ""
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return ""
	}
	auth := map[string]json.RawMessage{}
	for _, key := range authSettingsKeys {
		if v, ok := all[key]; ok {
			auth[key] = v
		}
	}
	if len(auth) == 0 {
		return ""
	}
	body, err := json.Marshal(auth)
	if err != nil {
		return ""
	}
	f, err := os.CreateTemp("", "steer-review-settings-*.json")
	if err != nil {
		return ""
	}
	_, werr := f.Write(body)
	if cerr := f.Close(); werr != nil || cerr != nil {
		os.Remove(f.Name())
		return ""
	}
	return f.Name()
}

type claudeReply struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
}

// exitDetail puts the result text of a failed run's JSON reply, such as a missing
// login, right after the exit code, ahead of the stderr tail that readers cut first.
// It returns detail unchanged when stdout holds no reply text.
func exitDetail(detail, stdout string) string {
	var reply claudeReply
	if json.Unmarshal([]byte(stdout), &reply) != nil {
		return detail
	}
	result := stderrTail(reply.Result)
	if result == "" {
		return detail
	}
	code, rest, _ := strings.Cut(detail, " ")
	return strings.TrimSpace(code + " " + result + " " + rest)
}

func (c *ClaudeCode) Review(ctx context.Context, prompt, cwd string) gate.RawRun {
	// --no-session-persistence keeps the run out of the user's session history: its
	// prompt is a diff, not anything the user said. --safe-mode drops the operator's
	// hooks, plugins, CLAUDE.md and output style, and --tools leaves only file reads.
	// --restricted ignores the operator's and the repository's settings files, so no
	// permission rule there can widen those reads past the working directory. The
	// sign-in keys it would drop come back through --settings, as a file so that a key
	// in env never appears in the process list.
	argv := []string{c.binary(), "-p", "--output-format", "json", "--no-session-persistence",
		"--safe-mode", "--tools", "Read,Grep,Glob", "--restricted"}
	if path := c.authSettings(); path != "" {
		defer os.Remove(path)
		argv = append(argv, "--settings", path)
	}
	if c.Model != "" {
		argv = append(argv, "--model", c.Model)
	}
	if c.Effort != "" {
		argv = append(argv, "--effort", c.Effort)
	}
	stdout, stderr, raw, failed := runProcess(ctx, cwd, argv, prompt)
	if failed {
		if raw.Outcome == gate.OutcomeExit {
			raw.Detail = exitDetail(raw.Detail, stdout)
		}
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
