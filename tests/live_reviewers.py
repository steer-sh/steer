#!/usr/bin/env python3
"""Run one paid reviewer cell through the gate in a throwaway home.

Requires explicit --provider, --model and --effort (use '' for no effort).
Claude Code needs an exported sandbox login; Codex and agy use sandbox.py's
login copies. Results contain actual argv, prompts, replies, timing and ledger
rows. This exercises the native hook command directly, not interactive delivery.
"""

import argparse
import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

from sandbox import GIT_IDENTITY, REPO, Sandbox

BINARIES = {"claude-code": "claude", "codex": "codex", "agy": "agy"}
BASE = '''def average(values):
    """Return the arithmetic mean of a nonempty sequence.

    Each item has equal weight. The divisor is the number of items, including
    a singleton sequence, whose average is its sole item. Empty input raises
    ValueError because it has no arithmetic mean.
    """
    if not values:
        raise ValueError("average requires at least one item")
    return sum(values) / len(values)
'''
DEFECT = BASE.replace("sum(values) / len(values)", "sum(values) / (len(values) - 1)")


def write_json(path, record):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(record, indent=2))
    temporary.replace(path)


def capture(real, trace, argv):
    stdin = sys.stdin.read()
    prompt = argv[argv.index("-p") + 1] if Path(real).name == "agy" else stdin
    safe_argv = list(argv)
    if Path(real).name == "agy":
        safe_argv[safe_argv.index("-p") + 1] = "<prompt in prompt.txt>"
    trace = Path(trace)
    trace.mkdir()
    (trace / "prompt.txt").write_text(prompt)
    record = {"argv": safe_argv, "cwd": os.getcwd(), "stdin_chars": len(stdin),
              "prompt_sha256": hashlib.sha256(prompt.encode()).hexdigest()}
    with (trace / "stdout.txt").open("w") as stdout, (trace / "stderr.txt").open("w") as stderr:
        start = time.monotonic()
        record["started_monotonic"] = start
        write_json(trace / "call.json", record)
        result = subprocess.run([real, *argv], input=stdin, text=True, stdout=stdout, stderr=stderr)
        record.update(seconds=time.monotonic() - start, returncode=result.returncode)
    write_json(trace / "call.json", record)
    sys.stdout.write((trace / "stdout.txt").read_text(errors="replace"))
    sys.stderr.write((trace / "stderr.txt").read_text(errors="replace"))
    return result.returncode


def finish_captures(output):
    calls = []
    for path in sorted(output.glob("call-*/call.json")):
        record = json.loads(path.read_text())
        if "returncode" not in record:
            record.update(returncode=None, termination="capture interrupted; exit status unknown",
                          seconds_upper_bound=time.monotonic() - record["started_monotonic"])
            write_json(path, record)
        calls.append(record)
    return calls


def has_pair(argv, flag, value):
    return any(argv[i:i + 2] == [flag, value] for i in range(len(argv) - 1))


def check_call(call, provider, model, effort):
    argv = call["argv"]
    failures = []
    if not has_pair(argv, "--model", model):
        failures.append("model did not reach the CLI")
    if effort:
        flag, value = ("-c", "model_reasoning_effort=" + effort) if provider == "codex" else ("--effort", effort)
        if not has_pair(argv, flag, value):
            failures.append("effort did not reach the CLI")
    if provider == "claude-code":
        flags = ("-p", "--safe-mode", "--restricted", "--no-session-persistence")
        pairs = (("--output-format", "json"), ("--tools", "Read,Grep,Glob"))
    elif provider == "codex":
        flags = ("exec", "--ignore-user-config", "--ephemeral", "--skip-git-repo-check")
        pairs = (("--sandbox", "read-only"), ("--disable", "hooks"), ("--disable", "apps"))
        if not argv or argv[-1] != "-":
            failures.append("Codex prompt is not on stdin")
    else:
        flags = ("-p", "--print-timeout")
        pairs = (("--output-format", "json"), ("-p", "<prompt in prompt.txt>"))
    if any(f not in argv for f in flags) or any(not has_pair(argv, *p) for p in pairs):
        failures.append("reviewer isolation or prompt argv is incorrect")
    if bool(call["stdin_chars"]) != (provider != "agy"):
        failures.append("prompt used the wrong transport")
    if call.get("returncode") != 0:
        failures.append("reviewer did not complete successfully")
    return failures


def run_cell(args):
    real = shutil.which(BINARIES[args.provider])
    if not real:
        raise ValueError(f"{BINARIES[args.provider]} is missing from PATH")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    root = Path(tempfile.mkdtemp(prefix="steer-live-reviewer-"))
    box = None
    report = {"provider": args.provider, "model": args.model, "effort": args.effort,
              "failures": [], "cost": "not measured; raw CLI replies retained"}
    try:
        box = Sandbox(root, codex=args.provider == "codex", agy=args.provider == "agy")
        env = box.env(claude=args.provider == "claude-code")
        # Keep operator overrides from redirecting the gate's state or changing its panel.
        env = {k: v for k, v in env.items()
               if not k.startswith(("STEER_", "COMMIT_GATE_", "COMMIT_REVIEW_", "GIT_"))}
        env.update(GIT_CONFIG_GLOBAL=str(box.gitconfig), GIT_CONFIG_NOSYSTEM="1", **GIT_IDENTITY)
        bindir = box.root / "bin"
        bindir.mkdir()
        steer = bindir / "steer"
        if args.steer_bin:
            shutil.copy2(args.steer_bin, steer)
        else:
            subprocess.run(["go", "build", "-o", str(steer), "."], cwd=REPO / "cli", check=True)
        env["PATH"] = str(bindir) + os.pathsep + env.get("PATH", "")
        wrapper = bindir / BINARIES[args.provider]
        capture_script = str(Path(__file__).resolve())
        wrapper.write_text("#!/bin/sh\nexec " + " ".join(shlex.quote(s) for s in
                           (sys.executable, capture_script, "--capture", real, str(output))) + ' "$@"\n')
        wrapper.chmod(0o755)
        work = box.root / "work"
        work.mkdir()

        def command(argv, stdin=None):
            result = subprocess.run(argv, cwd=work, env=env, input=stdin, text=True,
                                    capture_output=True, timeout=args.timeout + 90)
            if result.returncode:
                raise ValueError(f"{argv[0]} exited {result.returncode}: {result.stderr[-1500:]}")
            return result.stdout

        templates = box.root / "git-templates"
        templates.mkdir()
        command(["git", "init", "-q", "--template=" + str(templates)])
        marker = box.root / "unexpected-stop"
        oracle = work / "check.sh"
        oracle.write_text("#!/bin/sh\ntouch " + shlex.quote(str(marker)) + "\n")
        oracle.chmod(0o755)
        (work / "average.py").write_text(BASE)
        command(["git", "add", "average.py", "check.sh"])
        command(["git", "commit", "-qm", "Add arithmetic mean"])
        for key, value in (("provider", args.provider), ("model", args.model),
                           ("effort", args.effort), ("runs", "1"), ("timeout", str(args.timeout))):
            command([str(steer), "config", "set", "reviewer." + key, value])
        command([str(steer), "check", "adopt"])
        marker.unlink(missing_ok=True)
        # A Stop marker makes inherited hooks observable without touching operator files.
        stop = {"type": "command", "command": "touch " + shlex.quote(str(marker))}
        (box.home / ".claude" / "settings.json").write_text(json.dumps({"hooks": {"Stop": [{"hooks": [stop]}]}}))
        codexdir = box.home / ".codex"
        codexdir.mkdir(exist_ok=True)
        (codexdir / "hooks.json").write_text(json.dumps({"hooks": {"Stop": [{"hooks": [stop]}]}}))
        command([str(steer), "bin", "install"])
        command([str(steer), "commit-gate", "install", "--harness", "claude-code"])
        settings = json.loads((work / ".claude" / "settings.json").read_text())
        registered = [hook["command"] for entry in settings["hooks"]["PreToolUse"]
                      for hook in entry["hooks"] if "commit-gate review" in hook.get("command", "")]
        if len(registered) != 1:
            raise ValueError("expected one registered native review command")
        report["registered_command"] = registered[0]
        trial = command([str(steer), "commit-gate", "test-reviewer"])
        (output / "trial.txt").write_text(trial)
        (work / "average.py").write_text(DEFECT)
        command(["git", "add", "average.py"])
        payload = {"session_id": "live-reviewer-cell", "cwd": str(work),
                   "hook_event_name": "PreToolUse", "tool_name": "Bash",
                   "tool_input": {"command": "git commit -m 'Change arithmetic mean divisor'"}}
        start = time.monotonic()
        reply = command(["sh", "-c", registered[0]], json.dumps(payload))
        report["gate_seconds"] = time.monotonic() - start
        (output / "hook-reply.json").write_text(reply)
        if json.loads(reply).get("hookSpecificOutput", {}).get("permissionDecision") != "deny":
            report["failures"].append("planted divide-by-zero/off-by-one defect did not block the commit")
        rows = []
        for path in (box.home / ".steer" / "state" / "commit-review").glob("adjudication.*.jsonl"):
            rows += [json.loads(line) for line in path.read_text().splitlines() if line]
        report["ledger"] = rows
        if len(rows) != 1 or rows[0].get("phase") != "deny" or rows[0].get("finding_slots", 0) < 1:
            report["failures"].append("ledger does not contain one review with findings")
        elif any(rows[0].get("instrument_fields", {}).get(k) != v for k, v in
                 (("provider", args.provider), ("model", args.model), ("effort", args.effort), ("runs", 1))):
            report["failures"].append("ledger reviewer identity differs from the selected cell")
        traces = sorted(output.glob("call-*/call.json"))
        if len(traces) != 2:
            report["failures"].append(f"expected trial and live CLI calls, found {len(traces)}")
        for path in traces:
            report["failures"] += check_call(json.loads(path.read_text()), args.provider, args.model, args.effort)
        if traces:
            live = traces[-1].parent
            if "sum(values) / (len(values) - 1)" not in (live / "prompt.txt").read_text():
                report["failures"].append("live prompt did not carry the planted defect")
            response = (live / "stdout.txt").read_text()
            if not ("average.py" in response and re.search(r"zero|singleton|divis|denominator|off.by.one", response, re.I)):
                report["failures"].append("live reply did not identify the planted arithmetic defect")
        if marker.exists():
            report["failures"].append("reviewer ran the sandbox Stop hook")
    except (OSError, ValueError, SystemExit, subprocess.SubprocessError) as error:
        report["failures"].append(str(error))
    finally:
        report["calls"] = finish_captures(output)
        if box:
            report["failures"] += box.leaks()
            report["sessions"] = box.sessions()
            if any(report["sessions"].get(h) for h in ("claude", "codex")):
                report["failures"].append("Claude Code or Codex saved a reviewer session")
        report["passed"] = not report["failures"]
        (output / "result.json").write_text(json.dumps(report, indent=2))
        if args.keep:
            print(f"kept sandbox: {root}")
        else:
            shutil.rmtree(root)
    print(f"{args.provider}: {'PASS' if report['passed'] else 'FAIL'}; {output / 'result.json'}")
    return 0 if report["passed"] else 1


def main():
    if sys.argv[1:2] == ["--capture"]:
        real, output = sys.argv[2:4]
        trace = Path(output) / ("call-" + str(time.time_ns()))
        return capture(real, trace, sys.argv[4:])
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--provider", required=True, choices=BINARIES)
    parser.add_argument("--model", required=True)
    parser.add_argument("--effort", required=True)
    parser.add_argument("--output", required=True, type=Path, help="New directory for the cell's evidence")
    parser.add_argument("--steer-bin", type=Path, help="Candidate binary (default: build working tree)")
    parser.add_argument("--timeout", type=int, default=300)
    parser.add_argument("--keep", action="store_true", help="Keep the sandbox home, including copied login")
    args = parser.parse_args()
    if args.timeout < 1:
        parser.error("--timeout must be positive")
    try:
        return run_cell(args)
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
