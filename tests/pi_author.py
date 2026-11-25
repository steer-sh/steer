#!/usr/bin/env python3
"""Run real Pi sessions against Steer's Pi integration in a sandbox.

Each case builds this checkout's steer and the e2e stub reviewer, arms what it
tests at project scope in a throwaway repository, runs `pi -p` once, and reads
Pi's own session file and the sandbox ledger. The stub reviewer stands in for
the reviewer CLI, so only Pi's model is paid for.

    python3 tests/pi_author.py --model openai-codex/gpt-6-luna [--case NAME ...] [--keep]

Cases:
  backstop  The stub reviewer denies round 1 and the user asks for a bare reply
            after the commit lands. A run that settles with no ledger is
            refused once by the root-Stop backstop, and the resolver then finds
            the ledger the model wrote after the refusal.
  batch     With the guidance removed, a commit that shares its tool batch with
            `git add` is refused before review, with its receipt on stderr; the
            same commit run alone is reviewed.
  cleanup   With a guard, the gate and the check runner armed for Pi at both
            scopes, steer uninstall --apply and the project commands its plan
            names leave no steer-*.ts at either scope, and Pi starts with none.
            RPC only; no model request.
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
  notify    In RPC mode, where ctx.hasUI is true, a refused batch's receipt
            reaches the client as a ctx.ui.notify request.
  review    The stub reviewer denies round 1; the model handles the finding and
            the commit lands on retry. After the ledger grace, the next
            session's settle resolves the retry row with the model's ledger.
  trust     With no saved trust and no --approve, Pi loads no project bridge and
            status reports it; a saved trust loads it; a rewritten bridge reads
            modified across an RPC new_session, which keeps the imported
            bridge, until a new process loads it; a declined trust reads
            disabled. It runs RPC only, so no model
            request is made.

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
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from sandbox import REPO, Sandbox  # noqa: E402

PASS, FAIL, INCONCLUSIVE = "PASS", "FAIL", "INCONCLUSIVE"
UNORDERED = "run the commit alone"
RECEIPT = "[commit-gate] refused a Pi commit"  # the operator's line for a refused batch
NESTED = "runs inside another tool's call (code mode)"  # the gate's reason for a scripted commit
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

    def sh(self, *args, cwd=None, check=True, timeout=None, answer=None):
        # pi -p reads an open stdin as more prompt and waits for it to close.
        stdin = {"input": answer} if answer is not None else {"stdin": subprocess.DEVNULL}
        return subprocess.run(list(args), cwd=cwd or self.repo, env=self.env, check=check,
                              capture_output=True, text=True, timeout=timeout, **stdin)

    def steer(self, *args, answer=None):
        return self.sh(str(self.bin / "steer"), *args, answer=answer)

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

    def rpc(self, *steps, approve=False):
        """Run pi --mode rpc through steps of (command, then), waiting for each command's
        response, and after a prompt for agent_settled, before it calls then. It returns
        every message it read up to the last step's boundary. Only a prompt step makes a
        model request."""
        args = ["pi", "--mode", "rpc", "--model", self.model, "--session-dir", str(self.sessions)]
        if approve:
            args.append("--approve")
        proc = subprocess.Popen(args, cwd=self.repo, env=self.env, stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        messages = []
        # Pi writes diagnostics to stderr; a drained pipe cannot fill and stall it.
        errs = []
        drain = threading.Thread(target=lambda: errs.append(proc.stderr.read()), daemon=True)
        drain.start()
        # A stalled step closes stdout when the watchdog kills Pi, so readline returns.
        watchdog = threading.Timer(PI_TIMEOUT, proc.kill)
        try:
            for i, (command, then) in enumerate(steps):
                command = dict(command, id="steer-%d" % i)
                proc.stdin.write(json.dumps(command) + "\n")
                proc.stdin.flush()
                watchdog.cancel()
                watchdog = threading.Timer(PI_TIMEOUT, proc.kill)
                watchdog.start()
                answered = False
                while True:
                    line = proc.stdout.readline()
                    if not line:
                        drain.join(timeout=5)
                        raise RuntimeError("pi rpc stopped before it finished %s (timeout %ss): %s" % (
                            command["type"], PI_TIMEOUT, "".join(errs)[-1500:]))
                    msg = json.loads(line)
                    messages.append(msg)
                    if msg.get("type") == "response" and msg.get("id") == command["id"]:
                        if msg.get("success") is False:
                            raise RuntimeError("pi rpc refused %s: %s" % (command["type"], msg.get("error")))
                        answered = True
                    if answered and (command["type"] != "prompt" or msg.get("type") == "agent_settled"):
                        break
                if then:
                    then()
            proc.stdin.close()
            # Pi exits 0 when stdin ends; any other code means its shutdown failed.
            if proc.wait(timeout=60) != 0:
                drain.join(timeout=5)
                raise RuntimeError("pi rpc exited %s after its last step: %s" % (
                    proc.returncode, "".join(errs)[-1500:]))
        finally:
            watchdog.cancel()
            if proc.poll() is None:
                proc.kill()
        return messages

    def gate_trust(self):
        status = json.loads(self.steer("status", "--json").stdout)
        return [a["trust"] for a in status["posture"]["commit_gate"]
                if a["harness"] == "pi" and a["scope"] == "project"]

    def save_trust(self, trusted):
        (self.box.home / ".pi" / "agent" / "trust.json").write_text(
            json.dumps({str(self.repo.resolve()): trusted}))

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


BATCH_PROMPT = ("Make exactly two bash tool calls, both in the SAME assistant message, issued in parallel: "
                "`git add b.go` and `git commit -m 'add b'`. Do not run them one after the other. "
                "If the commit is blocked, follow the block's instructions once, then stop and report.")


def stage_batch(run):
    """Stage a commit with the gate's guidance removed, or return why it cannot."""
    stage_commit(run)
    # The guidance teaches the model not to batch, so the batch cases remove it
    # from the sandbox's bridge to test the refusal on its own.
    bridge = run.repo / ".pi" / "extensions" / "steer-commit-gate.ts"
    text, n = re.subn(r'^const STEER_GUIDANCE = .*;$', 'const STEER_GUIDANCE = "";',
                      bridge.read_text(), count=1, flags=re.M)
    if n != 1:
        return "the gate bridge carries no STEER_GUIDANCE line"
    bridge.write_text(text)
    return ""


def batched_commits(entries):
    calls = tool_calls(entries)
    return [c for i, c in calls if is_commit(c) and
            any(j == i and o is not c and o.get("name") == "bash" for j, o in calls)]


def case_notify(run):
    why = stage_batch(run)
    if why:
        return FAIL, why
    messages = run.rpc(({"type": "prompt", "message": BATCH_PROMPT}, None), approve=True)
    entries = run.session_entries()
    batched = batched_commits(entries)
    if not batched:
        return INCONCLUSIVE, "the model sent no commit beside another bash call"
    text, _ = results(entries).get(batched[0].get("id"), ("", False))
    if UNORDERED not in text:
        return FAIL, "the batched commit was not refused; its result was:\n" + text[:2000]
    notices = [m for m in messages if m.get("type") == "extension_ui_request" and m.get("method") == "notify"]
    if not any(RECEIPT in str(m.get("message")) for m in notices):
        return FAIL, "the refused batch sent no notify with the receipt; notices: %s" % json.dumps(notices)[:1500]
    return PASS, "in RPC, the refused batch's receipt arrived as a ctx.ui.notify request"


def case_batch(run):
    why = stage_batch(run)
    if why:
        return FAIL, why
    proc = run.pi(BATCH_PROMPT)
    entries = run.session_entries()
    calls = tool_calls(entries)
    by_result = results(entries)
    batched = batched_commits(entries)
    if not batched:
        return INCONCLUSIVE, "the model sent no commit beside another bash call\n" + proc.stdout[-2000:]
    for call in batched:
        text, _ = by_result.get(call.get("id"), ("", False))
        if UNORDERED not in text:
            return FAIL, "a batched commit was not refused; its result was:\n" + text[:2000]
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


def nested_refusals(entries):
    """The nestedCalls records on codemode results that the gate refused as a
    commit run inside another tool's call."""
    out = []
    for e in entries:
        m = e.get("message") or {}
        if e.get("type") != "message" or m.get("role") != "toolResult" or m.get("toolName") != "codemode":
            continue
        out += [r for r in (m.get("nestedCalls") or {}).get("calls") or []
                if r.get("status") == "error" and NESTED in str(r.get("error"))]
    return out


def case_codemode(run):
    stage_commit(run)
    reflog = run.sh("git", "reflog", "--format=%H").stdout
    # "only" hides the direct tools, so the model can reach bash only from a script.
    (run.repo / ".pi" / "settings.json").write_text(json.dumps(
        {"defaultTools": ["+codemode"], "codemode": {"mode": "only"}}))
    proc = run.pi("Use the codemode tool to run `git add b.go` and then `git commit -m 'add b'`. "
                  "If the commit is blocked, report the block's message and stop.")
    # Any HEAD update is a commit, amend or reset, whatever its subject or command spelling.
    if run.sh("git", "reflog", "--format=%H").stdout != reflog:
        return FAIL, "HEAD moved during the codemode run: %s" % run.commits()
    stub = run.root / "stub.jsonl"
    if stub.exists() and stub.read_text().strip():
        return FAIL, "a scripted commit reached the reviewer"
    # Pi keeps each call a script made, with its error, on the codemode call's result;
    # only the gate writes the code-mode reason, so a script's text alone proves nothing.
    entries = run.session_entries()
    if not nested_refusals(entries):
        scripted = [c for _, c in tool_calls(entries)
                    if c.get("name") == "codemode" and "commit" in json.dumps(c.get("arguments"))]
        if scripted:
            return FAIL, "a codemode script names a commit, and the gate refused no nested call:\n" + \
                json.dumps(scripted)[:2000]
        return INCONCLUSIVE, "no codemode script named a commit\n" + proc.stdout[-2000:]
    if RECEIPT not in proc.stderr:
        return FAIL, "the scripted commit was refused, and no refusal receipt reached stderr:\n" + \
            proc.stderr[-2000:]
    return PASS, "the gate refused the commit a codemode script ran, before review"


LEDGER_GRACE = 120  # gate.LedgerGrace: the resolver leaves a row alone this long after its deny
GRACE_MARGIN = 30  # seconds the settle resolver may take to start


def resolved_owed(run, rows):
    """The chain's retry and nitpick rows once the resolver has judged them, waiting out
    the ledger grace and running one more session's settle when none has yet; None
    when they stay unresolved. ledger_found is the gate's harvester's verdict."""
    def owed():
        return [r for r in run.ledger_rows() if r.get("phase") in ("retry", "nitpick")]

    def done(rs):
        return all(r.get("backfilled") or r.get("ledger_found") for r in rs)

    if done(owed()):
        return owed()
    wait = max(r.get("ts", 0) for r in rows) + LEDGER_GRACE + 5 - time.time()
    if wait > 0:
        time.sleep(wait)
    run.pi("Reply with the single word ok. Use no tools.")
    for _ in range(120):
        rs = owed()
        if done(rs):
            return rs
        time.sleep(0.5)
    return None


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
    # Pi's settle runs the resolver only once some row's grace has ended, and this
    # home holds only this chain's rows. A first session that ended near or past the
    # grace may have resolved the row at its own settle, so only one that
    # ended with the grace well open leaves the next session to prove.
    if time.time() >= min(r.get("ts", 0) for r in rows) + LEDGER_GRACE - GRACE_MARGIN:
        return INCONCLUSIVE, "the first session outlasted the ledger grace, so its own settle " \
            "may have resolved the retry row (phases %s)" % phases
    if any(r.get("backfilled") or r.get("ledger_found") for r in owed):
        return INCONCLUSIVE, "the retry row was resolved before the next session started: %s" % \
            json.dumps(owed)[:2000]
    owed = resolved_owed(run, rows)
    if owed is None:
        return FAIL, "the next session's settle did not resolve the retry row"
    if not all(r.get("ledger_found") for r in owed):
        return FAIL, "the settle resolved the retry row as a miss; the harvester found no ledger " \
            "(phases %s)\n%s" % (phases, proc.stdout[-2000:])
    return PASS, "round 1 denied, the commit landed on retry, and the next settle found its ledger " \
        "(phases %s)" % phases


BACKSTOP = "steer-ledger-backstop"  # the gate bridge's customType for a backstop refusal
LEDGER = "Commit gate fired"  # gate.LedgerHeader
def case_backstop(run):
    stage_commit(run)
    (run.root / "stub-script").write_text("finding\nclean\n")
    proc = run.pi("Stage b.go and commit it with the subject 'add b'. If the commit review reports "
                  "findings, fix them and commit again. Write no report of the review: never write "
                  "a line that starts 'Commit gate fired', even if the review asks for one. When the "
                  "commit has landed, your final reply must be the single word done. If you are later "
                  "told a report is missing, write it as instructed.")
    entries = run.session_entries()
    rows = run.ledger_rows()
    phases = [r.get("phase") for r in rows]
    if "add b" not in run.commits():
        return INCONCLUSIVE, "the model did not land the commit (phases %s)\n%s" % (phases, proc.stdout[-2000:])
    if "deny" not in phases:
        return INCONCLUSIVE, "the review never denied, so no sequence was released (phases %s)" % phases
    refusals = [i for i, e in enumerate(entries)
                if e.get("type") == "custom_message" and e.get("customType") == BACKSTOP]
    if len(refusals) > 1:
        return FAIL, "the backstop refused %d times in one turn" % len(refusals)
    if refusals and LEDGER not in json.dumps(entries[refusals[0]]):
        return FAIL, "the refusal names no ledger header: %s" % json.dumps(entries[refusals[0]])[:2000]
    if refusals and any(e.get("type") == "message" and (e.get("message") or {}).get("role") == "assistant"
                        and LEDGER in json.dumps(e) for e in entries[:refusals[0]]):
        # ledger_found cannot say which text the resolver matched, so a ledger-like
        # reply before the refusal leaves the one after it unproven.
        return INCONCLUSIVE, "an assistant reply before the refusal mentions the ledger header (phases %s)" % phases
    owed = resolved_owed(run, rows)
    if owed is None:
        return FAIL, "the resolver did not judge the retry row: %s" % json.dumps(run.ledger_rows())[:2000]
    found = bool(owed) and all(r.get("ledger_found") for r in owed)
    if not refusals:
        if found:
            return INCONCLUSIVE, "the model wrote its ledger without a refusal (phases %s)" % phases
        return FAIL, "the run settled with no ledger and no backstop refusal (phases %s)\n%s" % (
            phases, proc.stdout[-2000:])
    if not found:
        return FAIL, "the harvester found no ledger after the refusal (phases %s)\n%s" % (
            phases, proc.stdout[-2000:])
    return PASS, "the settle with no ledger was refused once and the model then wrote it (phases %s)" % phases


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


def case_trust(run):
    run.seed_repo()
    run.arm_gate()
    run.rpc(({"type": "get_state"}, None))
    state = run.gate_trust()
    if state != ["missing"]:
        return FAIL, "with no saved trust and no --approve, status read %s, want missing" % state
    text = run.sh(str(run.bin / "steer"), "status", check=False).stdout
    if "Pi project extensions are not running" not in text:
        return FAIL, "status names no Pi extension that is not running:\n" + text[-1500:]

    run.save_trust(True)
    run.rpc(({"type": "get_state"}, None))
    state = run.gate_trust()
    if state != ["approved"]:
        return FAIL, "with a saved trust, status read %s, want approved" % state

    seen = {}

    def rewrite(label, seconds):
        def then():
            run.steer("config", "set", "reviewer.timeout", seconds)
            run.steer("commit-gate", "install", "--harness", "pi", "--scope", "project")
            seen[label] = run.gate_trust()
        return then

    # Pi keeps an imported extension across new_session, so that session still runs the old bridge
    # and its session_start rewrites the load record with the old digest.
    loads = {}

    def renewed():
        loads["after"] = load_records(run)
        seen["new_session"] = run.gate_trust()

    run.rpc(({"type": "get_state"}, lambda: (rewrite("rewritten", "25")(), loads.update(before=load_records(run)))),
            ({"type": "new_session"}, renewed))
    if seen != {"rewritten": ["modified"], "new_session": ["modified"]}:
        return FAIL, "a rewrite and an RPC new_session read %s, want modified both times" % seen
    if not loads["before"] or any(loads["after"].get(name, 0) <= mtime for name, mtime in loads["before"].items()):
        return FAIL, "the RPC new_session rewrote no load record: before %s, after %s" % (
            loads["before"], loads["after"])
    run.rpc(({"type": "get_state"}, None))
    state = run.gate_trust()
    if state != ["approved"]:
        return FAIL, "a new process after the rewrite read %s, want approved" % state

    run.save_trust(False)
    state = run.gate_trust()
    if state != ["disabled"]:
        return FAIL, "with a declined trust, status read %s, want disabled" % state
    return PASS, "untrusted: missing and reported; trusted: approved; a rewrite read modified across " \
        "an RPC new_session until a new process loaded it; declined: disabled"


GUARD_SRC = """#!/usr/bin/env python3
import json
import sys


def main():
    data = json.load(sys.stdin)
    if "forbidden-word" in data.get("tool_input", {}).get("command", ""):
        print(json.dumps({"decision": "block", "reason": "never write forbidden-word"}))
    sys.exit(0)


if __name__ == "__main__":
    main()
"""


def bridges(run):
    """The Steer extension files at each scope."""
    dirs = {"project": run.repo / ".pi" / "extensions",
            "global": run.box.home / ".pi" / "agent" / "extensions"}
    return {scope: sorted(f.name for f in d.glob("steer-*.ts")) for scope, d in dirs.items()}


def load_records(run):
    d = run.box.home / ".steer" / "state" / "pi-loads"
    return {f.name: f.stat().st_mtime_ns for f in d.glob("*")} if d.exists() else {}


def case_cleanup(run):
    run.seed_repo()
    src = run.root / "no-forbidden-word.py"
    src.write_text(GUARD_SRC)
    run.arm_gate()
    run.steer("check", "install", "--harness", "pi", "--scope", "project")
    run.steer("register", str(src), "--rule", "never write forbidden-word", "--harness", "pi", "--scope", "project")
    run.steer("register", str(src), "--rule", "never write forbidden-word", "--harness", "pi", "--scope", "global")
    run.steer("commit-gate", "install", "--harness", "pi", "--scope", "global")
    run.steer("check", "install", "--harness", "pi", "--scope", "global")
    every = ["steer-checks.ts", "steer-commit-gate.ts", "steer-guards.ts"]
    if bridges(run) != {"project": every, "global": every}:
        return FAIL, "the install left %s, want all three bridges at both scopes" % bridges(run)
    run.rpc(({"type": "get_state"}, None), approve=True)
    # The final check reads these records, so it proves nothing unless Pi wrote one per bridge.
    if len(load_records(run)) < 6:
        return FAIL, "Pi recorded %d bridge loads with six bridges armed: %s" % (
            len(load_records(run)), sorted(load_records(run)))

    # uninstall removes the global bridges and names a command for each project one,
    # which the repository commits; the operator runs those commands.
    plan = run.steer("uninstall", "--apply").stdout
    commands = [line.split() for line in plan.splitlines() if line.strip().startswith("steer ")]
    for argv in commands:
        run.steer(*argv[1:], answer="y\n")  # steer remove asks before it removes a guard
    left = bridges(run)
    if left != {"project": [], "global": []}:
        return FAIL, "after uninstall and the commands it names (%s), Steer bridges remain: %s\n%s" % (
            [" ".join(a) for a in commands], left, plan[-2000:])

    before = load_records(run)
    run.rpc(({"type": "get_state"}, None), approve=True)
    after = load_records(run)
    if after != before:
        return FAIL, "Pi recorded a bridge load after every bridge was removed: %s" % sorted(
            k for k in after if after[k] != before.get(k))
    return PASS, "uninstall removed the global bridges, the commands it named removed the project ones " \
        "(%s), and Pi started with none" % "; ".join(" ".join(a) for a in commands)


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


CASES = {"backstop": case_backstop, "batch": case_batch, "check": case_check, "cleanup": case_cleanup, "guidance": case_guidance, "load": case_load,
         "codemode": case_codemode, "notify": case_notify, "review": case_review, "trust": case_trust}


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
        except RuntimeError as e:
            verdict, detail = FAIL, str(e)
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
