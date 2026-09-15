#!/usr/bin/env bash
# Non-blocking tier: everything check.sh must not gate a turn on. Run by hand.
set -euo pipefail

root="$(cd "$(dirname "$0")" && pwd)"

"$root/check.sh"

# Headless skill-generation harness: invokes `claude -p`, costs money, ~55 min.
if [ -x "$root/tests/.venv/bin/python3" ]; then
  "$root/tests/.venv/bin/python3" "$root/tests/run_tests.py" --model "${MODEL:-claude-haiku-4-5-20251001}"
  "$root/tests/.venv/bin/python3" "$root/tests/routing_scenarios.py" --model "${MODEL:-claude-haiku-4-5-20251001}"
else
  echo "tests/.venv missing — see CLAUDE.md for how to create it" >&2
  exit 1
fi

# Report comprehension: regenerates the /steer-report prose over six saved
# scenarios. It has no mechanical oracle — a person reads the output — so it
# writes the reports and never fails the run.
"$root/tests/report_scenarios.py" --model "${MODEL:-claude-haiku-4-5-20251001}" || true
