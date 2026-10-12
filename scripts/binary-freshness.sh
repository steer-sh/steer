#!/usr/bin/env bash
# Fails when a binary committed at HEAD under bin/ was not built from HEAD's CLI source.
# cli/build.sh stamps the short hash of the HEAD it built at through -ldflags; a stamp
# is fresh when that commit's shipped CLI source equals HEAD's.
set -euo pipefail

source_paths=(cli ':(exclude)cli/e2e' ':(exclude)cli/tools' ':(exclude)*_test.go' ':(exclude)*/testdata/*')

newest="$(git log -1 --format=%H -- "${source_paths[@]}")"
if [ -z "$newest" ]; then
  exit 0
fi
stamps=()
while IFS= read -r commit; do
  if git diff --quiet "$commit" HEAD -- "${source_paths[@]}"; then
    stamps+=(-e "${commit:0:7}")
  fi
done < <(echo "$newest"; git rev-list --ancestry-path "$newest..HEAD")

status=0
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
  binary="bin/$target/steer"
  if ! git cat-file -e "HEAD:$binary" 2>/dev/null; then
    echo "binary-freshness: $binary is not committed at HEAD; run cli/build.sh and commit bin/" >&2
    status=1
  elif [ "${#stamps[@]}" -eq 0 ] || ! LC_ALL=C grep -aqF "${stamps[@]}" < <(git cat-file blob "HEAD:$binary"); then
    echo "binary-freshness: $binary at HEAD lacks commit ${newest:0:7}, the newest change to cli/, or a later one with the same CLI source; run cli/build.sh and commit bin/" >&2
    status=1
  fi
done
exit "$status"
