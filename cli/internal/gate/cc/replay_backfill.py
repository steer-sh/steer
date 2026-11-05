#!/usr/bin/env python3
"""Differential-replay driver: resolve a copied shard with the Python resolver.

backfill_ledger.py has no state-dir override of its own -- it inherits STATE_DIR from
the hardcoded commit-review.py -- so this driver points its LOG, STATE_DIR and LOCK at a
temp copy of the shard while leaving the transcript harvest on the real
~/.claude/projects. It then rewrites that copy in place with --apply.

TestReplayBackfill (Go) seeds two copies of the real shard, runs this driver on one and
the Go resolver on the other over the same transcripts, and compares the resolved rows
by-parse. The Go test drives it; it is never run at turn end.

Usage: replay_backfill.py --state <dir> [--reresolve]
"""
import argparse
import importlib.util
import json
import pathlib
import sys


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--state", required=True)
    ap.add_argument("--reresolve", action="store_true")
    args = ap.parse_args()

    home = pathlib.Path.home()
    bl_path = home / ".claude" / "hooks" / "commit-review" / "backfill_ledger.py"
    bl = load(str(bl_path), "backfill_ledger")

    state = pathlib.Path(args.state)
    # cr.LOG_FILE names THIS machine's shard; the resolver rules on it and no other.
    log = state / bl.cr.LOG_FILE.name
    # Repoint the resolver's writes at the copy; PROJECTS stays on real transcripts.
    bl.LOG = log
    bl.cr.LOG_FILE = log
    bl.cr.STATE_DIR = state
    bl.LOCK = state / "backfill.lock"

    raw = log.read_text().splitlines()
    rows = [json.loads(x) for x in raw if x.strip()]
    if args.reresolve:
        bl.reopen(rows)
    bl.resolve_and_write(rows, len(raw), True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
