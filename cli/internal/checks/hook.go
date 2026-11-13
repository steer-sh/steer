package checks

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// StopPayload is the part of a Claude Code or Codex Stop payload the runner
// reads. AgentID is set only on a subagent's thread. StopHookActive is nil when
// the payload does not say whether a Stop hook already blocked this turn.
// BackgroundTasks is Claude Code's list of the session's in-flight tasks, kept
// raw so a shape this runner does not know never fails the whole payload.
type StopPayload struct {
	HookEventName   string          `json:"hook_event_name"`
	SessionID       string          `json:"session_id"`
	CWD             string          `json:"cwd"`
	AgentID         string          `json:"agent_id"`
	StopHookActive  *bool           `json:"stop_hook_active,omitempty"`
	BackgroundTasks json.RawMessage `json:"background_tasks,omitempty"`
}

// finishedTaskStatuses are the statuses of a background task that no longer runs.
var finishedTaskStatuses = map[string]bool{
	"completed": true, "failed": true, "killed": true, "stopped": true, "cancelled": true, "canceled": true,
}

// runningSubagents names each subagent task in a Stop payload's background_tasks
// that still runs, by its agent type or, without one, its id.
func runningSubagents(raw json.RawMessage) []string {
	var tasks []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Status    string `json:"status"`
		AgentType string `json:"agent_type"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &tasks) != nil {
		return nil
	}
	var out []string
	for _, task := range tasks {
		if task.Type != "subagent" || task.Status == "" || finishedTaskStatuses[task.Status] {
			continue
		}
		name := task.AgentType
		if name == "" {
			name = task.ID
		}
		if name == "" {
			name = "subagent"
		}
		out = append(out, name)
	}
	return out
}

// stopLocatorKeys are the payload fields that find a block in the harness
// transcript: the file, and the turn as Claude Code (prompt_id) or Codex
// (turn_id) names it.
var stopLocatorKeys = []string{"transcript_path", "prompt_id", "turn_id"}

// stopLocators collects the locator fields raw carries, by key presence: a
// null or non-string value is kept as an empty string.
func stopLocators(raw []byte) map[string]string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	var out map[string]string
	for _, key := range stopLocatorKeys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		var s string
		_ = json.Unmarshal(value, &s)
		out[key] = s
	}
	return out
}

// StopReply is a Stop hook's reply; Claude Code and Codex read the same shape.
// Decision "block" keeps the turn going with Reason as the model's next input.
// SystemMessage is shown to the operator.
type StopReply struct {
	Decision      string `json:"decision,omitempty"`
	Reason        string `json:"reason,omitempty"`
	SystemMessage string `json:"systemMessage,omitempty"`
}

// WriteStopReply writes reply as one JSON line, or nothing when reply is nil.
func WriteStopReply(w io.Writer, reply *StopReply) {
	if reply == nil {
		return
	}
	encoded, err := json.Marshal(reply)
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(encoded))
}

// hookRecordingMargin is the time a Stop hook needs past the run deadline to
// stop check.sh, record the run and save retry state.
const hookRecordingMargin = 30 * time.Second

// RegisteredHookTimeout is the harness timeout, in seconds, a Stop runner
// registration carries. A harness that stops the hook first leaves no run record.
func RegisteredHookTimeout() int {
	return int((DefaultExecTimeout + hookRecordingMargin) / time.Second)
}

// StopHookOptions carries what the hook process supplies beyond the payload.
type StopHookOptions struct {
	Timeout  time.Duration
	SteerBin string
}

// StopHook answers one Stop payload from harness. It acts only on the root
// thread's Stop event; any other event is ignored. A root Stop with a subagent
// still running is not checked: the tree holds that subagent's unfinished work.
// The reply is nil when the hook has nothing to say.
func StopHook(harness string, raw []byte, opts StopHookOptions) (StopDecision, *StopReply) {
	var p StopPayload
	var d StopDecision
	switch {
	case json.Unmarshal(raw, &p) != nil:
		d = StopDecision{Disposition: DispositionBadContext,
			Message: stopMessagePrefix + "the Stop payload is not JSON; check.sh did not run."}
	case p.HookEventName != "Stop" || p.AgentID != "":
		d = StopDecision{Disposition: DispositionIgnored}
	default:
		d = Stop(StopRequest{Harness: harness, Session: p.SessionID, CWD: p.CWD, Timeout: opts.Timeout, SteerBin: opts.SteerBin,
			Locators: stopLocators(raw), StopHookActive: p.StopHookActive, RunningSubagents: runningSubagents(p.BackgroundTasks)})
	}
	return d, d.Reply()
}

// Reply renders the decision for the harness, or nil when it says nothing.
func (d StopDecision) Reply() *StopReply {
	r := StopReply{SystemMessage: d.Message}
	if d.Disposition == DispositionBlock {
		r.Decision, r.Reason = "block", d.Reason
	}
	if r == (StopReply{}) {
		return nil
	}
	return &r
}
