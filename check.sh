#!/usr/bin/env bash
# The repo's verification oracle. Run by the turn-end Stop gate; exit 0 means the
# work is verified. Slow, networked, or model-invoking checks belong in check-full.sh.
set -euo pipefail

cd "$(dirname "$0")/cli"

unformatted="$(gofmt -l . ../scripts)"
if [ -n "$unformatted" ]; then
  echo "gofmt: needs formatting:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go build ./...
go vet ./...
go test ./...

# Each Go test added since the upstream branch must fail under some variant of the change.
python3 ../scripts/revert-test.py
