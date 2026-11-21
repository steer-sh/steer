#!/usr/bin/env python3
"""Run real Pi sessions against Steer's Pi integration in a sandbox.

Each case builds this checkout's steer and the e2e stub reviewer, arms what it
tests at project scope in a throwaway repository, runs `pi -p` once, and reads
Pi's own session file and the sandbox ledger. The stub reviewer stands in for
the reviewer CLI, so only Pi's model is paid for.

    python3 tests/pi_author.py --model openai-codex/gpt-6-luna [--case NAME ...] [--keep]

Cases:
  batch     With the guidance removed, a commit that shares its tool batch with
            `git add` is refused before review, with its receipt on stderr; the
            same commit run alone is reviewed.
  codemode  With codemode in "only" mode, a commit a script runs is refused
            before review, and pi -p prints the refusal receipt on stderr.
  check     A check.sh failure at Pi's settle boundary puts the block reason in
            the session and continues the run; the model repairs the tree and
            the next check run passes.
  guidance  The gate's commit guidance reaches the system prompt, and the
            commit the model makes is reviewed.
  load      With the gate and check runner armed and the package loaded with
            -e, one session makes status read both bridges as approved, and the
            package's session extension runs report cadence.
  review    The stub reviewer denies round 1; the model handles the finding and
            the commit lands on retry. After the ledger grace, the next
            session's turn_end resolves the retry row with the model's ledger.

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
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from sandbox import REPO, Sandbox  # noqa: E402

PASS, FAIL, INCONCLUSIVE = "PASS", "FAIL", "INCONCLUSIVE"
UNORDERED = "run the commit alone"
RECEIPT = "[commit-gate] refused a Pi commit"  # the operator's line for a refused batch
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
        self.env["STEER_STUB_SCRIPT"] = str(self.root / "stub-script")
        # Steer's ledger harvester finds Pi's sessions through this variable.
        self.env["PI_CODING_AGENT_SESSION_DIR"] = str(self.sessions)
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

    def pi(self, prompt, *extra):
        return self.sh("pi", "-p", "--approve", "--model", self.model, "--session-dir", str(self.sessions),
                       "--no-skills", "--no-prompt-templates", *extra, prompt, check=False, timeout=PI_TIMEOUT)

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
    if RECEIPT not in proc.stderr:
        return FAIL, "pi -p printed no refusal receipt on stderr:\n" + proc.stderr[-2000:]
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


def case_codemode(run):
    stage_commit(run)
    # "only" hides the direct tools, so the model can reach bash only from a script.
    (run.repo / ".pi" / "settings.json").write_text(json.dumps(
        {"defaultTools": ["+codemode"], "codemode": {"mode": "only"}}))
    proc = run.pi("Use the codemode tool to run `git add b.go` and then `git commit -m 'add b'`. "
                  "If the commit is blocked, report the block's message and stop.")
    scripted = [c for _, c in tool_calls(run.session_entries())
                if c.get("name") == "codemode" and "git commit" in json.dumps(c.get("arguments"))]
    if not scripted:
        return INCONCLUSIVE, "the model ran no commit from a codemode script\n" + proc.stdout[-2000:]
    if "add b" in run.commits():
        return FAIL, "a commit from a codemode script landed"
    stub = run.root / "stub.jsonl"
    if stub.exists() and stub.read_text().strip():
        return FAIL, "the scripted commit reached the reviewer"
    if RECEIPT not in proc.stderr:
        return FAIL, "the scripted commit did not land, and no refusal receipt reached stderr:\n" + \
            proc.stderr[-2000:]
    return PASS, "the gate refused the commit a codemode script ran, before review"


LEDGER_GRACE = 120  # gate.LedgerGrace: the resolver leaves a row alone this long after its deny


def case_review(run):
    stage_commit(run)
    (run.root / "stub-script").write_text("finding\nclean\n")
    proc = run.pi("Stage b.go and commit it with the subject 'add b'. If the commit review reports "
                  "findings, handle them as the review instructs, then commit again.")
    rows = run.ledger_rows()
    phases = [r.get("phase") for r in rows]
    if "add b" not in run.commits():
        if not rows:
            return INCONCLUSIVE, "the model made no commit\n" + proc.stdout[-2000:]
        return INCONCLUSIVE, "the model did not land the commit after the deny (phases %s)\n%s" % (
            phases, proc.stdout[-2000:])
    owed = [r for r in rows if r.get("phase") in ("retry", "nitpick")]
    if not owed:
        return FAIL, "the commit landed with no retry row after the deny (phases %s)" % phases
    # A session's own turn_end runs before the grace ends, so the next session's
    # turn_end is the one that resolves the row.
    wait = max(r.get("ts", 0) for r in rows) + LEDGER_GRACE + 5 - time.time()
    if wait > 0:
        time.sleep(wait)
    run.pi("Reply with the single word ok. Use no tools.")
    for _ in range(120):
        owed = [r for r in run.ledger_rows() if r.get("phase") in ("retry", "nitpick")]
        if all(r.get("backfilled") or r.get("ledger_found") for r in owed):
            break
        time.sleep(0.5)
    else:
        return FAIL, "the next session's turn_end did not resolve the retry row: %s" % json.dumps(owed)[:2000]
    found = [bool(r.get("ledger_found")) for r in owed]
    if not all(found):
        return FAIL, "turn_end resolved the retry row as a miss; the harvester found no ledger " \
            "(phases %s)\n%s" % (phases, proc.stdout[-2000:])
    return PASS, "round 1 denied, the commit landed on retry, and the next turn_end found its ledger " \
        "(phases %s)" % phases


CHECK_SH = """#!/usr/bin/env bash
if [ -e bad.txt ]; then
  echo "check.sh: bad.txt must not exist in this repository; remove it" >&2
  exit 1
fi
"""


def check_runs(run):
    rows = []
    for f in (run.box.home / ".steer").rglob("runs.jsonl"):
        rows += [json.loads(line) for line in f.read_text().splitlines() if line.strip()]
    return [r for r in rows if r.get("harness") == "pi"]


def case_check(run):
    run.seed_repo()
    oracle = run.repo / "check.sh"
    oracle.write_text(CHECK_SH)
    oracle.chmod(0o755)
    run.sh("git", "add", "check.sh")
    run.sh("git", "commit", "-q", "-m", "check")
    run.steer("check", "adopt")
    run.steer("check", "install", "--harness", "pi", "--scope", "project")
    proc = run.pi("Create a file named bad.txt containing the word hi. Then reply done.")
    entries = run.session_entries()
    blocks = [e for e in entries if e.get("type") == "custom_message" and e.get("customType") == "steer-check"]
    runs = check_runs(run)
    dispositions = [r.get("disposition") for r in runs]
    if not runs:
        return FAIL, "no Pi check run was recorded\n" + proc.stdout[-1500:] + proc.stderr[-1500:]
    if not blocks:
        if (run.repo / "bad.txt").exists():
            return FAIL, "check.sh failed and no block reached the session (dispositions %s)" % dispositions
        return INCONCLUSIVE, "the model did not create bad.txt (dispositions %s)" % dispositions
    if "steer check run: " not in json.dumps(blocks[0]):
        return FAIL, "the block entry carries no run line"
    if (run.repo / "bad.txt").exists():
        return FAIL, "the model did not repair the tree after the block (dispositions %s)" % dispositions
    if dispositions[-1] != "allow":
        return FAIL, "the last check run was %s, want allow (dispositions %s)" % (dispositions[-1], dispositions)
    return PASS, "the settle boundary blocked, the model removed bad.txt, the next run passed " \
        "(dispositions %s)" % dispositions


def stage_package(run):
    """Copy the Pi package's files into the sandbox, with this checkout's build beside them."""
    pkg = run.root / "pkg"
    pkg.mkdir()
    for rel in ("package.json", "pi", "scripts", "skills"):
        src = REPO / rel
        (shutil.copytree if src.is_dir() else shutil.copy2)(src, pkg / rel)
    platform = "%s-%s" % (os.uname().sysname.lower(), {"x86_64": "amd64", "aarch64": "arm64"}.get(
        os.uname().machine, os.uname().machine))
    (pkg / "bin" / platform).mkdir(parents=True)
    shutil.copy2(run.bin / "steer", pkg / "bin" / platform / "steer")
    return pkg


def case_load(run):
    run.seed_repo()
    run.arm_gate()
    run.steer("check", "install", "--harness", "pi", "--scope", "project")
    pkg = stage_package(run)
    proc = run.pi("Reply with the single word ok. Use no tools.", "-e", str(pkg))
    if not run.session_entries():
        return FAIL, "pi wrote no session\n" + proc.stdout[-1500:] + proc.stderr[-1500:]
    status = json.loads(run.steer("status", "--json").stdout)
    gate = [a for a in status["posture"]["commit_gate"] if a["harness"] == "pi" and a["scope"] == "project"]
    runner = [a for a in status["checks"]["runner"] if a["harness"] == "pi" and a["scope"] == "project"]
    trusts = {"gate": [a["trust"] for a in gate], "runner": [a["trust"] for a in runner]}
    if trusts != {"gate": ["approved"], "runner": ["approved"]}:
        loads = sorted(p.name for p in (run.box.home / ".steer" / "state" / "pi-loads").glob("*"))
        return FAIL, "status read %s after a Pi session; load records %s\n%s" % (trusts, loads, proc.stderr[-1500:])
    # The package's session extension runs report cadence, which starts a refresh in
    # the background on a machine that never refreshed; the refresh stamps its time.
    stamp = run.box.home / ".steer" / "state" / "steer-report" / "last_refresh"
    for _ in range(120):
        if stamp.exists():
            break
        time.sleep(0.5)
    else:
        return FAIL, "the package's session extension started no refresh (%s is absent)\n%s" % (
            stamp, proc.stderr[-1500:])
    return PASS, "Pi loaded both bridges and status read them approved; the session extension ran report cadence"


CASES = {"batch": case_batch, "check": case_check, "guidance": case_guidance, "load": case_load,
         "codemode": case_codemode, "review": case_review}


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
