package checks

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// OracleName is the repository's verification oracle at its root.
const OracleName = "check.sh"

// ExecInvocation is the oracle line that runs the managed script checks.
const ExecInvocation = `"$steer" check exec --all`

// GoSuiteInvocation is the oracle line that runs a root Go module's suite
// through the adapter.
const GoSuiteInvocation = `"$steer" check go-test -- ./...`

const scaffoldHead = `#!/usr/bin/env bash
# Verification oracle: a Stop hook runs it at each turn end and blocks the turn
# while it exits non-zero. Authoring guide: steer check guide
set -euo pipefail
cd "$(dirname "$0")"

steer="${STEER_BIN:-$(command -v steer || true)}"
if [ -z "$steer" ]; then
  echo "check.sh: steer was not found; install it or set STEER_BIN to its path" >&2
  exit 1
fi

status=0
`

// Scaffold returns the check.sh that Init writes for a repository without one.
// The Go suite line appears when the repository root holds a go.mod.
func Scaffold(root string) []byte {
	var b bytes.Buffer
	b.WriteString(scaffoldHead)
	fmt.Fprintf(&b, "%s || status=1\n", ExecInvocation)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		fmt.Fprintf(&b, "%s || status=1\n", GoSuiteInvocation)
	}
	b.WriteString("exit \"$status\"\n")
	return b.Bytes()
}

// OraclePath returns the oracle of the repository rooted at root.
func OraclePath(root string) string { return filepath.Join(root, OracleName) }

// WriteScaffold writes the check.sh scaffold when the repository has none. An
// existing check.sh is never changed; created reports whether this call wrote
// one.
func WriteScaffold(root string) (created bool, err error) {
	f, err := os.OpenFile(OraclePath(root), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("write %s: %w", OracleName, err)
	}
	if _, err := f.Write(Scaffold(root)); err != nil {
		f.Close()
		return false, fmt.Errorf("write %s: %w", OracleName, err)
	}
	return true, f.Close()
}

// OracleRunsExec reports whether the repository's check.sh names the managed
// executor. It is a text search, not proof that the line runs.
func OracleRunsExec(root string) bool {
	data, err := os.ReadFile(OraclePath(root))
	return err == nil && bytes.Contains(data, []byte("check exec"))
}
