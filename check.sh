#!/usr/bin/env bash
# The repo's verification oracle. Run by the turn-end Stop gate; exit 0 means the
# work is verified. Slow, networked, or model-invoking checks belong in check-full.sh.
# Authoring guide for the managed checks: steer check guide
set -euo pipefail

root="$(cd "$(dirname "$0")" && pwd)"

steer="${STEER_BIN:-$(command -v steer || true)}"
if [ -z "$steer" ]; then
  echo "check.sh: steer was not found; install it or set STEER_BIN to its path" >&2
  exit 1
fi

cd "$root/cli"

unformatted="$(gofmt -l . ../scripts)"
if [ -n "$unformatted" ]; then
  echo "gofmt: needs formatting:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go build ./...
go vet ./...
# No Windows binary ships, so this is the only compile of the Windows build-tag files.
GOOS=windows GOARCH=amd64 go vet ./...
go test ./...
# The built binary end to end, in throwaway homes with stub reviewer CLIs.
go test -tags e2e ./e2e/...

cd "$root"
status=0
"$steer" check exec --all || status=1
exit "$status"
