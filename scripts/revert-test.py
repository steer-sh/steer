#!/usr/bin/env python3
"""Revert-test check: each Go test added by the unpushed change must notice the change.

The change is the working tree against the merge base with the upstream branch
(HEAD when there is none). The script builds variants of the change in a copy of
the repository and runs the new top-level test functions under each one:

  zero    a changed function returns the zero values of its results at once
  revert  one changed block of non-test code put back to its base version

A new test that fails under at least one variant notices the change. Every other
new test is reported and the script exits 1: a test that fails before any
variant, one that no variant built and ran, and one that passes under every
variant. Panic variants run only for the reported tests, to say whether each one
reaches the changed functions. A test whose doc comment carries MARKER is exempt.
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
PANIC = 'panic("revert-test")'
FUNCS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "revert-test-funcs.go")


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


def functions(paths):
    """Map each Go file to its top-level functions, as revert-test-funcs.go prints them."""
    if not paths:
        return {}
    listed = subprocess.run(["go", "run", FUNCS], input="\n".join(paths),
                            capture_output=True, text=True)
    if listed.returncode != 0:
        raise RuntimeError("revert-test-funcs: %s" % listed.stderr.strip())
    return json.loads(listed.stdout)


def insert(data, offset, statement):
    return data[:offset] + ("\n\t%s\n" % statement).encode() + data[offset:]


def variants(base, paths, untracked):
    """Return (behavioural, panics): lists of (path, mutated bytes), zero variants first."""
    sources = [p for p in paths if p.endswith(".go") and not is_test_side(p)]
    funcs = functions(sources)
    zeros, reverts, panics = [], [], []
    for path in sources:
        with open(path, "rb") as handle:
            data = handle.read()
        lines = data.decode("utf-8").splitlines(keepends=True)
        touched = set(range(1, len(lines) + 1)) if path in untracked else set()
        if path not in untracked:
            for start, count, old, new in hunks(base, path):
                if is_trivia(old) and is_trivia(new):
                    continue
                touched.update(range(start, start + count) if count else (start, start + 1))
                reverts.append((path, "".join(revert(lines, start, count, old)).encode()))
        for fn in funcs[path]:
            if any(fn["first"] <= t <= fn["last"] for t in touched):
                zeros.append((path, insert(data, fn["body"], fn["zero"])))
                panics.append((path, insert(data, fn["body"], PANIC)))
    return zeros + reverts, panics


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
    for script in (__file__, FUNCS):
        with open(script, "rb") as handle:
            digest.update(handle.read())
    digest.update(base.encode() + name_status.encode())
    for path in paths:
        digest.update(path.encode() + b"\0")
        with open(path, "rb") as handle:
            digest.update(handle.read())
    return digest.hexdigest()


def copy_repository(root, tmp):
    """Copy the module into tmp and link every other top-level entry, so tests that
    read files beside the module find them."""
    shutil.copytree(os.path.join(root, MODULE), os.path.join(tmp, MODULE), symlinks=True)
    for entry in os.listdir(root):
        if entry not in (MODULE, ".git"):
            os.symlink(os.path.join(root, entry), os.path.join(tmp, entry))


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


def judge(tmp, module_dir, prefix, by_package, deps, changes, targets):
    """Run targets under each variant. Return (ran, failed, built): the tests that
    ran under a built variant, those that failed under one, and the built count."""
    failed, ran, built = set(), set(), 0
    for path, mutated in changes:
        if failed >= targets:
            break
        imported = import_path(prefix, path)
        reachable = {p: [n for n in ns if (p, n) in targets and (p, n) not in failed]
                     for p, ns in by_package.items() if imported in deps[p]}
        reachable = {p: ns for p, ns in reachable.items() if ns}
        if not reachable:
            continue
        target = os.path.join(tmp, path)
        with open(target, "rb") as handle:
            original = handle.read()
        with open(target, "wb") as handle:
            handle.write(mutated)
        outcome = run_tests(module_dir, prefix, reachable)
        with open(target, "wb") as handle:
            handle.write(original)
        outcome = {k: v for k, v in outcome.items() if k in targets}
        if not outcome:
            continue
        built += 1
        ran.update(outcome)
        failed.update(k for k, v in outcome.items() if v == "fail")
    return ran, failed, built


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
    selected = {(p, n) for p, ns in by_package.items() for n in ns}
    started = time.time()
    reasons = {}
    with tempfile.TemporaryDirectory(prefix="revert-test-") as tmp:
        copy_repository(root, tmp)
        module_dir = os.path.join(tmp, MODULE)

        baseline = run_tests(module_dir, prefix, by_package)
        for test in selected:
            if baseline.get(test) != "pass":
                reasons[test] = ("fails in the copy of the repository before any variant (%s)"
                                 % baseline.get(test, "no result"))
        live = selected - set(reasons)

        behavioural, panics = variants(base, paths, untracked)
        mutated_packages = {import_path(prefix, path) for path, _ in behavioural + panics}
        for test in live:
            if not mutated_packages & deps[test[0]]:
                reasons[test] = "its test binary links none of the changed packages"
        targets = live - set(reasons)
        ran, killed, built = judge(tmp, module_dir, prefix, by_package, deps, behavioural, targets)
        survivors = targets - killed
        if survivors:
            _, reached, _ = judge(tmp, module_dir, prefix, by_package, deps, panics, survivors)
            for test in survivors:
                if test not in ran:
                    what = "no variant of the change built and ran it"
                elif test in reached:
                    what = ("it calls the changed functions, but passes under every variant "
                            "that built: each function returning zero values, each block reverted")
                else:
                    what = "it passes under every variant that built and never calls a changed function"
                reasons[test] = what

    print("revert-test: base %s, %d new test(s), %d variant(s) built, %.0fs"
          % (base[:8], len(selected), built, time.time() - started))
    if reasons:
        print("revert-test: these new tests do not show that they test the change:",
              file=sys.stderr)
        for (package, test), what in sorted(reasons.items()):
            print("  %s: %s: %s" % (package, test, what), file=sys.stderr)
        print("Make each test fail when the changed code is wrong. If a test deliberately "
              "pins behaviour that already existed, put `%s` in its doc comment; the commit "
              "reviewer sees that claim." % MARKER, file=sys.stderr)
        return 1
    with open(stamp, "w", encoding="utf-8") as handle:
        handle.write(key)
    return 0


if __name__ == "__main__":
    sys.exit(main())
