#!/usr/bin/env python3
"""Replay a recorded commit-gate round 1 to the author under one deny/cap template arm.

  build    Build a steer binary from the cli/ tree of one revision (git archive; the
           working tree is not touched).
  episode  Run one episode in a sandbox home:
           1. clone --repo at the start's base_sha, apply its round-1 diff to the index;
           2. install --steer-bin and arm the commit gate at sandbox global scope;
           3. seed: send the original commit command through that gate with a stub
              reviewer that replies with the frozen round-1 bundle, so the gate writes
              the real chain state and round-1 ledger row and renders the arm's deny;
           4. rebuild the author's session up to its commit tool_use, with the repo path
              rewritten to the clone, and append that deny as the tool result;
           5. resume it with `claude -p`. A guard hook ends the episode after a commit
              lands or once the third panel would start (the second live review).
           Writes the session, the sandbox ledger, the reviewer calls, the clone's diffs
           and result.json to a new --out directory.

A start is a JSON file: {"session_id", "base_sha", "diff", "bundle", "prefix",
"prefix_lines"}, paths absolute. Claude Code needs CLAUDE_CODE_OAUTH_TOKEN or
ANTHROPIC_API_KEY exported; Codex uses sandbox.py's login copy.
"""

import argparse
import gzip
import json
import os
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

from sandbox import GIT_IDENTITY, REPO, Sandbox

REAL_PATHS = ("/Volumes/MiniEx/projects/steer-skill", "/Users/jeremy/MiniEx/projects/steer-skill")
OUTSIDE = ("/Volumes/MiniEx", "/Users/jeremy")
REVIEWER = {"provider": "codex", "model": "gpt-6.1-sol", "effort": "low", "runs": "3", "timeout": "400"}
REVIEW_PHASES = ("deny", "clean", "cap")
DENY_PREFIX = "PreToolUse:Bash hook error: "
RESUME_PROMPT = "Continue."
STOP_REASON = ("This trial episode is over: %s. Write any report you owe now, as text, "
               "and make no further tool calls.")


def git(cwd, *args, env=None, check=True):
    return subprocess.run(["git", "-C", str(cwd), *args], env=env, check=check,
                          text=True, capture_output=True).stdout


def build(args):
    out = args.out.resolve()
    with tempfile.TemporaryDirectory(prefix="steer-arm-") as tmp:
        archive = subprocess.run(["git", "-C", str(REPO), "archive", args.rev, "cli"],
                                 check=True, capture_output=True).stdout
        subprocess.run(["tar", "-x", "-C", tmp], input=archive, check=True)
        subprocess.run(["go", "build", "-o", str(out), "."], cwd=Path(tmp) / "cli", check=True)
    print(out)
    return 0


def bundle_sections(path):
    """The per-reviewer reply texts of a frozen '--- REVIEWS ---' bundle."""
    text = Path(path).read_text()
    body = text.split("--- REVIEWS ---\n", 1)[1].rsplit("--- END REVIEWS ---", 1)[0]
    parts = re.split(r"^--- reviewer \d+ ---\n", body, flags=re.M)
    return [p.strip() + "\n" for p in parts if p.strip()]


def ledger_rows(home):
    rows = []
    for path in sorted((home / ".steer" / "state" / "commit-review").glob("adjudication.*.jsonl")):
        rows += [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
    return rows


def live_panels(rows):
    return [r for r in rows if r.get("phase") in REVIEW_PHASES and int(r.get("firing") or 0) >= 2]


def reviewer(root, argv):
    """Stand in for codex: replay a frozen reviewer section while seeding, else pass to
    the real CLI until the third panel would start."""
    root = Path(root)
    state = json.loads((root / "reviewer.json").read_text())
    stdin = sys.stdin.read()
    calls = root / "reviewer-calls"
    calls.mkdir(exist_ok=True)
    record = {"mode": state["mode"], "argv": argv, "started": time.time(), "prompt_chars": len(stdin)}
    if state["mode"] == "stub":
        sections = state["sections"]
        index = 0
        while True:
            try:
                fd = os.open(str(calls / ("stub-%d" % index)), os.O_CREAT | os.O_EXCL | os.O_WRONLY)
                break
            except FileExistsError:
                index += 1
        record["section"] = index
        os.write(fd, json.dumps(record).encode())
        os.close(fd)
        sys.stdout.write(sections[index % len(sections)])
        return 0
    if live_panels(ledger_rows(Path(state["home"]))):
        (root / "boundary").touch()
        record["refused"] = "third panel"
        (calls / ("refused-%d" % time.time_ns())).write_text(json.dumps(record))
        print("trial: the third panel boundary was reached", file=sys.stderr)
        return 1
    result = subprocess.run([state["codex"], *argv], input=stdin, text=True, capture_output=True)
    record.update(seconds=time.time() - record["started"], returncode=result.returncode,
                  stdout=result.stdout, stderr=result.stderr[-4000:])
    (calls / ("live-%d.json" % time.time_ns())).write_text(json.dumps(record))
    sys.stdout.write(result.stdout)
    sys.stderr.write(result.stderr)
    return result.returncode


def guard(root, work, base):
    """PreToolUse guard: end the episode after a commit lands or at the boundary, and
    refuse any tool input that names a path outside the sandbox."""
    payload = json.load(sys.stdin)
    root = Path(root)
    reason = None
    if (root / "boundary").exists():
        reason = STOP_REASON % "the next review would be the third panel"
    elif git(work, "rev-list", "%s..HEAD" % base, check=False).strip():
        reason = STOP_REASON % "the commit landed"
    if reason:
        with open(root / "stop-denials", "a") as fh:
            fh.write(payload.get("tool_name", "") + "\n")
    elif any(p in json.dumps(payload.get("tool_input") or {}) for p in OUTSIDE):
        reason = ("This run is sandboxed: paths under %s are unavailable. The repository is %s."
                  % (" and ".join(OUTSIDE), work))
    if reason:
        print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse",
                                                 "permissionDecision": "deny",
                                                 "permissionDecisionReason": reason}}))
    return 0


def rewrite(text, work):
    for real in REAL_PATHS:
        text = text.replace(real, str(work))
    return text


def session_rows(start, work, deny_text):
    """The prefix up to and including the commit tool_use, then the arm's deny result.
    Also returns the original deny text."""
    with gzip.open(start["prefix"], "rt") as fh:
        lines = fh.read().splitlines()
    deny_at = None
    for i in range(start["prefix_lines"], len(lines)):
        d = json.loads(lines[i])
        content = (d.get("message") or {}).get("content")
        if d.get("type") == "user" and isinstance(content, list) and any(
                c.get("type") == "tool_result" and str(c.get("content", "")).startswith(DENY_PREFIX)
                and "red-teamed this commit" in str(c.get("content")) for c in content):
            deny_at = i
            break
    if deny_at is None:
        raise ValueError("no round-1 deny after prefix line %d" % start["prefix_lines"])
    deny = json.loads(lines[deny_at])
    use_id = next(c["tool_use_id"] for c in deny["message"]["content"] if c.get("type") == "tool_result")
    use_at = next(i for i in range(deny_at - 1, -1, -1)
                  if use_id in lines[i] and json.loads(lines[i]).get("type") == "assistant")
    command = next(c["input"]["command"] for c in json.loads(lines[use_at])["message"]["content"]
                   if c.get("type") == "tool_use" and c.get("id") == use_id)
    original = next(c["content"] for c in deny["message"]["content"] if c.get("type") == "tool_result")
    kept = [rewrite(line, work) for line in lines[:use_at + 1]]
    deny = json.loads(rewrite(lines[deny_at], work))
    for c in deny["message"]["content"]:
        if c.get("type") == "tool_result":
            c["content"] = DENY_PREFIX + deny_text
    deny["toolUseResult"] = "Error: " + DENY_PREFIX + deny_text
    kept.append(json.dumps(deny, ensure_ascii=False))
    return kept, rewrite(command, work), rewrite(original, work)


def real_repo_state():
    return {"head": git(REPO, "rev-parse", "HEAD"), "status": git(REPO, "status", "--porcelain"),
            "refs": git(REPO, "for-each-ref", "--format=%(refname) %(objectname)"),
            "stash": git(REPO, "stash", "list")}


def episode(args):
    start = json.loads(args.start.read_text())
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=False)
    real_codex = shutil.which("codex")
    if not real_codex:
        raise SystemExit("codex is missing from PATH")
    before = real_repo_state()
    root = Path(tempfile.mkdtemp(prefix="steer-trial-"))
    box = None
    result = {"start": str(args.start), "arm_bin": str(args.steer_bin), "label": args.label,
              "model": args.model, "max_turns": args.max_turns,
              "resume_prompt": RESUME_PROMPT, "failures": []}
    try:
        box = Sandbox(root, codex=True)
        env = box.env(claude=not args.seed_only)
        env = {k: v for k, v in env.items()
               if not k.startswith(("STEER_", "COMMIT_GATE_", "COMMIT_REVIEW_", "GIT_", "CLAUDE_CODE_SESSION"))}
        env.update(GIT_CONFIG_GLOBAL=str(box.gitconfig), GIT_CONFIG_NOSYSTEM="1", **GIT_IDENTITY)
        for key in ("GOCACHE", "GOMODCACHE", "GOPATH"):
            env[key] = subprocess.run(["go", "env", key], capture_output=True, text=True).stdout.strip()
        bindir = box.root / "bin"
        bindir.mkdir()
        steer = bindir / "steer"
        shutil.copy2(args.steer_bin, steer)
        me = shlex.quote(sys.executable) + " " + shlex.quote(str(Path(__file__).resolve()))
        (bindir / "codex").write_text("#!/bin/sh\nexec %s --reviewer %s \"$@\"\n" % (me, shlex.quote(str(box.root))))
        (bindir / "codex").chmod(0o755)
        env["PATH"] = str(bindir) + os.pathsep + env.get("PATH", "")
        (box.root / "reviewer.json").write_text(json.dumps(
            {"mode": "stub", "sections": bundle_sections(start["bundle"]), "home": str(box.home),
             "codex": real_codex}))

        def run(argv, cwd, stdin=None, timeout=600):
            r = subprocess.run(argv, cwd=cwd, env=env, input=stdin, text=True, capture_output=True,
                               timeout=timeout)
            if r.returncode:
                raise ValueError("%s exited %d: %s" % (argv[:3], r.returncode, r.stderr[-1500:]))
            return r.stdout

        # The clone's origin is a scratch bare copy whose main is base_sha, so a push
        # stays in the sandbox and "since upstream" means since the round-1 base.
        origin = box.root / "origin.git"
        work = box.root / "steer-skill"
        run(["git", "clone", "-q", "--bare", "--shared", str(REPO), str(origin)], box.root)
        run(["git", "-C", str(origin), "update-ref", "refs/heads/main", start["base_sha"]], box.root)
        run(["git", "clone", "-q", str(origin), str(work)], box.root)
        run(["git", "checkout", "-q", "main"], work)
        run(["git", "apply", "--index", start["diff"]], work)
        for key, value in REVIEWER.items():
            run([str(steer), "config", "set", "reviewer." + key, value], work)
        run([str(steer), "bin", "install"], work)
        run([str(steer), "commit-gate", "install", "--harness", "claude-code", "--scope", "global"], work)
        settings_path = box.home / ".claude" / "settings.json"
        settings = json.loads(settings_path.read_text())
        registered = [h["command"] for e in settings["hooks"]["PreToolUse"] for h in e["hooks"]
                      if "commit-gate review" in h.get("command", "")]
        if len(registered) != 1:
            raise ValueError("expected one registered review command, found %d" % len(registered))

        # Seed round 1 through the arm's own gate.
        kept, command, original = session_rows(start, work, "")
        payload = {"session_id": start["session_id"], "cwd": str(work), "hook_event_name": "PreToolUse",
                   "tool_name": "Bash", "tool_input": {"command": command}}
        reply = json.loads(run(["sh", "-c", registered[0]], work, json.dumps(payload)))
        hso = reply.get("hookSpecificOutput", {})
        rows = ledger_rows(box.home)
        if hso.get("permissionDecision") != "deny" or [r.get("phase") for r in rows] != ["deny"]:
            raise ValueError("seed did not deny once: %s / %s" % (hso.get("permissionDecision"),
                                                                  [r.get("phase") for r in rows]))
        # The panel runs reviewers in parallel, so the stub's sections arrive in any
        # order; the frozen bundle restores the order the author originally read.
        deny_text = hso["permissionDecisionReason"]
        head, rest = deny_text.split("--- REVIEWS ---\n", 1)
        tail = rest.split("--- END REVIEWS ---", 1)[1]
        deny_text = head + Path(start["bundle"]).read_text().rstrip("\n") + tail
        (out / "seed-deny.txt").write_text(deny_text)
        (out / "original-deny.txt").write_text(original[len(DENY_PREFIX):])
        result["seed_row"] = rows[0]

        kept, command, _ = session_rows(start, work, deny_text)
        enc = re.sub(r"[^A-Za-z0-9]", "-", str(work))
        session_dir = box.home / ".claude" / "projects" / enc
        session_dir.mkdir(parents=True, exist_ok=True)
        (session_dir / (start["session_id"] + ".jsonl")).write_text("\n".join(kept) + "\n")
        result["session_rows"] = len(kept)
        if args.seed_only:
            return 0

        guard_cmd = "%s --guard %s %s %s" % (me, shlex.quote(str(box.root)), shlex.quote(str(work)),
                                             start["base_sha"])
        settings["hooks"]["PreToolUse"].append({"matcher": "*", "hooks": [{"type": "command", "command": guard_cmd}]})
        settings_path.write_text(json.dumps(settings, indent=2))
        state = json.loads((box.root / "reviewer.json").read_text())
        state["mode"] = "live"
        (box.root / "reviewer.json").write_text(json.dumps(state))

        argv = ["claude", "-p", "--resume", start["session_id"], "--model", args.model,
                "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"]
        if args.max_turns:
            argv += ["--max-turns", str(args.max_turns)]
        argv.append(RESUME_PROMPT)
        began = time.monotonic()
        with open(out / "stream.jsonl", "w") as so, open(out / "stderr.txt", "w") as se:
            proc = subprocess.Popen(argv, cwd=work, env=env, stdout=so, stderr=se, start_new_session=True)
            ending = None
            while proc.poll() is None:
                time.sleep(5)
                denials = box.root / "stop-denials"
                if denials.exists() and len(denials.read_text().splitlines()) >= 3:
                    ending = "stopped after repeated denials"
                elif time.monotonic() - began > args.timeout:
                    ending = "timeout"
                if ending:
                    os.killpg(proc.pid, signal.SIGTERM)
                    proc.wait(30)
        result["elapsed_s"] = time.monotonic() - began
        result["exit"] = proc.returncode
        result["ending"] = ending or ("boundary" if (box.root / "boundary").exists() else "exited")
        rows = ledger_rows(box.home)
        result["ledger"] = rows
        result["live_panels"] = len(live_panels(rows))
        result["boundary"] = (box.root / "boundary").exists()
        result["commits"] = git(work, "log", "--format=%H %s", "%s..HEAD" % start["base_sha"]).splitlines()
        (out / "final.diff").write_text(git(work, "diff", start["base_sha"]))
        # The author's own changes against the round-1 tree.
        r1 = box.root / "round1"
        run(["git", "worktree", "add", "-q", "--detach", str(r1), start["base_sha"]], work)
        run(["git", "apply", "--index", start["diff"]], r1)
        run(["git", "add", "-A"], work)
        tree = git(work, "write-tree").strip()
        run(["git", "add", "-A"], r1)
        r1tree = git(r1, "write-tree").strip()
        (out / "repair.diff").write_text(git(work, "diff", r1tree, tree))
        result["repair_stat"] = git(work, "diff", "--stat", r1tree, tree)
        for path in session_dir.glob("*.jsonl"):
            shutil.copy2(path, out / ("session-" + path.name))
        calls = box.root / "reviewer-calls"
        if calls.exists():
            shutil.copytree(calls, out / "reviewer-calls")
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        result["failures"].append(str(error))
    finally:
        if box:
            result["failures"] += box.leaks()
        if real_repo_state() != before:
            result["failures"].append("the real repository changed during the episode")
        result["passed"] = not result["failures"]
        (out / "result.json").write_text(json.dumps(result, indent=2, default=str))
        if args.keep:
            print("kept sandbox: %s" % root)
        else:
            shutil.rmtree(root, ignore_errors=True)
    print("%s: %s; %s" % (args.label, "ok" if result["passed"] else "FAIL", out / "result.json"))
    return 0 if result["passed"] else 1


def main():
    if sys.argv[1:2] == ["--reviewer"]:
        return reviewer(sys.argv[2], sys.argv[3:])
    if sys.argv[1:2] == ["--guard"]:
        return guard(*sys.argv[2:5])
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("build")
    p.add_argument("--rev", required=True)
    p.add_argument("--out", type=Path, required=True)
    p = sub.add_parser("episode")
    p.add_argument("--start", type=Path, required=True)
    p.add_argument("--steer-bin", type=Path, required=True)
    p.add_argument("--label", required=True)
    p.add_argument("--out", type=Path, required=True, help="new directory for the episode's evidence")
    p.add_argument("--timeout", type=int, default=5400, help="seconds before the author is stopped")
    p.add_argument("--model", required=True, help="the author model")
    p.add_argument("--max-turns", type=int, help="the author's turn cap")
    p.add_argument("--seed-only", action="store_true", help="stop after seeding round 1; no agent runs")
    p.add_argument("--keep", action="store_true", help="keep the sandbox, including copied logins")
    args = parser.parse_args()
    return build(args) if args.cmd == "build" else episode(args)


if __name__ == "__main__":
    sys.exit(main())
