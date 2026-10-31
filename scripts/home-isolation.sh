#!/usr/bin/env bash
# Fails when a Go test file sets HOME more often than it sets USERPROFILE.
# os.UserHomeDir reads USERPROFILE on Windows, so such a test reads the real home
# there. Use testhome.Set; a line that must set HOME alone carries "home-only".
set -euo pipefail

status=0
while IFS= read -r -d '' f; do
  home=$(grep 'Setenv("HOME"' "$f" | grep -vc 'home-only' || true)
  profile=$(grep -c 'Setenv("USERPROFILE"' "$f" || true)
  if [ "$home" -gt "$profile" ]; then
    echo "home-isolation: $f sets HOME $home time(s) but USERPROFILE $profile time(s); use testhome.Set" >&2
    grep -n 'Setenv("HOME"' "$f" | grep -v 'home-only' >&2 || true
    status=1
  fi
done < <(find . -name '*_test.go' -not -path './.git/*' -print0)
exit "$status"
