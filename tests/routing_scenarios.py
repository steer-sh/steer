#!/usr/bin/env python3
"""Run /steer's headless routing scenarios and check each decision.

Each run gets a fresh git repository holding the scenario's files and a
temporary home. Claude Code keeps the real home, which holds its login; the
steer CLI the skill calls runs under the temporary home through a wrapper
named by PLUGIN_ROOT and first on PATH. The runner hashes the real Steer and
harness files before and after each run and fails the run when one changed.

Usage:
    tests/.venv/bin/python3 tests/routing_scenarios.py --model <model>
    tests/.venv/bin/python3 tests/routing_scenarios.py --model <model> --scenario pi-unsupported --repeat 1
"""

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
SCENARIOS = ROOT / "tests" / "test_data" / "routing.yaml"
STEER = ROOT / "scripts" / "steer"
HOME = Path.home()
GUARDED = [
    HOME / ".claude" / "settings.json",
    HOME / ".claude" / "CLAUDE.md",
    HOME / ".claude" / "check-gate-approved.txt",
    HOME / ".steer" / "state" / "checks" / "adoptions.jsonl",
    HOME / ".steer" / "hooks" / "steer-manifest.json",
    HOME / ".steer" / "hooks" / "steer-hooks.py",
    HOME / ".codex" / "hooks.json",
    HOME / ".codex" / "AGENTS.md",
]
FIELDS = ("tier", "action", "supported")


def digest(paths):
    return {str(p): hashlib.sha256(p.read_bytes()).hexdigest() if p.exists() else None
            for p in paths}


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


def build_home(home, workdir, personal_gate):
    claude = home / ".claude"
    claude.mkdir(parents=True)
    if personal_gate:
        hooks = claude / "hooks"
        hooks.mkdir()
        (hooks / "check-gate.py").write_text("import sys\nsys.exit(0)\n")
        settings = {"hooks": {"Stop": [{"hooks": [
            {"type": "command", "command": "python3 ~/.claude/hooks/check-gate.py"}]}]}}
        (claude / "settings.json").write_text(json.dumps(settings, indent=2))
        (claude / "check-gate-approved.txt").write_text(str(workdir.resolve()) + "\n")


def build_wrapper(scratch, home):
    """Return (plugin root, bin dir, call log). The wrapper appends each call's
    arguments to the log, one line per call, before it runs the CLI."""
    calls = scratch / "steer-calls.log"
    script = "#!/bin/sh\necho \"$*\" >> %s\nHOME=%s exec %s \"$@\"\n" % (calls, home, STEER)
    plugin = scratch / "plugin" / "scripts"
    bindir = scratch / "bin"
    for directory in (plugin, bindir):
        directory.mkdir(parents=True)
        wrapper = directory / "steer"
        wrapper.write_text(script)
        wrapper.chmod(0o755)
    return scratch / "plugin", bindir, calls


def arming_problems(calls):
    """A personal gate must be looked for before a runner is registered."""
    lines = calls.read_text().splitlines() if calls.exists() else []
    migrate = next((i for i, l in enumerate(lines) if l.startswith("check migrate")), None)
    install = next((i for i, l in enumerate(lines) if l.startswith("check install")), None)
    problems = []
    if migrate is None:
        problems.append("the skill never ran steer check migrate to look for a personal gate")
    if install is not None and (migrate is None or install < migrate):
        problems.append("steer check install ran before steer check migrate")
    return problems


def run_skill(scenario, workdir, plugin, bindir, model):
    flags = "--headless --" + scenario["mode"]
    if scenario.get("harness"):
        flags += " --harness " + scenario["harness"]
    env = os.environ.copy()
    env.pop("CLAUDECODE", None)
    env["PLUGIN_ROOT"] = str(plugin)
    env["PATH"] = "%s:%s" % (bindir, env.get("PATH", ""))
    result = subprocess.run(
        ["claude", "-p", "/steer %s %s" % (flags, scenario["rule"]),
         "--output-format", "json", "--model", model,
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


def steer(home, workdir, *args):
    env = dict(os.environ, HOME=str(home))
    return subprocess.run([str(STEER), *args], cwd=workdir, env=env,
                          capture_output=True, text=True)


def installed_problems(workdir, home):
    """What a --route install beside a personal gate left wrong."""
    problems = []
    manifest = workdir / ".steer" / "checks" / "manifest.json"
    if not manifest.exists():
        return ["no check declared: %s is missing" % manifest.relative_to(workdir)]
    declared = json.loads(manifest.read_text()).get("checks", [])
    if not declared:
        problems.append("the check manifest declares no check")
    for check in declared:
        replay = steer(home, workdir, "check", "test", check["id"])
        if replay.returncode != 0:
            problems.append("steer check test %s failed: %s"
                            % (check["id"], (replay.stdout + replay.stderr)[-300:]))
    oracle = (workdir / "check.sh").read_text()
    if "unittest" not in oracle:
        problems.append("check.sh lost the existing suite")
    if "check exec" not in oracle and "check go-test" not in oracle:
        problems.append("check.sh does not call the check executor")
    settings = (home / ".claude" / "settings.json").read_text()
    if "check-gate.py" not in settings:
        problems.append("the personal gate was removed without consent")
    if "check hook" in settings:
        problems.append("a Stop runner was registered beside the personal gate")
    return problems


def run_once(scenario, model):
    scratch = Path(tempfile.mkdtemp(prefix="steer-route-"))
    try:
        workdir, home = scratch / "repo", scratch / "home"
        workdir.mkdir()
        build_repo(workdir, scenario.get("files"))
        build_home(home, workdir, scenario.get("personal_gate", False))
        plugin, bindir, calls = build_wrapper(scratch, home)

        before = digest(GUARDED)
        result, error = run_skill(scenario, workdir, plugin, bindir, model)
        leaked = [p for p, h in digest(GUARDED).items() if h != before[p]]

        problems = ["the run changed the real %s" % p for p in leaked]
        if result is None:
            return problems + [error], None
        for field in FIELDS:
            want = scenario["expect"].get(field)
            if want is not None and result.get(field) != want:
                problems.append("%s: want %r, got %r" % (field, want, result.get(field)))
        if scenario["mode"] == "route":
            problems += installed_problems(workdir, home) + arming_problems(calls)
            if not result.get("pending"):
                problems.append("pending is empty, but the personal gate leaves the repository unarmed")
        elif result.get("installed") or result.get("pending"):
            problems.append("route-only reported installed or pending steps")
        return problems, result
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--model", "-m", required=True, help="Claude model to run the skill")
    parser.add_argument("--scenario", "-s", help="Run only this scenario")
    parser.add_argument("--repeat", "-r", type=int, default=3,
                        help="Runs per route-only scenario (default 3); a --route scenario runs once")
    args = parser.parse_args()

    scenarios = yaml.safe_load(SCENARIOS.read_text())["scenarios"]
    if args.scenario:
        scenarios = [s for s in scenarios if s["name"] == args.scenario]
        if not scenarios:
            sys.exit("no scenario named %s" % args.scenario)

    failed = 0
    for scenario in scenarios:
        runs = args.repeat if scenario["mode"] == "route-only" else 1
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
