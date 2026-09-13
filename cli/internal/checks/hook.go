package checks

import (
	"encoding/json"
	"time"
)

// StopPayload is the part of a Claude Code or Codex Stop payload the runner
// reads. AgentID is set only on a subagent's thread.
type StopPayload struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	AgentID       string `json:"agent_id"`
}

// StopReply is a Stop hook's reply; Claude Code and Codex read the same shape.
// Decision "block" keeps the turn going with Reason as the model's next input.
// SystemMessage is shown to the operator.
type StopReply struct {
	Decision      string `json:"decision,omitempty"`
	Reason        string `json:"reason,omitempty"`
	SystemMessage string `json:"systemMessage,omitempty"`
}

// StopHookOptions carries what the hook process supplies beyond the payload.
type StopHookOptions struct {
	Timeout  time.Duration
	SteerBin string
}

// StopHook answers one Stop payload from harness. It acts only on the root
// thread's Stop event; any other event is ignored. The reply is nil when the
// hook has nothing to say.
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
		d = Stop(StopRequest{Harness: harness, Session: p.SessionID, CWD: p.CWD, Timeout: opts.Timeout, SteerBin: opts.SteerBin})
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
