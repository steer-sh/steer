// Package reviewers holds the reviewer providers the commit gate can drive: agy
// (default), Codex, and `claude -p`. Each turns one prompt into one raw run; the
// engine classifies and pools them.
package reviewers

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
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
	setProcessGroup(cmd)
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
				Detail:  "rc=" + strconv.Itoa(exitErr.ExitCode()) + " " + strings.TrimSpace(stderr),
			}, true
		}
		return stdout, stderr, gate.RawRun{
			Outcome: gate.OutcomeSpawn,
			Detail:  err.Error(),
		}, true
	}
}
