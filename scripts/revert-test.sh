#!/usr/bin/env bash
# Bash entry point for the revert-test check. Steer runs a script binding under
# bash, so the Python detector is invoked from here.
set -euo pipefail
exec python3 "$(dirname "$0")/revert-test.py" "$@"
