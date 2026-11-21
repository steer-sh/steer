#!/usr/bin/env python3
"""Run real Pi sessions against Steer's Pi integration in a sandbox.

Each case builds this checkout's steer and the e2e stub reviewer, arms what it
tests at project scope in a throwaway repository, runs `pi -p` once, and reads
Pi's own session file and the sandbox ledger. The stub reviewer stands in for
the reviewer CLI, so only Pi's model is paid for.

    python3 tests/pi_author.py --model openai-codex/gpt-6-luna [--case NAME ...] [--keep]

Cases:
  batch     With the guidance removed, a commit that shares its tool batch with
            `git add` is refused before review; the same commit run alone is
            reviewed.
  guidance  The gate's commit guidance reaches the system prompt, and the
            commit the model makes is reviewed.

A case whose model did not make the calls the case needs reports INCONCLUSIVE
and exits 2: the integration was not exercised, so nothing passed or failed.
Pi needs a login in ~/.pi/agent/auth.json.
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

PASS, FAIL, INCONCLUSIVE = "PASS", "FAIL", "INCONCLUSIVE"
UNORDERED = "run the commit alone"
PI_TIMEOUT = 300  # seconds one `pi -p` may run; a stalled model request fails its case


class Run:
    """One case's sandbox, binaries, repository and environment."""

    def __init__(self, root, model):
        self.root = Path(root)
        self.model = model
        self.box = Sandbox(self.root, pi=True)
        self.bin = self.root / "bin"
        self.stubs = self.root / "stubs"
        self.repo = self.root / "repo"
        self.sessions = self.root / "sessions"
        for d in (self.bin, self.stubs, self.repo, self.sessions):
            d.mkdir(parents=True, exist_ok=True)
        self.env = self.box.env(claude=False)
        self.env["PATH"] = os.pathsep.join([str(self.stubs), str(self.bin), self.env.get("PATH", "")])
        self.env["STEER_STUB_LOG"] = str(self.root / "stub.jsonl")
        self.env["STEER_STUB_STATE"] = str(self.root / "stubstate")
        (self.root / "stubstate").mkdir(exist_ok=True)

    def build(self):
        cli = REPO / "cli"
        go_env = dict(os.environ, CGO_ENABLED="0")
        for out, pkg in ((self.bin / "steer", "."), (self.root / "stubreviewer", "./e2e/stubreviewer")):
            subprocess.run(["go", "build", "-o", str(out), pkg], cwd=cli, env=go_env, check=True)
        os.symlink(self.root / "stubreviewer", self.stubs / "claude")

    def sh(self, *args, cwd=None, check=True, timeout=None):
        # pi -p reads an open stdin as more prompt and waits for it to close.
        return subprocess.run(list(args), cwd=cwd or self.repo, env=self.env, check=check,
                              capture_output=True, text=True, timeout=timeout,
                              stdin=subprocess.DEVNULL)

    def steer(self, *args):
        return self.sh(str(self.bin / "steer"), *args)

    def seed_repo(self):
        self.sh("git", "init", "-q")
        (self.repo / "README.md").write_text("seed\n")
        self.sh("git", "add", "README.md")
        self.sh("git", "commit", "-q", "-m", "seed")

    def arm_gate(self):
        for key, value in (("reviewer.provider", "claude-code"), ("reviewer.model", "model-claude-code"),
                           ("reviewer.runs", "1"), ("reviewer.timeout", "20")):
            self.steer("config", "set", key, value)
        self.steer("commit-gate", "install", "--harness", "pi", "--scope", "project")

    def pi(self, prompt):
        return self.sh("pi", "-p", "--approve", "--model", self.model, "--session-dir", str(self.sessions),
                       "--no-skills", "--no-prompt-templates", prompt, check=False, timeout=PI_TIMEOUT)

    def session_entries(self):
        files = sorted(self.sessions.rglob("*.jsonl"))
        entries = []
        for f in files:
            for line in f.read_text().splitlines():
                try:
                    entries.append(json.loads(line))
                except ValueError:
                    pass
        return entries

    def ledger_rows(self):
        rows = []
        for shard in (self.box.home / ".steer").rglob("adjudication.*.jsonl"):
            rows += [json.loads(line) for line in shard.read_text().splitlines() if line.strip()]
        return rows

    def commits(self):
        return self.sh("git", "log", "--format=%s").stdout.splitlines()


def tool_calls(entries):
    """(message index, call) for each toolCall block, in order."""
    out = []
    for i, e in enumerate(entries):
        m = e.get("message") or {}
        if e.get("type") == "message" and m.get("role") == "assistant":
            for block in m.get("content") or []:
                if isinstance(block, dict) and block.get("type") == "toolCall":
                    out.append((i, block))
    return out


def results(entries):
    out = {}
    for e in entries:
        m = e.get("message") or {}
        if e.get("type") == "message" and m.get("role") == "toolResult":
            text = "".join(c.get("text", "") for c in m.get("content") or [] if isinstance(c, dict))
            out[m.get("toolCallId")] = (text, bool(m.get("isError")))
    return out


def is_commit(call):
    return call.get("name") == "bash" and "git commit" in str((call.get("arguments") or {}).get("command", ""))


def stage_commit(run):
    run.seed_repo()
    (run.repo / "b.go").write_text("package b\n\n" + "var B = 1 // a changed line for review\n" * 50)
    run.arm_gate()


def case_guidance(run):
    stage_commit(run)
    proc = run.pi("Stage b.go and commit it with the subject 'add b'.")
    if "<steer-commit-gate>" not in json.dumps(run.session_entries()):
        return FAIL, "the session's system prompt carries no steer-commit-gate section"
    if "add b" not in run.commits():
        return INCONCLUSIVE, "the model made no commit\n" + proc.stdout[-2000:]
    if not run.ledger_rows():
        return FAIL, "the commit landed with no ledger row"
    return PASS, "the guidance reached the system prompt; the commit was reviewed"


def case_batch(run):
    stage_commit(run)
    # The guidance teaches the model not to batch, so this case removes it from
    # the sandbox's bridge to test the refusal on its own.
    bridge = run.repo / ".pi" / "extensions" / "steer-commit-gate.ts"
    text, n = re.subn(r'^const STEER_GUIDANCE = .*;$', 'const STEER_GUIDANCE = "";',
                      bridge.read_text(), count=1, flags=re.M)
    if n != 1:
        return FAIL, "the gate bridge carries no STEER_GUIDANCE line"
    bridge.write_text(text)
    proc = run.pi(
        "Make exactly two bash tool calls, both in the SAME assistant message, issued in parallel: "
        "`git add b.go` and `git commit -m 'add b'`. Do not run them one after the other. "
        "If the commit is blocked, follow the block's instructions once, then stop and report.")
    entries = run.session_entries()
    calls = tool_calls(entries)
    by_result = results(entries)
    batched = [c for i, c in calls if is_commit(c) and
               any(j == i and o is not c and o.get("name") == "bash" for j, o in calls)]
    if not batched:
        return INCONCLUSIVE, "the model sent no commit beside another bash call\n" + proc.stdout[-2000:]
    text, _ = by_result.get(batched[0].get("id"), ("", False))
    if UNORDERED not in text:
        return FAIL, "the batched commit was not refused; its result was:\n" + text[:2000]
    # Only a commit run alone can write a ledger row, so rows count those commits.
    alone = [c for i, c in calls if is_commit(c) and sum(1 for j, _ in calls if j == i) == 1]
    rows = run.ledger_rows()
    if not alone:
        if rows:
            return FAIL, "the refused commit reached the reviewer (%d ledger rows)" % len(rows)
        return PASS, "the batched commit was refused before review; the model did not retry it alone"
    if not rows:
        return FAIL, "the model ran the commit alone and no review was recorded"
    return PASS, "the batched commit was refused before review; the commit run alone was reviewed"


CASES = {"batch": case_batch, "guidance": case_guidance}


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--model", required=True, help="Pi model, for example openai-codex/gpt-6-luna")
    parser.add_argument("--case", action="append", choices=sorted(CASES), help="run only this case")
    parser.add_argument("--keep", action="store_true", help="keep each case's sandbox")
    args = parser.parse_args()
    if not shutil.which("pi"):
        raise SystemExit("pi is not on PATH")
    verdicts = []
    for name in args.case or sorted(CASES):
        root = tempfile.mkdtemp(prefix="steer-pi-%s-" % name)
        run = Run(root, args.model)
        try:
            run.build()
            verdict, detail = CASES[name](run)
        except subprocess.CalledProcessError as e:
            verdict, detail = FAIL, "%s\n%s%s" % (e, e.stdout or "", e.stderr or "")
        except subprocess.TimeoutExpired as e:
            verdict, detail = FAIL, "%s did not finish in %ss" % (e.cmd[0], e.timeout)
        leaks = run.box.leaks()
        if leaks:
            verdict, detail = FAIL, "\n".join(leaks)
        print("%s %s: %s" % (verdict, name, detail))
        if args.keep:
            print("  kept: %s" % root)
        else:
            shutil.rmtree(root, ignore_errors=True)
        verdicts.append(verdict)
    sys.exit(1 if FAIL in verdicts else 2 if INCONCLUSIVE in verdicts else 0)


if __name__ == "__main__":
    main()
