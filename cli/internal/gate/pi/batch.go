package pi

import "strings"

// Batch is what the bridge read about the assistant message that holds a call.
// Siblings names the other tool calls in that message. Found is false when the
// bridge could not find the message, and the gate then reviews as before.
type Batch struct {
	Found    bool     `json:"found"`
	Siblings []string `json:"siblings"`
}

// readOnlyTools are Pi's built-in tools that change no file, so a commit
// beside them reviews the tree it lands.
var readOnlyTools = map[string]bool{"read": true, "grep": true, "find": true, "ls": true}

// unorderedCommit names why the gate cannot know which tree a commit lands, or
// returns "". Pi runs every tool_call handler of one assistant message before
// it runs any of those calls, then runs them at the same time; a code-mode
// script can run nested calls at the same time too. A sibling that writes can
// therefore change the index after the review and before the commit.
func unorderedCommit(payload Payload) string {
	if payload.ParentToolCallID != "" {
		return "it runs inside another tool's call (code mode), whose other calls can run at the same time"
	}
	if payload.Batch == nil || !payload.Batch.Found {
		return ""
	}
	var writers []string
	for _, name := range payload.Batch.Siblings {
		if !readOnlyTools[name] {
			writers = append(writers, name)
		}
	}
	if len(writers) == 0 {
		return ""
	}
	return "the same message also calls " + strings.Join(writers, ", ") +
		", and Pi runs those calls at the same time as the commit"
}

// unorderedReason is what Pi renders back to the model, so it names the retry.
func unorderedReason(why string) string {
	return "[commit-gate] BLOCKED: this commit cannot be reviewed because " + why +
		". The gate reviews the staged tree before any call in the batch runs, so it " +
		"would review a tree the commit may not land. Let the other calls finish, then " +
		"run the commit alone: one top-level bash call, the only tool call in its message."
}

// unorderedReceipt is the operator's line, carried on the deny's systemMessage.
const unorderedReceipt = "[commit-gate] refused a Pi commit that shares its tool batch " +
	"with a call that can change files. No review ran and no adjudication row was written."
