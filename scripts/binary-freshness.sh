#!/usr/bin/env bash
# Fails when a committed binary under bin/ does not carry the short hash of the newest
# commit that changes the CLI's shipped source. cli/build.sh stamps that hash through
# -ldflags, so a binary without it was built from an older tree.
set -euo pipefail

newest="$(git log -1 --format=%H -- cli \
  ':(exclude)cli/e2e' ':(exclude)*_test.go' ':(exclude)*/testdata/*' ':(exclude)cli/build.sh')"
if [ -z "$newest" ]; then
  exit 0
fi
short="${newest:0:7}"

status=0
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
  binary="bin/$target/steer"
  if [ ! -f "$binary" ]; then
    echo "binary-freshness: $binary is missing; run cli/build.sh" >&2
    status=1
  elif ! LC_ALL=C grep -aqF "$short" "$binary"; then
    echo "binary-freshness: $binary lacks commit $short, the newest change to cli/; run cli/build.sh and commit bin/" >&2
    status=1
  fi
done
exit "$status"
