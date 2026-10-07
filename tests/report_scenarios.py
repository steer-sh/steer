#!/usr/bin/env python3
"""Regenerate the /steer-report prose over saved scenarios.

The skill-generation harness (skill_test.py) generates guards and checks them
against fixtures. This runner answers a different question, and it has no
mechanical oracle: does the report a person reads say what happened, what a
change would do, and what decision is needed — without a glossary?

It feeds each saved scenario to the working tree's report skill headlessly and
stores the output beside the others under tests/scenarios/out/. Read them side by side with
somebody who has never used Steer, record where they were misled, and revise the
copy. The runner never passes or fails; it only regenerates. It records each
report's word count in the report's first line and in its closing summary.

Usage:
    python3 tests/report_scenarios.py                       # all scenarios
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

DEFAULT_MODEL = "sonnet"
REPO = Path(__file__).resolve().parent.parent
SKILL_SRC = REPO / "skills" / "steer-report"
ROUTING_SRC = REPO / "skills" / "steer" / "prompts" / "routing.md"
OUT_DIR = Path(__file__).parent / "scenarios" / "out"

# The scenario is a saved snapshot, so the skill must read the files rather than
# this machine's own state. Step 2 mines live corpora and is skipped: the queue's
# candidates stand in for its output, so Step 3 qualifies them and Step 4 routes
# them. The question here is how the audit and the proposal READ, not what the
# mining finds.
PROMPT = """Read {skill} with the Read tool and follow its reporting instructions.
{extra_reads}

This is a saved scenario, not this machine. Run no steer command at all.
Read {status} as the output of `steer status --json`, {score} as the output of
`steer fix score --json`, and {queue} as the refreshed queue. Skip Step 0 and
Step 2. The queue's candidates are the evidence Step 3 qualifies; mine nothing.

Produce only the prose a user reads: the audit of the records in the snapshot,
and recommendations for the candidates in the queue or supporting evidence.
Ask only for decisions the skill needs, and wait for no answer: install nothing
and write no files; the reader knows this, so do not report it. Treat unavailable
evidence as unavailable; invent no audit results. Do not emit JSON — this is the
report itself.

{request}
"""


def skill_reads(events: list[dict], skill_dirs: set[Path]) -> list[str]:
    """Skill files under skill_dirs with successful Read results in the recorded
    tool stream."""
    requested = {}
    seen = []
    for ev in events:
        content = ev.get("message", {}).get("content", [])
        if not isinstance(content, list):
            continue
        for part in content:
            if not isinstance(part, dict):
                continue
            if part.get("type") == "tool_use" and part.get("name") == "Read":
                path = Path(part.get("input", {}).get("file_path", ""))
                if path.parent in skill_dirs:
                    requested[part["id"]] = path.name
            if part.get("type") == "tool_result" and not part.get("is_error"):
                name = requested.get(part.get("tool_use_id"))
                if name and name not in seen:
                    seen.append(name)
    return seen


def run_scenario(name: str, model: str) -> tuple[str, float, list[str]]:
    """Generate one report. Returns (report text, cost in USD, skill files read)."""
    with tempfile.TemporaryDirectory(prefix=f"steer-report-{name}-") as tmp:
        box = Sandbox(tmp)
        work = box.root / "work"
        work.mkdir()
        status, score, queue = scenarios.write(name, str(work))
        supporting = scenarios.supporting_evidence(name)
        request = ""
        if supporting is not None:
            evidence_path = work / "supporting-evidence.json"
            evidence_path.write_text(json.dumps(supporting, indent=2) + "\n")
            request = (f"Read {evidence_path} for saved audit output and inspected facts. "
                       "Use these as evidence, without running commands or recording judgments.")
        if name == "remove-a-guard":
            request = ("I have decided to remove the no-bare-python guard. Explain "
                       "what removal changes and how it differs from stopping tracking.")
        # The run reads the WORKING TREE's skill, not whichever copy is
        # installed on this machine: the prose under test is the file just
        # edited. It is copied in as a project skill of the scenario
        # directory, which stays outside the repository so a run that writes
        # anything writes it into the temp directory.
        skill_dir = work / ".claude" / "skills" / "steer-report"
        shutil.copytree(SKILL_SRC, skill_dir)
        # Step 4 reads the routing guide from the sibling steer skill.
        routing = work / ".claude" / "skills" / "steer" / "prompts" / "routing.md"
        routing.parent.mkdir(parents=True)
        shutil.copy2(ROUTING_SRC, routing)
        snapshot = json.loads(status.read_text())
        telemetry = snapshot.get("telemetry", {})
        needs_audit = bool(snapshot.get("interventions") or snapshot.get("hooks")
                           or telemetry.get("audit_firings", 0)
                           or telemetry.get("check_blocks", 0))
        needs_routing = bool(json.loads(queue.read_text()).get("candidates"))
        extra_reads = []
        if needs_audit:
            extra_reads.append(f"Read {skill_dir / 'audit.md'} with the Read tool too; "
                               "this snapshot requires the audit instructions.")
        if needs_routing:
            extra_reads.append(f"Read {routing} with the Read tool as the routing guide "
                               "Step 4 names; the queue holds candidates to route.")
        cmd = [
            "claude", "-p", PROMPT.format(
                skill=skill_dir / "SKILL.md",
                extra_reads="\n".join(extra_reads) or
                "Read supporting skill files only if the skill requires them.",
                status=status, score=score, queue=queue,
                request=request),
            "--output-format", "stream-json", "--verbose",
            "--model", model,
            "--no-session-persistence",
            "--dangerously-skip-permissions",
        ]
        result = subprocess.run(cmd, capture_output=True, text=True,
                                cwd=work, env=box.env())
        leaks = box.leaks()
        for leak in leaks:
            print(f"  LEAK: {leak}", file=sys.stderr)
    if result.returncode != 0:
        print(f"  ERROR: claude exited {result.returncode}", file=sys.stderr)
        print(f"  stderr: {result.stderr[:500]}", file=sys.stderr)
        return "", 0.0, []
    events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    final = next((e for e in reversed(events) if e.get("type") == "result"), {})
    reads = skill_reads(events, {skill_dir, routing.parent})
    required = {"SKILL.md"}
    if needs_audit:
        required.add("audit.md")
    if needs_routing:
        required.add("routing.md")
    missing = required - set(reads)
    cost = final.get("total_cost_usd", 0.0)
    if missing or leaks or final.get("is_error"):
        print(f"  INVALID RUN: missing skill reads={sorted(missing)}, "
              f"leaks={len(leaks)}, model error={bool(final.get('is_error'))}", file=sys.stderr)
        return "", cost, reads
    return final.get("result", ""), cost, reads


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
    failed = 0
    words: dict[str, int] = {}
    started = time.time()
    for name in names:
        print(f"\n=== {name} ===")
        report, cost, reads = run_scenario(name, args.model)
        total += cost
        if not report:
            failed += 1
            print("  no report generated")
            continue
        dest = OUT_DIR / f"{name}.md"
        opened = ", ".join(reads) or "none"
        words[name] = len(report.split())
        dest.write_text(f"<!-- {name} · {args.model} · {words[name]} words · "
                        f"skill files read: {opened} -->\n\n{report}\n")
        print(f"  {words[name]} words → {dest}  (${cost:.4f})  skill files read: {opened}")

    if words:
        print("\nWords per report:")
        for name, count in words.items():
            print(f"  {count:5d}  {name}")
    print(f"\n{len(names)} scenario(s), ${total:.4f}, "
          f"{time.time() - started:.0f}s. Read them side by side.")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
