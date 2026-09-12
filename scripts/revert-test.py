#!/usr/bin/env python3
"""Revert-test check: each Go test added by the unpushed change must notice the change.

The change is the working tree against the merge base with the upstream branch
(HEAD when there is none). The script builds variants of the change in a copy of
the module and runs the new top-level test functions under each one:

  revert  one changed block of non-test code put back to its base version
  panic   panic("revert-test") inserted as the first statement of a changed function

A new test that fails under at least one variant notices the change. A test that
runs under variants and never fails does not test the change: it is reported and
the script exits 1. A test whose doc comment carries MARKER is exempt.
"""

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time

MODULE = "cli"
MARKER = "// pins existing behaviour"
TEST_FUNC = re.compile(r"^func (Test\w+)\(\w+ \*testing\.T\)", re.MULTILINE)
HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")
PANIC = '\tpanic("revert-test")\n'


def git(*args, check=True):
    result = subprocess.run(["git", *args], capture_output=True, text=True)
    if check and result.returncode != 0:
        raise RuntimeError("git %s: %s" % (" ".join(args), result.stderr.strip()))
    return result.stdout


def base_revision():
    upstream = subprocess.run(["git", "merge-base", "HEAD", "@{upstream}"],
                              capture_output=True, text=True)
    if upstream.returncode == 0 and upstream.stdout.strip():
        return upstream.stdout.strip()
    return git("rev-parse", "HEAD").strip()


def is_test_side(path):
    return path.endswith("_test.go") or "/testdata/" in path


def changed_paths(base):
    tracked = git("diff", "--name-only", "--no-ext-diff", base, "--", MODULE).split()
    untracked = git("ls-files", "--others", "--exclude-standard", "--", MODULE).split()
    return sorted(p for p in set(tracked) | set(untracked) if os.path.isfile(p)), set(untracked)


def marked(source, func_start):
    lines = source[:func_start].splitlines()
    while lines and lines[-1].lstrip().startswith("//"):
        if lines[-1].strip() == MARKER:
            return True
        lines.pop()
    return False


def new_tests(base, path):
    with open(path, encoding="utf-8") as handle:
        current = handle.read()
    old = git("show", "%s:%s" % (base, path), check=False)
    before = set(TEST_FUNC.findall(old))
    return [m.group(1) for m in TEST_FUNC.finditer(current)
            if m.group(1) not in before and not marked(current, m.start())]


def is_trivia(lines):
    return all(not l.strip() or l.strip().startswith("//") for l in lines)


def hunks(base, path):
    """Yield (new_start, new_count, old_lines, new_lines) for each changed block."""
    diff = git("diff", "-U0", "--no-color", "--no-ext-diff", base, "--", path)
    current = None
    for line in diff.splitlines():
        match = HUNK.match(line)
        if match:
            if current:
                yield current
            current = (int(match.group(3)), int(match.group(4) or 1), [], [])
        elif current and line.startswith("-") and not line.startswith("---"):
            current[2].append(line[1:] + "\n")
        elif current and line.startswith("+") and not line.startswith("+++"):
            current[3].append(line[1:] + "\n")
    if current:
        yield current


def revert(lines, start, count, old):
    at = start if count == 0 else start - 1
    return lines[:at] + old + lines[at + count:]


def code(line):
    return re.sub(r"\s*//[^\"`]*$", "", line).rstrip()


def changed_functions(lines, touched):
    """Yield the opening-brace line index of each top-level function whose span holds a touched line."""
    i = 0
    while i < len(lines):
        if lines[i].startswith("func "):
            start = i
            while i < len(lines) and not code(lines[i]).endswith("{"):
                if code(lines[i]).endswith("}"):
                    break
                i += 1
            if i < len(lines) and code(lines[i]).endswith("{"):
                open_at = i
                while i < len(lines) and code(lines[i]) != "}":
                    i += 1
                if any(start <= t <= i for t in touched):
                    yield open_at
        i += 1


def variants(base, paths, untracked):
    """Return (path, mutated_lines) for every variant of the change, panics first."""
    panics, reverts = [], []
    for path in paths:
        if not path.endswith(".go") or is_test_side(path):
            continue
        with open(path, encoding="utf-8") as handle:
            lines = handle.readlines()
        touched = set(range(len(lines))) if path in untracked else set()
        if path not in untracked:
            for start, count, old, new in hunks(base, path):
                if is_trivia(old) and is_trivia(new):
                    continue
                touched.update(range(start - 1, start - 1 + count) if count else (start - 1, start))
                reverts.append((path, revert(lines, start, count, old)))
        for open_at in changed_functions(lines, touched):
            panics.append((path, lines[:open_at + 1] + [PANIC] + lines[open_at + 1:]))
    return panics + reverts


def import_path(prefix, path):
    package = os.path.relpath(os.path.dirname(path), MODULE)
    return prefix if package == "." else "%s/%s" % (prefix, package)


def test_deps(prefix, packages):
    """Map each package to the set of import paths linked into its test binary."""
    deps = {}
    for package in packages:
        listed = subprocess.run(["go", "list", "-test", "-deps", "./" + package],
                                cwd=MODULE, capture_output=True, text=True, check=True)
        deps[package] = {line.split(" ")[0] for line in listed.stdout.splitlines()}
    return deps


def change_key(base, name_status, paths):
    digest = hashlib.sha256()
    with open(__file__, "rb") as handle:
        digest.update(handle.read())
    digest.update(base.encode() + name_status.encode())
    for path in paths:
        digest.update(path.encode() + b"\0")
        with open(path, "rb") as handle:
            digest.update(handle.read())
    return digest.hexdigest()


def run_tests(module_dir, prefix, by_package):
    """Map (package, test) to pass or fail. A panic ends its package's binary, so
    tests left without an outcome are run again until none makes progress."""
    outcome, pending = {}, {p: list(ns) for p, ns in by_package.items()}
    while pending:
        found = run_once(module_dir, prefix, pending)
        if not found:
            break
        outcome.update(found)
        pending = {p: [n for n in ns if (p, n) not in outcome] for p, ns in pending.items()}
        pending = {p: ns for p, ns in pending.items() if ns}
    return outcome


def run_once(module_dir, prefix, by_package):
    names = sorted({n for ns in by_package.values() for n in ns})
    run = subprocess.run(
        ["go", "test", "-count=1", "-vet=off", "-json", "-run", "^(%s)$" % "|".join(names)]
        + ["./" + p for p in sorted(by_package)],
        cwd=module_dir, capture_output=True, text=True)
    outcome = {}
    for line in run.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        test = event.get("Test")
        if event.get("Action") in ("pass", "fail") and test and "/" not in test:
            package = os.path.relpath(event.get("Package", ""), prefix) if prefix else ""
            if test in by_package.get(package, ()):
                outcome[(package, test)] = event["Action"]
    return outcome


def main():
    root = git("rev-parse", "--show-toplevel").strip()
    os.chdir(root)
    base = base_revision()
    paths, untracked = changed_paths(base)

    by_package = {}
    for path in paths:
        if path.endswith("_test.go"):
            names = new_tests(base, path)
            if names:
                package = os.path.relpath(os.path.dirname(path), MODULE)
                by_package.setdefault(package, []).extend(names)
    if not by_package:
        print("revert-test: no new test functions since %s" % base[:8])
        return 0

    stamp = os.path.join(git("rev-parse", "--absolute-git-dir").strip(), "revert-test.pass")
    key = change_key(base, git("diff", "--name-status", "--no-ext-diff", base, "--", MODULE), paths)
    try:
        with open(stamp, encoding="utf-8") as handle:
            if handle.read().strip() == key:
                return 0
    except OSError:
        pass

    with open(os.path.join(MODULE, "go.mod"), encoding="utf-8") as handle:
        prefix = re.search(r"^module (\S+)", handle.read(), re.MULTILINE).group(1)
    deps = test_deps(prefix, by_package)
    started = time.time()
    with tempfile.TemporaryDirectory(prefix="revert-test-") as tmp:
        module_dir = os.path.join(tmp, MODULE)
        shutil.copytree(MODULE, module_dir, symlinks=True)

        baseline = run_tests(module_dir, prefix, by_package)
        live = {k for k, v in baseline.items() if v == "pass"}
        if not live:
            print("revert-test: no new test passes on the change itself; nothing to judge")
            return 0

        changes = variants(base, paths, untracked)
        mutated_packages = {import_path(prefix, path) for path, _ in changes}
        unreachable = {(p, n) for p, n in live if not mutated_packages & deps[p]}
        killed, ran, built = set(), set(), 0
        for path, mutated in changes:
            if killed | unreachable == live:
                break
            imported = import_path(prefix, path)
            reachable = {p: [n for n in ns if (p, n) in live and (p, n) not in killed]
                         for p, ns in by_package.items() if imported in deps[p]}
            reachable = {p: ns for p, ns in reachable.items() if ns}
            if not reachable:
                continue
            target = os.path.join(tmp, path)
            with open(target, encoding="utf-8") as handle:
                original = handle.read()
            with open(target, "w", encoding="utf-8") as handle:
                handle.writelines(mutated)
            outcome = run_tests(module_dir, prefix, reachable)
            with open(target, "w", encoding="utf-8") as handle:
                handle.write(original)
            outcome = {k: v for k, v in outcome.items() if k in live}
            if not outcome:
                continue
            built += 1
            ran.update(outcome)
            killed.update(k for k, v in outcome.items() if v == "fail")

    blind = sorted((ran - killed) | unreachable)
    silent = sorted(live - ran - unreachable)
    print("revert-test: base %s, %d new test(s), %d variant(s) built, %.0fs"
          % (base[:8], len(live), built, time.time() - started))
    for package, test in silent:
        print("  no signal (no variant built and ran it): %s: %s" % (package, test))
    if blind:
        print("revert-test: these new tests pass under every variant, or link none of "
              "the changed code, so they do not test the change:", file=sys.stderr)
        for package, test in blind:
            print("  %s: %s" % (package, test), file=sys.stderr)
        print("Make each test exercise the change. If a test deliberately pins behaviour "
              "that already existed, put `%s` in its doc comment; the commit reviewer "
              "sees that claim." % MARKER, file=sys.stderr)
        return 1
    with open(stamp, "w", encoding="utf-8") as handle:
        handle.write(key)
    return 0


if __name__ == "__main__":
    sys.exit(main())
