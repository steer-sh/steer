#!/usr/bin/env python3
"""Check that a guard's block reason reaches the blocked agent.

A unit test can check the JSON a master prints, not what a harness does with it.
This runs a real agent in a sandbox (tests/sandbox.py) against one probe guard
and reads the agent's own transcript: the recorded result of the blocked call
must carry the guard's reason, the `steer hook: ` footer and the contest
command for that firing.

    python3 tests/block_delivery.py [--harness claude|codex ...] [--claude-model M]
        [--codex-model M] [--keep]

Claude Code needs CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY exported; Codex
needs a login in ~/.codex/auth.json.
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from sandbox import REPO, Sandbox  # noqa: E402

TOKEN = "STEER_DELIVERY_PROBE_7f3a"
HOOK = "delivery-probe-hook"
REASON = "delivery probe blocked this command"
FOOTER = "steer hook: " + HOOK
CONTEST = re.compile(r"firing: ([0-9a-f]{32})[\s\S]*contest \1 ")

GUARD = '''#!/usr/bin/env python3
import json
import sys


def main():
    try:
        data = json.loads(sys.stdin.read())
    except Exception:
        sys.exit(0)
    if data.get("tool_name") != "Bash":
        sys.exit(0)
    if %(token)r in data.get("tool_input", {}).get("command", ""):
        print(json.dumps({"decision": "block", "stopReason": %(reason)r}))
    sys.exit(0)


if __name__ == "__main__":
    main()
''' % {"token": TOKEN, "reason": REASON}

PROMPT = ("Run exactly this shell command once: echo %s\n"
          "Do not retry it and do not run anything else. Then stop." % TOKEN)


def build_steer(bin_dir):
    exe = bin_dir / "steer"
    subprocess.run(["go", "build", "-o", str(exe), "."], cwd=REPO / "cli", check=True)
    return exe


def install_guard(box, work, env):
    subprocess.run(["git", "init", "-q"], cwd=work, check=True)
    hooks = work / ".steer" / "hooks"
    hooks.mkdir(parents=True)
    guard = hooks / (HOOK + ".py")
    guard.write_text(GUARD)
    guard.chmod(0o755)
    # register writes the rule into CLAUDE.md; a rule naming the token makes
    # the agent refuse the command, so no block would ever happen.
    subprocess.run(["steer", "register", str(guard), "--rule", "sandbox guard for a hook test"],
                   cwd=work, env=env, check=True, stdout=subprocess.DEVNULL)


def claude_results(home):
    """The text of every errored tool result in the sandbox's Claude transcripts."""
    out = []
    for path in (home / ".claude" / "projects").rglob("*.jsonl"):
        for line in path.read_text(errors="replace").splitlines():
            try:
                row = json.loads(line)
            except json.JSONDecodeError:
                continue
            content = (row.get("message") or {}).get("content")
            if not isinstance(content, list):
                continue
            for block in content:
                if isinstance(block, dict) and block.get("type") == "tool_result" and block.get("is_error"):
                    body = block.get("content")
                    if isinstance(body, list):
                        body = " ".join(b.get("text", "") for b in body if isinstance(b, dict))
                    out.append(str(body))
    return out


def codex_results(home):
    """The text of every tool output item in the sandbox's Codex rollouts."""
    out = []
    for path in (home / ".codex" / "sessions").rglob("rollout-*.jsonl"):
        for line in path.read_text(errors="replace").splitlines():
            try:
                row = json.loads(line)
            except json.JSONDecodeError:
                continue
            payload = row.get("payload") or {}
            if row.get("type") == "response_item" and str(payload.get("type", "")).endswith("_output"):
                out.append(json.dumps(payload))
    return out


def run(harness, args):
    root = Path(tempfile.mkdtemp(prefix="steer-delivery-"))
    try:
        box = Sandbox(root, codex=harness == "codex")
        bin_dir = box.root / "bin"
        bin_dir.mkdir()
        build_steer(bin_dir)
        env = box.env(claude=harness == "claude")
        env["PATH"] = str(bin_dir) + os.pathsep + env.get("PATH", "")
        work = box.root / "work"
        work.mkdir()
        install_guard(box, work, env)

        if harness == "claude":
            cmd = ["claude", "-p", PROMPT, "--model", args.claude_model,
                   "--allowedTools", "Bash"] + box.claude_args()
            subprocess.run(cmd, cwd=work, env=env, timeout=600)
            results = claude_results(box.home)
        else:
            # The sandbox wrote its only hook itself, and codex exec runs no
            # project hook without persisted trust.
            cmd = ["codex", "exec", "--model", args.codex_model, "--sandbox", "workspace-write",
                   "--dangerously-bypass-hook-trust", PROMPT]
            subprocess.run(cmd, cwd=work, env=env, timeout=600)
            results = codex_results(box.home)

        failures = ["%s: %s" % (harness, leak) for leak in box.leaks()]
        hits = [r for r in results if FOOTER in r]
        if not results:
            failures.append("%s: the transcript holds no blocked tool result" % harness)
        elif not hits:
            failures.append("%s: no blocked result carries %r; results: %s"
                            % (harness, FOOTER, [r[:200] for r in results]))
        elif not any(REASON in r for r in hits):
            failures.append("%s: the footer arrived without the guard's reason: %s"
                            % (harness, hits[0][:300]))
        elif not any(CONTEST.search(r.replace("\\n", "\n")) for r in hits):
            failures.append("%s: the footer arrived without the contest command for its firing: %s"
                            % (harness, hits[0][:600]))
        return failures
    finally:
        if args.keep:
            print("kept: %s" % root, file=sys.stderr)
        else:
            shutil.rmtree(root, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--harness", action="append", choices=["claude", "codex"],
                        help="Harness to check; repeatable (default: both)")
    parser.add_argument("--claude-model", default="claude-haiku-5-5",
                        help="Claude Code model (default: claude-haiku-5-5)")
    parser.add_argument("--codex-model", default="gpt-6-luna",
                        help="Codex model (default: gpt-6-luna)")
    parser.add_argument("--keep", action="store_true", help="Keep each sandbox directory")
    args = parser.parse_args()
    failures = []
    for harness in args.harness or ["claude", "codex"]:
        found = run(harness, args)
        print("%s: %s" % (harness, "FAIL" if found else "PASS"))
        failures += found
    for failure in failures:
        print("  " + failure, file=sys.stderr)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
