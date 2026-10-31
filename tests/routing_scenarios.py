#!/usr/bin/env python3
"""Run /steer's headless routing scenarios and check each decision.

Each run gets a fresh git repository holding the scenario's files and a
sandbox home (tests/sandbox.py) shared by Claude Code and the steer CLI the
skill calls, which runs through a wrapper named by PLUGIN_ROOT and first on
PATH. A run fails when it changed a real Steer or harness file or wrote a
firing to the real audit log.

Usage:
    tests/.venv/bin/python3 tests/routing_scenarios.py --model <model>
    tests/.venv/bin/python3 tests/routing_scenarios.py --model <model> --scenario pi-unsupported --repeat 1
"""

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml

from sandbox import Sandbox

ROOT = Path(__file__).resolve().parent.parent
SCENARIOS = ROOT / "tests" / "test_data" / "routing.yaml"
STEER = ROOT / "scripts" / "steer"
FIELDS = ("tier", "action", "supported")


def git(cwd, *args):
    subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True)


def build_repo(workdir, files):
    git(workdir, "init", "-q")
    git(workdir, "config", "user.email", "routing@test.invalid")
    git(workdir, "config", "user.name", "routing test")
    for rel, content in (files or {}).items():
        path = workdir / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        if rel.endswith(".sh"):
            path.chmod(0o755)
    (workdir / "README.md").write_text("routing scenario\n")
    git(workdir, "add", "-A")
    git(workdir, "commit", "-q", "-m", "fixture")


def build_wrapper(scratch, home):
    """Return (plugin root, bin dir) for a wrapper that runs the CLI in home."""
    script = "#!/bin/sh\nHOME=%s exec %s \"$@\"\n" % (home, STEER)
    plugin = scratch / "plugin" / "scripts"
    bindir = scratch / "bin"
    for directory in (plugin, bindir):
        directory.mkdir(parents=True)
        wrapper = directory / "steer"
        wrapper.write_text(script)
        wrapper.chmod(0o755)
    return scratch / "plugin", bindir


def run_skill(scenario, workdir, box, plugin, bindir, model):
    flags = "--headless --" + scenario["mode"]
    if scenario.get("harness"):
        flags += " --harness " + scenario["harness"]
    env = box.env()
    env["PLUGIN_ROOT"] = str(plugin)
    env["PATH"] = "%s:%s" % (bindir, env.get("PATH", ""))
    result = subprocess.run(
        ["claude", "-p", "/steer %s %s" % (flags, scenario["rule"]),
         "--output-format", "json", "--model", model, *box.claude_args(),
         "--no-session-persistence", "--dangerously-skip-permissions"],
        cwd=workdir, env=env, capture_output=True, text=True)
    if result.returncode != 0:
        return None, "claude exited %d: %s" % (result.returncode, result.stderr[:300])
    try:
        outer = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None, "claude output is not JSON: %s" % result.stdout[:300]
    raw = outer.get("result", "")
    if "```json" in raw:
        start = raw.index("```json") + 7
        raw = raw[start:raw.index("```", start)]
    try:
        return json.loads(raw.strip()), ""
    except json.JSONDecodeError:
        return None, "skill result is not JSON: %s" % outer.get("result", "")[:300]


def run_once(scenario, model):
    scratch = Path(tempfile.mkdtemp(prefix="steer-route-"))
    try:
        box = Sandbox(scratch)
        workdir, home = scratch / "repo", box.home
        workdir.mkdir()
        build_repo(workdir, scenario.get("files"))
        (home / ".claude").mkdir(parents=True, exist_ok=True)
        plugin, bindir = build_wrapper(scratch, home)

        result, error = run_skill(scenario, workdir, box, plugin, bindir, model)

        problems = box.leaks()
        if result is None:
            return problems + [error], None
        for field in FIELDS:
            want = scenario["expect"].get(field)
            if want is not None and result.get(field) != want:
                problems.append("%s: want %r, got %r" % (field, want, result.get(field)))
        return problems, result
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--model", "-m", required=True, help="Claude model to run the skill")
    parser.add_argument("--scenario", "-s", help="Run only this scenario")
    parser.add_argument("--repeat", "-r", type=int, default=3,
                        help="Runs per scenario (default 3)")
    args = parser.parse_args()

    scenarios = yaml.safe_load(SCENARIOS.read_text())["scenarios"]
    if args.scenario:
        scenarios = [s for s in scenarios if s["name"] == args.scenario]
        if not scenarios:
            sys.exit("no scenario named %s" % args.scenario)

    failed = 0
    for scenario in scenarios:
        runs = args.repeat
        for n in range(1, runs + 1):
            problems, result = run_once(scenario, args.model)
            label = "%s [%d/%d]" % (scenario["name"], n, runs)
            if problems:
                failed += 1
                print("FAIL %s" % label)
                for problem in problems:
                    print("     - %s" % problem)
            else:
                print("pass %s" % label)
            if result is not None:
                shown = result if problems else {k: result.get(k) for k in
                                                 ("tier", "action", "supported", "reason", "pending")}
                print("     %s" % json.dumps(shown)[:2000])
    print("%d run(s) failed" % failed)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
