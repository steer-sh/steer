#!/usr/bin/env python3
"""Replay recorded commit-gate rounds through a chosen reviewer panel.

  rounds  Write the reviewed rounds of named chains from the adjudication ledger
          as a TSV: chain, firing, base_sha, diff_file, subject.
  run     Stage each round's saved diff on its base commit in a clone of --repo
          and run cli/tools/replay-review over it in a sandbox home. Writes
          <out>/<tag>-<chain>-r<firing>.json. The ledger keeps only the
          subject, so the panel reviews without the commit body. The global
          review addendum is the operator's current one, not the round's. Claude Code
          needs an exported sandbox login, as in tests/sandbox.py.
  tally   Count, per output, which panel runs matched each marker regex in a
          markers JSON {"accepted": {name: regex}, "rejected": {name: regex}}.
"""

import argparse
import codecs
import glob
import itertools
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path

from sandbox import REPO, Sandbox

STATE = Path.home() / ".steer" / "state" / "commit-review"
REVIEW_PHASES = ("deny", "clean", "cap")


def chain_rounds(state, chains):
    """The reviewed rounds of chains, in chain then firing order."""
    rows = []
    for path in sorted(glob.glob(str(Path(state) / "adjudication.*.jsonl"))):
        if ".bak." in path or "premigrate" in path:
            continue
        with open(path) as fh:
            for line in fh:
                if not line.strip():
                    continue
                d = json.loads(line)
                if d.get("chain_id") in chains and d.get("phase") in REVIEW_PHASES and d.get("diff_file"):
                    if not d.get("base_sha"):
                        # A repository's first commit has no HEAD to record.
                        print("rounds: %s r%s has no base_sha; skipped" % (d["chain_id"], d["firing"]),
                              file=sys.stderr)
                        continue
                    rows.append((d["chain_id"], int(d["firing"]), d["base_sha"], d["diff_file"],
                                 " ".join((d.get("subject") or "").split())))
    order = {c: i for i, c in enumerate(chains)}
    return sorted(set(rows), key=lambda r: (order[r[0]], r[1]))


def read_rounds(path):
    with open(path) as fh:
        return [line.rstrip("\n").split("\t") for line in fh if line.strip()]


def output_name(tag, chain, firing):
    return "%s-%s-r%s.json" % (tag, chain, firing)


# A stage() candidate resolved to the empty tree in the repository's own object format.
EMPTY_TREE = "empty-tree"
BINARY_RE = re.compile(r"^Binary files (.+) and (.+) differ$", re.M)


def unquote_path(side):
    """A diff header path with Git's C-style quoting, as core.quotePath writes a
    non-ASCII or special name, removed."""
    if len(side) > 1 and side[0] == side[-1] == '"':
        return codecs.escape_decode(side[1:-1].encode("utf-8"))[0].decode("utf-8", "surrogateescape")
    return side


def binary_paths(diff_text):
    """The paths of a diff's binary sections, which carry no applicable patch."""
    paths = set()
    for m in BINARY_RE.finditer(diff_text):
        for side in map(unquote_path, m.groups()):
            if side[:2] in ("a/", "b/"):
                paths.add(side[2:])
    return sorted(paths)


def stage(repo, wt, base, diff, env, exclude=()):
    """Stage diff in a clone of repo at base, else at base's parent, else (for a root
    base) on the empty tree, named by its object id: a ledger row names HEAD as base_sha, and an amend's diff
    is against HEAD^. A diff that applies to base is taken as a plain commit; rows
    carry no amend flag. HEAD ends at base, as the live gate saw it. exclude names
    paths left unstaged. Returns the revision the diff was applied to, or None."""
    subprocess.run(["git", "clone", "-q", "--shared", "--no-checkout", str(repo), str(wt)], check=True, env=env)
    git = ["git", "-C", str(wt)]
    # --exclude takes a wildmatch pattern; escape so it names exactly one path.
    excludes = ["--exclude=" + re.sub(r"([][*?\\])", r"\\\1", p) for p in exclude]
    root = False
    for rev in (base, base + "^", EMPTY_TREE):
        if rev == EMPTY_TREE:
            if not root:
                break
            subprocess.run(git + ["checkout", "-q", "--force", "--detach", base], check=True, env=env)
            empty = subprocess.run(git + ["hash-object", "-t", "tree", "--stdin"], input="", text=True,
                                   check=True, env=env, capture_output=True).stdout.strip()
            subprocess.run(git + ["read-tree", "--empty"], check=True, env=env)
            subprocess.run(git + ["clean", "-fdxq"], check=True, env=env)
        elif subprocess.run(git + ["checkout", "-q", "--force", "--detach", rev],
                            env=env, capture_output=True).returncode:
            root = rev != base
            continue
        check = subprocess.run(git + ["apply", "--check", "--index"] + excludes + [str(diff)],
                               env=env, capture_output=True)
        if check.returncode == 0:
            subprocess.run(git + ["apply", "--index"] + excludes + [str(diff)], check=True, env=env)
            if rev == EMPTY_TREE:
                return empty
            if rev != base:
                subprocess.run(git + ["reset", "-q", "--soft", base], check=True, env=env)
            return rev
    return None


def elision(diff_text):
    """What the gate's diff budget left out of a saved diff, or "" when nothing.
    The notices precede the first file header, in either order."""
    preamble = diff_text.split("diff --git ", 1)[0]
    if "[diff is " in preamble:
        return "stat only"
    if "[The review harness dropped" in preamble:
        return "dropped files absent from the staged tree"
    return ""


def run(args):
    rounds = read_rounds(args.rounds)
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    taken = [output_name(args.tag, c, n) for c, n, *_ in rounds if (out / output_name(args.tag, c, n)).exists()]
    if taken:
        raise SystemExit("replay: %s already holds %s; pick a new --tag" % (out, ", ".join(taken)))
    root = Path(tempfile.mkdtemp(prefix="steer-replay-"))
    box = None
    failed = 0
    try:
        tool = root / "replay-review"
        subprocess.run(["go", "build", "-o", str(tool), "./tools/replay-review"], cwd=REPO / "cli", check=True)
        box = Sandbox(root, codex=args.provider == "codex", agy=args.provider == "agy")
        # The gate reads the global review addendum from the home it runs under: the
        # neutral store, or the legacy one on a machine that never migrated.
        for store in (Path(".steer") / "hooks", Path(".claude") / "hooks"):
            manifest = Path.home() / store / "steer-manifest.json"
            if manifest.exists():
                (box.home / store).mkdir(parents=True, exist_ok=True)
                shutil.copy2(manifest, box.home / store / manifest.name)
                break
        env = box.env(claude=args.provider == "claude-code")
        env = {k: v for k, v in env.items()
               if not k.startswith(("STEER_", "COMMIT_GATE_", "COMMIT_REVIEW_", "GIT_"))}
        env.update(GIT_CONFIG_GLOBAL=str(box.gitconfig), GIT_CONFIG_NOSYSTEM="1")
        diffs = args.diffs.resolve()
        for chain, firing, base, diff_file, subject in rounds:
            label = "%s r%s" % (chain, firing)
            diff = diffs / diff_file
            text = diff.read_text(errors="replace")
            elided = elision(text)
            binaries = binary_paths(text)
            if elided == "stat only":
                failed += 1
                print("%s skipped: the saved diff is %s" % (label, elided), file=sys.stderr)
                continue
            wt = root / "work" / ("%s-r%s" % (chain, firing))
            rev = stage(args.repo.resolve(), wt, base, diff, env, binaries)
            if rev is None:
                failed += 1
                print("%s skipped: the saved diff applies neither to %s nor to its parent"
                      % (label, base[:12]), file=sys.stderr)
                continue
            notes = [n for n in (elided,
                                 "staged on base_sha^" if rev == base + "^" else "",
                                 "staged on the empty tree" if rev not in (base, base + "^") else "",
                                 "%d binary file(s) unstaged" % len(binaries) if binaries else "") if n]
            result = subprocess.run(
                [str(tool), "--provider", args.provider, "--model", args.model, "--effort", args.effort,
                 "--runs", str(args.runs), "--base", rev, str(wt), str(diff), subject,
                 str(out / output_name(args.tag, chain, firing))],
                cwd=wt, env=env, text=True, capture_output=True)
            print("%s %s%s" % (label, result.stdout.strip(), "".join(" [%s]" % n for n in notes)), flush=True)
            if result.returncode:
                failed += 1
                print(result.stderr.strip()[-1500:], file=sys.stderr)
    finally:
        leaks = box.leaks() if box else []
        shutil.rmtree(root, ignore_errors=True)
    for leak in leaks:
        print("replay: " + leak, file=sys.stderr)
    return 1 if failed or leaks else 0


def run_hits(texts, pattern):
    """The 1-based panel runs whose text matches pattern."""
    return [i + 1 for i, t in enumerate(texts) if re.search(pattern, t or "", re.I)]


def panel_curve(cells, runs):
    """Expected count of cells a panel of k runs drawn from runs would catch, k = 1..runs.
    A cell is the set of 1-based runs that caught one marker in one round."""
    curve = []
    for k in range(1, runs + 1):
        subsets = [set(s) for s in itertools.combinations(range(1, runs + 1), k)]
        curve.append(sum(sum(1 for s in subsets if s & cell) / len(subsets) for cell in cells))
    return curve


def tally(args):
    markers = json.loads(Path(args.markers).read_text())
    by_tag = {}
    for path in sorted(glob.glob(str(args.out / "*-*-r*.json"))):
        tag, chain, firing = Path(path).stem.rsplit("-", 2)
        if not args.tags or tag in args.tags:
            d = json.loads(Path(path).read_text())
            by_tag.setdefault((tag, len(d["texts"])), []).append((chain, firing, d))
    for (tag, runs), rows in by_tag.items():
        print("== %s  rounds=%d runs=%d blocked=%d/%d median_s=%.0f" % (
            tag, len(rows), runs, sum(d["verdict"] == "deny" for _, _, d in rows), len(rows),
            statistics.median(d["elapsed_s"] for _, _, d in rows)))
        cells, found = [], set()
        for chain, firing, d in rows:
            lost = len(d["texts"]) - d["answered"]
            for kind in ("accepted", "rejected"):
                for name, pattern in markers.get(kind, {}).items():
                    hits = run_hits(d["texts"], pattern)
                    if hits:
                        print("  %s %s %-9s %-24s runs %s of %d%s" % (
                            chain, firing, kind, name, hits, len(d["texts"]), " (%d lost)" % lost if lost else ""))
                        if kind == "accepted":
                            cells.append(set(hits))
                            found.add(name)
        print("  accepted markers found: %d of %d" % (len(found), len(markers.get("accepted", {}))))
        if cells:
            print("  expected cells by panel size: " + "  ".join(
                "%d:%.2f" % (k + 1, v) for k, v in enumerate(panel_curve(cells, runs))) + " of %d" % len(cells))
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("rounds")
    p.add_argument("chains", nargs="+")
    p.add_argument("--state", type=Path, default=STATE)
    p = sub.add_parser("run")
    p.add_argument("--rounds", type=Path, required=True)
    p.add_argument("--repo", type=Path, required=True, help="the repository the chains committed to")
    p.add_argument("--provider", required=True, choices=("agy", "claude-code", "codex"))
    p.add_argument("--model", required=True)
    p.add_argument("--effort", required=True, help="'' for the provider's own")
    p.add_argument("--runs", type=int, default=3)
    p.add_argument("--tag", required=True)
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--diffs", type=Path, default=STATE / "diffs")
    p = sub.add_parser("tally")
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--markers", type=Path, required=True)
    p.add_argument("--tags", nargs="*")
    args = parser.parse_args(argv)
    if args.cmd == "rounds":
        for row in chain_rounds(args.state, args.chains):
            print("\t".join(map(str, row)))
        return 0
    return run(args) if args.cmd == "run" else tally(args)


if __name__ == "__main__":
    sys.exit(main())
