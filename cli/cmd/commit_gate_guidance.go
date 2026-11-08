package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/cc"
	"github.com/steer-sh/steer-cli/internal/harness"
)

// claudeCommitGuidance returns the commit-shape guidance for a Claude Code session
// whose commit gate is armed at either scope, and "" anywhere else.
func claudeCommitGuidance() string {
	if harness.Detect() != harness.ClaudeCode {
		return ""
	}
	if !claude.CommitGateHookArmed(claude.GlobalScope).Registered &&
		!claude.CommitGateHookArmed(claude.ProjectScope).Registered {
		return ""
	}
	return cc.CommitShapeGuidance
}

// hookContextReply is a SessionStart or SubagentStart reply: operator lines go to
// systemMessage and the model's context, guidance to the context alone. ok is false
// when there is nothing to say.
func hookContextReply(event string, lines []string, guidance string) (reply []byte, ok bool) {
	system := strings.Join(lines, "\n")
	context := system
	if guidance != "" {
		if context != "" {
			context += "\n\n"
		}
		context += guidance
	}
	if context == "" {
		return nil, false
	}
	out, err := json.Marshal(sessionStartOutput{
		SystemMessage: system,
		HookSpecificOutput: sessionStartHookOutput{
			HookEventName:     event,
			AdditionalContext: context,
		},
	})
	return out, err == nil
}

var commitGateSubagentStartCmd = &cobra.Command{
	Use:    "subagent-start",
	Hidden: true,
	Short:  "SubagentStart hook: give a Claude Code subagent the commit form the gate accepts",
	Long: `subagent-start is the plugin's SubagentStart entry point. In a Claude Code
session whose commit gate is armed it adds the commit-shape guidance to the
subagent's context, which does not inherit the session's SessionStart context.
Anywhere else it prints nothing. It always exits 0.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if out, ok := hookContextReply("SubagentStart", nil, claudeCommitGuidance()); ok {
			fmt.Println(string(out))
		}
		return nil
	},
}
