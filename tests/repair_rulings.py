#!/usr/bin/env python3
"""Ask the author model how it would rule on a frozen round-1 review under one deny text.

Each run stages the start's round-1 diff on its base_sha in a sandbox clone and gives
the author, with read-only tools, the user's task turns, its own commit command and the
deny text. The author returns one ruling per distinct concern and edits nothing. Runs
write <out>/<label>-r<n>/ with the stream, the parsed rulings and the usage.

A start is a JSON file: {"session_id", "base_sha", "diff", "bundle", "prefix",
"prefix_lines", "chain", "tasks"}: the round-1 diff, frozen review bundle, saved session
prefix and task-turn file from the commit-review state directory, and the chain id. Claude Code needs CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY.
"""

import argparse
import glob
import gzip
import json
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from sandbox import REPO, Sandbox

STATE = Path.home() / ".steer" / "state" / "commit-review"
REAL_PATHS = ("/Volumes/MiniEx/projects/steer-skill", "/Users/jeremy/MiniEx/projects/steer-skill")
DENY_PREFIX = "PreToolUse:Bash hook error: "
INSTRUCTIONS = """This is an evaluation of how you adjudicate a commit-gate review. Do not edit files and do not commit; your tools are read-only.

You are the author of the change staged in this repository ({work}). `git diff --cached` is not available to you, so the staged change is in {diff}.

The user's requests in this session, oldest first:
{tasks}

You then ran this commit command:

{command}

The commit gate blocked it with this message:

{deny}

Investigate as the message asks. Then end your reply with one fenced ```json block holding a list with one object per distinct concern:
{{"concern": "<what it claims, one clause>", "ruling": "fixed" | "accepted_not_fixed" | "rejected", "change": "<the repair you would make, or empty>", "reason": "<why>"}}
"fixed" means you would change code or documentation before retrying."""


def round1_ts(chain):
    for path in glob.glob(str(STATE / "adjudication.*.jsonl")):
        for line in open(path):
            if chain in line:
                d = json.loads(line)
                if d.get("chain_id") == chain and d.get("firing") == 1 and d.get("phase") == "deny":
                    return d["ts"]
    raise ValueError("no round-1 deny for chain %s" % chain)


def commit_command(start, work):
    """The Bash command whose round-1 deny follows prefix_lines in the saved session,
    with the repository path rewritten to work."""
    with gzip.open(start["prefix"], "rt") as fh:
        rows = [json.loads(line) for line in fh if line.strip()]
    commands = {}
    for row in rows:
        content = (row.get("message") or {}).get("content")
        if not isinstance(content, list):
            continue
        for c in content:
            if c.get("type") == "tool_use":
                commands[c.get("id")] = (c.get("input") or {}).get("command", "")
    for row in rows[start["prefix_lines"]:]:
        content = (row.get("message") or {}).get("content")
        if row.get("type") != "user" or not isinstance(content, list):
            continue
        for c in content:
            if (c.get("type") == "tool_result" and str(c.get("content", "")).startswith(DENY_PREFIX)
                    and "red-teamed this commit" in str(c.get("content"))):
                command = commands[c["tool_use_id"]]
                for real in REAL_PATHS:
                    command = command.replace(real, str(work))
                return command
    raise ValueError("no round-1 deny after prefix line %d" % start["prefix_lines"])


JSON_BLOCK = re.compile(r"^```json[ \t]*\n(.*?)^```[ \t]*$", re.MULTILINE | re.DOTALL)


def last_json_block(text):
    """The last ```json block, closed by a fence on its own line, so backticks inside
    a JSON string do not end it."""
    blocks = JSON_BLOCK.findall(text)
    if not blocks:
        return None
    try:
        return json.loads(blocks[-1])
    except json.JSONDecodeError:
        return None


def run_one(args, start, out):
    out.mkdir(parents=True, exist_ok=False)
    root = Path(tempfile.mkdtemp(prefix="steer-rulings-"))
    record = {"label": args.label, "model": args.model, "failures": []}
    try:
        box = Sandbox(root)
        env = box.env()
        work = box.root / "steer-skill"
        command = commit_command(start, work)
        if "--amend" in command:
            raise ValueError("an amend round's diff is against HEAD^, so it cannot be staged on base_sha")
        subprocess.run(["git", "clone", "-q", "--shared", "--no-checkout", str(REPO), str(work)], check=True, env=env)
        subprocess.run(["git", "-C", str(work), "checkout", "-q", "--detach", start["base_sha"]], check=True, env=env)
        subprocess.run(["git", "-C", str(work), "apply", "--index", start["diff"]], check=True, env=env)
        diff = work / ".round1.diff"
        shutil.copy2(start["diff"], diff)
        cutoff = round1_ts(start["chain"])
        turns = [json.loads(l) for l in open(start["tasks"]) if l.strip()]
        tasks = "\n".join("- " + t["text"] for t in turns if t["ts"] < cutoff)
        prompt = INSTRUCTIONS.format(work=work, diff=diff, tasks=tasks, command=command,
                                     deny=Path(args.deny).read_text())
        (out / "prompt.txt").write_text(prompt)
        argv = ["claude", "-p", "--model", args.model, "--tools", "Read,Grep,Glob",
                "--max-turns", str(args.max_turns), "--output-format", "stream-json", "--verbose",
                "--no-session-persistence"]
        with open(out / "stream.jsonl", "w") as so:
            r = subprocess.run(argv, input=prompt, cwd=work, env=env, stdout=so, stderr=subprocess.PIPE,
                               text=True, timeout=args.timeout)
        record["exit"] = r.returncode
        final = None
        for line in open(out / "stream.jsonl"):
            try:
                d = json.loads(line)
            except json.JSONDecodeError:
                continue
            if d.get("type") == "result":
                final = d
        if not final:
            record["failures"].append("no result event; stderr: " + r.stderr[-1000:])
        else:
            record.update(subtype=final.get("subtype"), cost_usd=final.get("total_cost_usd"),
                          turns=final.get("num_turns"), usage=final.get("usage"),
                          duration_s=(final.get("duration_ms") or 0) / 1000)
            record["rulings"] = last_json_block(final.get("result") or "")
            (out / "reply.md").write_text(final.get("result") or "")
            if record["rulings"] is None:
                record["failures"].append("no parseable json block")
        record["failures"] += box.leaks()
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        record["failures"].append(str(error))
    finally:
        shutil.rmtree(root, ignore_errors=True)
        (out / "result.json").write_text(json.dumps(record, indent=2))
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--start", type=Path, required=True)
    parser.add_argument("--deny", type=Path, required=True, help="the arm's rendered deny text")
    parser.add_argument("--label", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--reps", type=int, default=1)
    parser.add_argument("--max-turns", type=int, default=20)
    parser.add_argument("--timeout", type=int, default=900)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    start = json.loads(args.start.read_text())
    failed = 0
    for n in range(1, args.reps + 1):
        rec = run_one(args, start, args.out.resolve() / ("%s-r%d" % (args.label, n)))
        rulings = rec.get("rulings") or []
        print("%s r%d: %s turns=%s cost=$%s %s" % (args.label, n, "ok" if not rec["failures"] else "FAIL",
              rec.get("turns"), rec.get("cost_usd"), [x.get("ruling") for x in rulings]), flush=True)
        failed += bool(rec["failures"])
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
