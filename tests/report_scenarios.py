#!/usr/bin/env python3
"""Regenerate the /steer-report prose over seven saved scenarios.

The skill-generation harness (skill_test.py) generates guards and checks them
against fixtures. This runner answers a different question, and it has no
mechanical oracle: does the report a person reads say what happened, what a
change would do, and what decision is needed — without a glossary?

It feeds each saved scenario to the working tree's report skill headlessly and
stores the output beside the others under tests/scenarios/out/. Read them side by side with
somebody who has never used Steer, record where they were misled, and revise the
copy. The runner never passes or fails; it only regenerates.

Usage:
    python3 tests/report_scenarios.py                       # all seven
    python3 tests/report_scenarios.py --scenario remove-a-guard
    python3 tests/report_scenarios.py --model claude-opus-5
    python3 tests/report_scenarios.py --list
"""

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent / "scenarios"))
import scenarios  # noqa: E402
from sandbox import Sandbox  # noqa: E402

DEFAULT_MODEL = "claude-haiku-4-5-20251001"
REPO = Path(__file__).resolve().parent.parent
SKILL_SRC = REPO / "skills" / "steer-report"
OUT_DIR = Path(__file__).parent / "scenarios" / "out"

# The scenario is a saved snapshot, so the skill must read the files rather than
# this machine's own state. Steps 2 and 3 mine live corpora and are skipped: the
# question here is how the audit and the proposal READ, not what they find.
PROMPT = """/steer-report

This is a saved scenario, not this machine. Run no steer command at all.
Read {status} as the output of `steer status --json`, {score} as the output of
`steer fix score --json`, and {queue} as the refreshed queue. Skip Step 0, Step 2
and Step 3.

Produce only the prose a user reads: the audit of the records in the snapshot,
and the proposals for the candidates in the queue. End each proposal with the
question the skill asks, but wait for no answer: install nothing and write no
files. Do not emit JSON — this is the report itself.

{request}
"""


def skill_reads(events: list[dict]) -> list[str]:
    """The skill files a run opened beside SKILL.md, in the order it opened them."""
    names = [f.name for f in SKILL_SRC.iterdir() if f.name != "SKILL.md"]
    seen = []
    for ev in events:
        if ev.get("type") != "assistant":
            continue
        for part in ev.get("message", {}).get("content", []):
            if part.get("type") != "tool_use":
                continue
            text = json.dumps(part.get("input", {}))
            for n in names:
                if n in text and n not in seen:
                    seen.append(n)
    return seen


def run_scenario(name: str, model: str) -> tuple[str, float, list[str]]:
    """Generate one report. Returns (report text, cost in USD, skill files read)."""
    with tempfile.TemporaryDirectory(prefix=f"steer-report-{name}-") as tmp:
        box = Sandbox(tmp)
        work = box.root / "work"
        work.mkdir()
        status, score, queue = scenarios.write(name, str(work))
        # The run reads the WORKING TREE's skill, not whichever copy is
        # installed on this machine: the prose under test is the file just
        # edited. It is copied in as a project skill of the scenario
        # directory, which stays outside the repository so a run that writes
        # anything writes it into the temp directory.
        shutil.copytree(SKILL_SRC, work / ".claude" / "skills" / "steer-report")
        cmd = [
            "claude", "-p", PROMPT.format(
                status=status, score=score, queue=queue,
                request=("I have decided to remove the no-bare-python guard. Explain "
                         "what removal changes and how it differs from stopping tracking."
                         if name == "remove-a-guard" else "")),
            "--output-format", "stream-json", "--verbose",
            "--model", model,
            "--no-session-persistence",
            "--dangerously-skip-permissions",
        ]
        result = subprocess.run(cmd, capture_output=True, text=True,
                                cwd=work, env=box.env())
        for leak in box.leaks():
            print(f"  LEAK: {leak}", file=sys.stderr)
    if result.returncode != 0:
        print(f"  ERROR: claude exited {result.returncode}", file=sys.stderr)
        print(f"  stderr: {result.stderr[:500]}", file=sys.stderr)
        return "", 0.0, []
    events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    final = next((e for e in reversed(events) if e.get("type") == "result"), {})
    return final.get("result", ""), final.get("total_cost_usd", 0.0), skill_reads(events)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--scenario", "-s", help="Run one scenario by name")
    ap.add_argument("--model", "-m", default=DEFAULT_MODEL)
    ap.add_argument("--list", "-l", action="store_true",
                    help="List the scenario names and exit")
    args = ap.parse_args()

    if args.list:
        for name in scenarios.SCENARIOS:
            print(name)
        return 0

    names = [args.scenario] if args.scenario else list(scenarios.SCENARIOS)
    for name in names:
        if name not in scenarios.SCENARIOS:
            print(f"unknown scenario {name!r}", file=sys.stderr)
            return 2

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    total = 0.0
    started = time.time()
    for name in names:
        print(f"\n=== {name} ===")
        report, cost, reads = run_scenario(name, args.model)
        total += cost
        if not report:
            print("  no report generated")
            continue
        dest = OUT_DIR / f"{name}.md"
        opened = ", ".join(reads) or "none"
        dest.write_text(f"<!-- {name} · {args.model} · skill files read: {opened} -->\n\n"
                        f"{report}\n")
        print(f"  {len(report)} chars → {dest}  (${cost:.4f})  skill files read: {opened}")

    print(f"\n{len(names)} scenario(s), ${total:.4f}, "
          f"{time.time() - started:.0f}s. Read them side by side.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
