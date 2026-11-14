#!/usr/bin/env python3
"""Merge a machine's content-named transcript prefixes into one file per session.

The commit gate used to store every firing's transcript prefix as its own
`<stem>-<sha16>.jsonl.gz`. It now keeps one `<stem>.jsonl.gz` per session, and
gate.ReadPrefix reads a row whose content-named file is gone from that session
file. This script converts the files written before that change.

For each session stem it takes the longest stored text that extends the session
file (when one exists), writes it as the session file, and removes each
content-named file whose bytes are an exact prefix of it. A file that is not a
prefix is left in place, and its rows keep reading it. A stem whose session file
lock is held is skipped.

Dry run by default; pass --apply to write. Run it while no coding agent is
committing on this machine.
"""

import argparse
import collections
import gzip
import os
import re
import sys
import tempfile

CONTENT_NAME = re.compile(r"^(.+)-[0-9a-f]{16}\.jsonl\.gz$")


def default_dir():
    return os.path.join(os.path.expanduser("~"), ".steer", "state", "commit-review", "prefixes")


def read(path):
    with gzip.open(path, "rb") as handle:
        return handle.read()


def write_session_file(path, text):
    fd, tmp = tempfile.mkstemp(prefix=".prefix-", dir=os.path.dirname(path))
    try:
        with os.fdopen(fd, "wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", compresslevel=6, mtime=0) as zw:
            zw.write(text)
        os.chmod(tmp, 0o644)
        if read(tmp) != text:
            raise RuntimeError("the written file does not read back as the merged text")
        os.replace(tmp, path)
    except BaseException:
        if os.path.exists(tmp):
            os.remove(tmp)
        raise


def plan_stem(directory, stem, names):
    """Return (target text or None when unchanged, removable names, kept names)."""
    session = os.path.join(directory, stem + ".jsonl.gz")
    current = read(session) if os.path.exists(session) else None
    texts = {name: read(os.path.join(directory, name)) for name in names}
    candidates = [t for t in texts.values() if current is None or t.startswith(current)]
    if current is not None:
        candidates.append(current)
    target = max(candidates, key=len)
    removable = [n for n, t in texts.items() if target.startswith(t)]
    kept = [n for n in names if n not in removable]
    return (None if target == current else target), removable, kept


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dir", default=default_dir(), help="the prefixes directory (default: %(default)s)")
    parser.add_argument("--apply", action="store_true", help="write the session files and remove merged files")
    args = parser.parse_args()

    if not os.path.isdir(args.dir):
        print(f"no prefixes directory at {args.dir}")
        return 0

    stems = collections.defaultdict(list)
    for name in sorted(os.listdir(args.dir)):
        match = CONTENT_NAME.match(name)
        if match:
            stems[match.group(1)].append(name)

    before = after = 0
    merged = removed = skipped = diverged = 0
    for stem, names in sorted(stems.items()):
        session = os.path.join(args.dir, stem + ".jsonl.gz")
        if os.path.exists(session + ".lock"):
            print(f"skip {stem}: a gate holds its lock")
            skipped += 1
            continue
        sizes = {n: os.path.getsize(os.path.join(args.dir, n)) for n in names}
        session_size = os.path.getsize(session) if os.path.exists(session) else 0
        before += sum(sizes.values()) + session_size
        try:
            target, removable, kept = plan_stem(args.dir, stem, names)
        except (OSError, EOFError, gzip.BadGzipFile) as err:
            print(f"skip {stem}: {err}")
            skipped += 1
            after += sum(sizes.values()) + session_size
            continue
        diverged += len(kept)
        if args.apply:
            if target is not None:
                write_session_file(session, target)
            for name in removable:
                os.remove(os.path.join(args.dir, name))
        if target is not None:
            merged += 1
        removed += len(removable)
        new_session = session_size
        if args.apply:
            new_session = os.path.getsize(session)
        elif target is not None:
            new_session = max([sizes[n] for n in removable] + [session_size])
        after += new_session + sum(sizes[n] for n in kept)

    mode = "applied" if args.apply else "dry run -- pass --apply to write"
    estimate = "" if args.apply else " (estimated)"
    print(f"{len(stems)} sessions: {merged} session files written, {removed} files merged, "
          f"{diverged} not a prefix and kept, {skipped} skipped")
    print(f"{before / 1e6:.1f} MB -> {after / 1e6:.1f} MB{estimate}; {mode}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
