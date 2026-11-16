// Package reviewers holds the reviewer providers the commit gate can drive: agy,
// Codex, `claude -p` (default), and an OpenAI-compatible endpoint. Each turns one
// prompt into one raw run; the engine classifies and pools them.
package reviewers

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/procgroup"
)

// runProcess executes argv with cwd set to the repo and returns stdout, stderr and a
// terminal outcome when the process itself failed.
//
// The reviewer runs in its OWN process group and the timeout signals the group, not the
// child: the stdlib default kills only the immediate child PID, and a reviewer CLI spawns
// children that would outlive it holding a model call open.
func runProcess(ctx context.Context, cwd string, argv []string, stdin string) (stdout, stderr string, raw gate.RawRun, failed bool) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	procgroup.Set(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout, stderr = out.String(), errBuf.String()
	switch {
	case err == nil:
		return stdout, stderr, gate.RawRun{}, false
	case ctx.Err() != nil:
		return stdout, stderr, gate.RawRun{
			Outcome: gate.OutcomeTimeout,
			Detail:  "no response before the deadline",
		}, true
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout, stderr, gate.RawRun{
				Outcome: gate.OutcomeExit,
				Detail:  "rc=" + strconv.Itoa(exitErr.ExitCode()) + " " + stderrTail(stderr),
			}, true
		}
		return stdout, stderr, gate.RawRun{
			Outcome: gate.OutcomeSpawn,
			Detail:  err.Error(),
		}, true
	}
}

// stderrTailChars bounds, in bytes, the stderr a run's detail keeps: inside the
// ledger's byte limit on a detail, with room for the exit code.
const stderrTailChars = 240

// stderrTail keeps the end of a reviewer's stderr. A CLI writes its banner and the
// echoed prompt first and its error last, so a head cut drops the cause.
func stderrTail(stderr string) string {
	text := strings.TrimSpace(stderr)
	if len(text) <= stderrTailChars {
		return text
	}
	start := len(text) - stderrTailChars
	tail := text[start:]
	if text[start-1] != '\n' {
		if i := strings.IndexByte(tail, '\n'); i >= 0 && i < len(tail)-1 {
			tail = tail[i+1:]
		}
	}
	for tail != "" && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return "…" + strings.TrimSpace(tail)
}
