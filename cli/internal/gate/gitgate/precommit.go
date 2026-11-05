// Package gitgate is the git pre-hook adapter for the commit gate. It answers "which
// tree, which diff" from the index instead of from shell text, which deletes the `cd`,
// `-C` and staging simulation the Claude Code adapter needs.
//
// It is the portable tier, not a replacement for a PreToolUse deny: it blocks with exit 2,
// which `--no-verify` bypasses.
package gitgate

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Result is what Run reports back to the git hook wrapper.
type Result struct {
	// Block is true when the commit must not proceed. The caller exits 2.
	Block bool
	// AgentText is the review material, written to stderr, which is where git shows a
	// pre-commit hook's output.
	AgentText string
	// OperatorText is the one-line receipt.
	OperatorText string
}

// IndexSource names the index git handed this hook: the basename of GIT_INDEX_FILE, or
// "default" when git set none. It is the only in-band signal a pre-hook gets about the
// form of the change, and it rides every row this channel writes.
func IndexSource(env func(string) string) string {
	idx := env("GIT_INDEX_FILE")
	if idx == "" {
		return "default"
	}
	return idx[strings.LastIndexByte(idx, '/')+1:]
}

// Run reviews the staged tree and reports whether the commit may proceed.
func Run(ctx context.Context, cfg gate.Config, reviewer gate.Reviewer, cwd string) Result {
	repo := gate.Toplevel(cwd)
	engine := gate.NewEngine(cfg, reviewer)

	// Chain identity is (repo, HEAD): git hands a hook no session id and no transcript,
	// so the actor the PreToolUse adapter keys on does not exist here.
	head := ""
	if out, ok := gate.Git(repo, 5*time.Second, "rev-parse", "HEAD"); ok {
		head = strings.TrimSpace(out)
	}

	// The author fields are absent by construction under this channel, not lost; `channel`
	// on every row is what lets a reader tell those apart.
	rowExtra := gate.Row{"repo": repo, "client": "git", "harness": "git"}
	if head != "" {
		rowExtra["base_sha"] = head
		gate.Git(repo, 5*time.Second, "update-ref", "refs/commit-review/"+head, head)
	}

	rowExtra["index_source"] = IndexSource(os.Getenv)

	decision := engine.Handle(ctx, gate.Request{
		Repo:      repo,
		Actor:     head,
		Channel:   gate.ChannelPreCommit,
		SessionID: "",
		Spec:      gate.DiffSpec{StagedOnly: true},
		Message:   commitMessageUnavailable,
		// No RerunNote: this adapter blocks nothing the shell already wrote.
		RowExtra: rowExtra,
	})
	return render(decision)
}

// commitMessageUnavailable is what this channel can honestly say about the message: git
// prepares it only after the hook runs, so no source on disk holds this change's text
// yet.
const commitMessageUnavailable = gate.SubjectUnavailable

// render maps a decision onto git's one stderr. A receipt's agent copy already carries
// the operator's sentence, so the operator copy is dropped to print it once.
func render(d gate.Decision) Result {
	operator := d.SystemMessage
	if d.Receipt {
		operator = ""
	}
	if d.LedgerError != nil {
		operator = strings.TrimSpace(operator + " [commit-gate] the adjudication row could NOT be written (" +
			d.LedgerError.Error() + ").")
	}
	switch d.Kind {
	case gate.Deny:
		return Result{Block: true, AgentText: d.Reason, OperatorText: operator}
	case gate.Allow, gate.ContextOnly:
		return Result{AgentText: d.Reason, OperatorText: operator}
	default:
		return Result{OperatorText: operator}
	}
}

func mergeRow(base, extra gate.Row) gate.Row {
	out := gate.Row{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

// Write emits the result to the streams git reads.
func (r Result) Write(stderr io.Writer) {
	if r.AgentText != "" {
		fmt.Fprintln(stderr, r.AgentText)
	}
	if r.OperatorText != "" {
		fmt.Fprintln(stderr, r.OperatorText)
	}
}
