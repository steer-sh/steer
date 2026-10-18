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
# The shipped binaries as build.sh writes them, into a scratch directory.
built="$(mktemp -d)"
trap 'rm -rf "$built"' EXIT
VERSION=check-build BIN_DIR="$built" cli/build.sh >/dev/null
python3 scripts/check-binaries.py --bin-dir "$built" --version check-build \
  --commit "$(git rev-parse --short=7 HEAD)"
python3 tests/test_check_binaries.py
python3 scripts/third-party-licenses.py --bin-dir "$built"
python3 tests/test_third_party_licenses.py
# Release preparation tools use fixture repositories and fake reviewer replies.
python3 tests/test_release_tools.py
python3 tests/test_replay_reviewers.py
python3 tests/test_ci_base.py
status=0
"$steer" check exec --all || status=1
exit "$status"
