#!/usr/bin/env python3
"""Revert-test check: each Go test added by the unpushed change must fail under a variant of it.

The change is the working tree against REVERT_TEST_BASE when it is set, which
must name a commit. Otherwise it is against the merge base with the upstream
branch (HEAD when there is none), except under GitHub Actions, where an unset
REVERT_TEST_BASE fails the run: a CI checkout has no upstream, and HEAD as the
base would check nothing. The script builds variants of the change in a copy of
the repository and runs the new top-level test functions under each one:

  revert    one changed block of non-test code put back to its base version
  copy      an added statement that copies a slice wrapped in `if false { }`
  alias     an added `x := <copy of src>` bound to src, the copy call kept
            in an unreachable block
  zero      a changed function returns the zero values of its results at once
  file      one changed text file outside the Go sources put back whole, or
            removed when it is new; every new test runs under it
  guard     an added if without else that ends in return, break, continue or
            goto wrapped in `if false { }`
  suppress  an added expression, `=` assignment, ++/--, go or defer statement
            wrapped in `if false { }`

Variants run in that order. copy, alias, guard and suppress come only from
statements that intersect added lines; FUNCTION_CAP and RUN_CAP limit them,
and the evidence lists each candidate a cap or a duplicate dropped. RUN_CAP
spends its budget one site per function at a time, so a change in one file
cannot starve the sites of another: without that, the order is alphabetical by
path and a late path loses every site it had.

A new test that fails under at least one variant passes the check. Every other
new test is reported and the script exits 1: a test that fails before any
variant, one that no variant ran, and one that passes under every variant that
ran. A test that times out under a variant is not failed by it. Panic probes run
only for the reported tests, to show whether a probe in a changed function makes
each one fail. A test whose doc comment carries MARKER is exempt.

Variants run on WORKERS copies of the repository at once (REVERT_TEST_WORKERS
overrides the default). Variant i always runs in copy i % WORKERS, so each copy
compiles the same mutations on every run. Results are taken in variant order, so
the lowest-index decisive failure decides each test; a test that a later variant
ran before an earlier one decided it records that attempt as ignored. The
failure that would accept each test then runs again alone, because two processes
that run one test at once can collide on a path they share. A failure that does
not happen again is unconfirmed; when it was decisive, its tests are judged again
on one copy from the next variant on.

Each go command runs in its own process group, and go test with -timeout
TEST_TIMEOUT. BUDGET starts before the lock wait; when it runs out, the running
commands' groups are killed, and the run writes incomplete evidence, records no
pass and exits 1. A run that finishes every variant keeps its result however
long it took.

Each run writes one evidence record per new test to EVIDENCE/<key>.json in the
git directory: the variant that decided it, the failure excerpt and its kind
(assertion, crash, or non-panic failure), and the outcome of every other
variant, with each variant's diff and the full test output.

A test that fails under a variant is recorded in EVIDENCE/ACCEPTED with the
digest of its package's test-side files: the *_test.go files in its directory
and every file under its testdata/. A later run of the same checker does not
judge that test again while that digest is unchanged, so a test is judged again
when its test side changes, not when only the code under test changes. Files
outside the package that a test reads are not in the digest.
"""

import collections
import concurrent.futures
import difflib
import fcntl
import hashlib
import json
import os
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import threading
import time

MODULE = "cli"
BASE_ENV = "REVERT_TEST_BASE"
MARKER = "// pins existing behaviour"
TEST_FUNC = re.compile(r"^func (Test\w+)\(\w+ \*testing\.T\)", re.MULTILINE)
HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")
PANIC = 'panic("revert-test")'
FUNCS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "revert-test-funcs.go")
EVIDENCE = "revert-test"
LOCK = "lock"
ACCEPTED = "accepted"
WAIT = 120
KEEP = 20
NOISE = re.compile(r"^(=== (RUN|PAUSE|CONT|NAME)|--- (FAIL|PASS|SKIP)|\s*$)")
FRAME = re.compile(r"^\s+(\S+\.go:\d+)")
REPORTED = re.compile(r"^\s+\S+\.go:\d+: ")
TIMED_OUT = re.compile(r"^panic: test timed out after ")
RUNNING = re.compile(r"^\t\t(Test\w+)")
FUNCTION_CAP = 4
RUN_CAP = 48
SITE_ORDER = ["copy", "alias", "guard", "suppress"]
SITE_LABELS = {"copy": "copy statement suppressed", "alias": "copy replaced by its source",
               "guard": "guard clause suppressed", "suppress": "statement suppressed"}
TEST_TIMEOUT = 60
BUDGET = 240
WORKERS = max(1, int(os.environ.get("REVERT_TEST_WORKERS") or min(4, os.cpu_count() or 1)))
POLL = 0.2
SCRUB = []
HELD = []
EXPIRED = []
BUDGET_REASON = "the time budget ran out before a variant decided it"


class BudgetExpired(Exception):
    pass


class Cancelled(Exception):
    pass


def bounded(args, cwd, deadline, stdin=None, fds=(), cancel=None):
    """Run args in its own process group and kill the group at deadline, a
    time.monotonic() value, or once cancel, a threading.Event, is set; fds are
    inherited. Return (returncode, stdout, stderr); raise BudgetExpired when the
    deadline passes first and Cancelled when cancel is set first."""
    if deadline - time.monotonic() <= 0:
        raise BudgetExpired("before %s" % " ".join(args[:2]))
    process = subprocess.Popen(args, cwd=cwd, stdin=subprocess.PIPE if stdin is not None else None,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                               pass_fds=fds, start_new_session=True)
    try:
        while True:
            remaining = max(0, deadline - time.monotonic())
            try:
                stdout, stderr = process.communicate(
                    stdin, timeout=remaining if cancel is None else min(remaining, POLL))
                return process.returncode, stdout, stderr
            except subprocess.TimeoutExpired:
                # communicate() refuses input once it has started.
                stdin = None
                if time.monotonic() >= deadline:
                    raise BudgetExpired("%s killed at the time budget" % " ".join(args[:2]))
                if cancel is not None and cancel.is_set():
                    raise Cancelled()
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate()
        raise


def git(*args, check=True):
    result = subprocess.run(["git", *args], capture_output=True, text=True)
    if check and result.returncode != 0:
        raise RuntimeError("git %s: %s" % (" ".join(args), result.stderr.strip()))
    return result.stdout


def base_revision():
    """Return the base commit of the change, or None after printing why there is none."""
    explicit = os.environ.get(BASE_ENV)
    if explicit is not None:
        resolved = subprocess.run(["git", "rev-parse", "--verify", "--quiet", explicit + "^{commit}"],
                                  capture_output=True, text=True)
        if not explicit.strip() or resolved.returncode != 0:
            print("revert-test: %s=%r names no commit" % (BASE_ENV, explicit), file=sys.stderr)
            return None
        return resolved.stdout.strip()
    if os.environ.get("GITHUB_ACTIONS") == "true":
        print("revert-test: %s is unset under GitHub Actions; the workflow must resolve the base"
              % BASE_ENV, file=sys.stderr)
        return None
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


def data_paths(base):
    """Return changed text files that are not Go sources or test-side files,
    anywhere in the repository: files a test can read."""
    tracked = git("diff", "--name-only", "--no-ext-diff", base).split()
    untracked = git("ls-files", "--others", "--exclude-standard").split()
    paths = []
    for path in sorted(set(tracked) | set(untracked)):
        if path.endswith(".go") or is_test_side(path) or not os.path.isfile(path):
            continue
        try:
            with open(path, encoding="utf-8") as handle:
                handle.read()
        except (OSError, UnicodeDecodeError):
            continue
        paths.append(path)
    return paths


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


def functions(paths, deadline):
    """Map each Go file to its top-level functions, as revert-test-funcs.go prints them."""
    if not paths:
        return {}
    code, stdout, stderr = bounded(["go", "run", FUNCS], None, deadline, "\n".join(paths))
    if code != 0:
        raise RuntimeError("revert-test-funcs: %s" % stderr.strip())
    return json.loads(stdout)


def insert(data, offset, statement):
    return data[:offset] + ("\n\t%s\n" % statement).encode() + data[offset:]


def variant(kind, path, label, original, mutated):
    """mutated None removes the file."""
    diff = difflib.unified_diff(original.decode("utf-8").splitlines(keepends=True),
                                (mutated or b"").decode("utf-8").splitlines(keepends=True),
                                path, path + " (variant)", n=1)
    return {"kind": kind, "path": path, "label": label, "data": mutated, "diff": "".join(diff)}


def in_base(base, path):
    return subprocess.run(["git", "cat-file", "-e", "%s:%s" % (base, path)],
                          capture_output=True).returncode == 0


def data_variants(base, paths):
    """One variant per data file: a new file removed, a changed one put back whole."""
    out = []
    for path in paths:
        with open(path, "rb") as handle:
            data = handle.read()
        if not in_base(base, path):
            out.append(variant("file", path, "file removed (%s)" % path, data, None))
        else:
            old = git("show", "%s:%s" % (base, path)).encode()
            out.append(variant("file", path, "file reverted to base (%s)" % path, data, old))
    return out


def apply_edits(data, edits):
    for edit in sorted(edits, key=lambda e: (e["start"], e["end"]), reverse=True):
        data = data[:edit["start"]] + edit["text"].encode() + data[edit["end"]:]
    return data


def added_ranges(lines, spans):
    """Return the byte ranges of the added lines, given 1-based (start, count) spans."""
    offsets = [0]
    for line in lines:
        offsets.append(offsets[-1] + len(line.encode()))
    return [(offsets[start - 1], offsets[start - 1 + count]) for start, count in spans]


def rank(candidates):
    """Number each candidate within its own kind and function, in the order given.
    Sorting on that number next spreads RUN_CAP over the functions: every function
    spends its first site before any function spends its second."""
    counts = {}
    for candidate in candidates:
        key = (candidate["kind"], candidate["path"], candidate["function"])
        candidate["rank"] = counts.get(key, 0)
        counts[key] = candidate["rank"] + 1


def select_sites(candidates, seen):
    """Apply dedup and the caps to candidates in their priority order.
    Return (kept, omitted); seen holds (path, bytes) of earlier variants."""
    kept, omitted, per_function = [], [], {}
    for candidate in candidates:
        key = (candidate["path"], candidate["data"])
        function = (candidate["path"], candidate["function"])
        if key in seen:
            reason = "same bytes as an earlier variant"
        elif per_function.get(function, 0) >= FUNCTION_CAP:
            reason = "cap of %d per function" % FUNCTION_CAP
        elif len(kept) >= RUN_CAP:
            reason = "cap of %d per run" % RUN_CAP
        else:
            seen.add(key)
            per_function[function] = per_function.get(function, 0) + 1
            kept.append(candidate)
            continue
        omitted.append({"label": candidate["label"], "reason": reason})
    return kept, omitted


def variants(base, paths, untracked, deadline):
    """Return (early, late, panics, omitted). early holds block reverts, copy
    suppressions and aliases, then zero values; late holds guard and other statement
    suppressions. omitted lists the site candidates that dedup or a cap dropped."""
    sources = [p for p in paths if p.endswith(".go") and not is_test_side(p)]
    funcs = functions(sources, deadline)
    zeros, reverts, panics, candidates = [], [], [], []
    for path in sources:
        with open(path, "rb") as handle:
            data = handle.read()
        lines = data.decode("utf-8").splitlines(keepends=True)
        touched = set(range(1, len(lines) + 1)) if path in untracked else set()
        spans = [(1, len(lines))] if path in untracked else []
        if path not in untracked:
            for start, count, old, new in hunks(base, path):
                if is_trivia(old) and is_trivia(new):
                    continue
                touched.update(range(start, start + count) if count else (start, start + 1))
                if count:
                    spans.append((start, count))
                where = ("%s:%d-%d" % (path, start, start + count - 1) if count
                         else "%s: after line %d" % (path, start))
                mutated = "".join(revert(lines, start, count, old)).encode()
                reverts.append(variant("revert", path, "block reverted to base (%s)" % where,
                                       data, mutated))
        added = added_ranges(lines, spans)
        for fn in funcs[path]:
            if any(fn["first"] <= t <= fn["last"] for t in touched):
                where = "%s:%d" % (path, fn["first"])
                zeros.append(variant("zero", path, "func %s returns zero values (%s)"
                                     % (fn["name"], where), data,
                                     insert(data, fn["body"], fn["zero"])))
                panics.append(variant("panic", path, "panic probe in func %s (%s)"
                                      % (fn["name"], where), data,
                                      insert(data, fn["body"], PANIC)))
            for site in fn["sites"]:
                if not any(site["start"] < end and start < site["end"] for start, end in added):
                    continue
                label = "%s in func %s (%s:%d: %s)" % (SITE_LABELS[site["kind"]], fn["name"],
                                                      path, site["line"], site["text"][:60])
                candidate = variant(site["kind"], path, label, data,
                                    apply_edits(data, site["edits"]))
                candidate["function"] = "%s:%d" % (fn["name"], fn["first"])
                candidate["offset"] = site["start"]
                candidates.append(candidate)
    candidates.sort(key=lambda c: (SITE_ORDER.index(c["kind"]), c["path"], c["offset"]))
    rank(candidates)
    candidates.sort(key=lambda c: (SITE_ORDER.index(c["kind"]), c["rank"], c["path"],
                                   c["offset"]))
    kept, omitted = select_sites(candidates, {(r["path"], r["data"]) for r in reverts})
    for candidate in kept:
        del candidate["function"], candidate["offset"], candidate["rank"]
    early = [c for c in kept if c["kind"] in ("copy", "alias")]
    late = [c for c in kept if c["kind"] not in ("copy", "alias")]
    return reverts + early + zeros, late, panics, omitted


def import_path(prefix, path):
    package = os.path.relpath(os.path.dirname(path), MODULE)
    return prefix if package == "." else "%s/%s" % (prefix, package)


def test_deps(prefix, packages, deadline):
    """Map each package to the set of import paths linked into its test binary."""
    deps = {}
    for package in packages:
        args = ["go", "list", "-test", "-deps", "./" + package]
        code, stdout, stderr = bounded(args, MODULE, deadline)
        if code != 0:
            raise RuntimeError("%s: %s" % (" ".join(args), stderr.strip()))
        deps[package] = {line.split(" ")[0] for line in stdout.splitlines()}
    return deps


def checker_digest():
    digest = hashlib.sha256()
    for script in (__file__, FUNCS):
        with open(script, "rb") as handle:
            digest.update(handle.read())
    return digest.hexdigest()


def test_inputs(package):
    """Return the digest of package's test-side files: the *_test.go files in its
    directory and every file under its testdata/, tracked or not. Return None when
    one of them is a symlink, so no acceptance is reused or recorded for it."""
    directory = os.path.join(MODULE, package)
    paths = [os.path.join(directory, f) for f in os.listdir(directory) if f.endswith("_test.go")]
    testdata = os.path.join(directory, "testdata")
    if os.path.islink(testdata):
        return None
    for top, dirs, names in os.walk(testdata):
        if any(os.path.islink(os.path.join(top, d)) for d in dirs):
            return None
        paths.extend(os.path.join(top, n) for n in names)
    digest = hashlib.sha256()
    for path in sorted(paths):
        if os.path.islink(path):
            return None
        with open(path, "rb") as handle:
            data = handle.read()
        digest.update(b"%s\0%d\0" % (path.encode(), len(data)) + data)
    return digest.hexdigest()


def read_accepted(directory, checker):
    """Return the acceptance records by (package, test), or {} when the store is
    missing, unreadable, or written by a different checker."""
    try:
        with open(os.path.join(directory, ACCEPTED), encoding="utf-8") as handle:
            store = json.load(handle)
        if store["checker"] != checker:
            return {}
        return {(r["package"], r["test"]): r for r in store["tests"]}
    except (OSError, ValueError, KeyError, TypeError):
        return {}


def write_accepted(directory, checker, records):
    path = os.path.join(directory, ACCEPTED)
    with open(path + ".tmp", "w", encoding="utf-8") as handle:
        json.dump({"checker": checker, "tests": [records[t] for t in sorted(records)]},
                  handle, indent=1)
    os.replace(path + ".tmp", path)


def change_key(base, name_status, paths):
    digest = hashlib.sha256()
    digest.update(checker_digest().encode())
    digest.update(base.encode() + name_status.encode())
    for path in paths:
        digest.update(path.encode() + b"\0")
        with open(path, "rb") as handle:
            digest.update(handle.read())
    return digest.hexdigest()


def copy_repository(root, tmp, copied):
    """Copy the module and each top-level entry in copied into tmp, and link every
    other top-level entry, so tests that read files beside the module find them.
    A variant writes only into copied entries, never through a link."""
    for entry in os.listdir(root):
        if entry == ".git":
            continue
        source, target = os.path.join(root, entry), os.path.join(tmp, entry)
        if entry != MODULE and entry not in copied:
            os.symlink(source, target)
        elif os.path.isdir(source) and not os.path.islink(source):
            shutil.copytree(source, target, symlinks=True)
        else:
            shutil.copy2(source, target, follow_symlinks=False)


def hold_lock(directory, deadline=None):
    """Take the exclusive per-worktree lock in directory, waiting up to WAIT seconds
    and never past deadline, a time.monotonic() value. Return its descriptor, or
    None when another holder keeps it. The lock file is never removed."""
    os.makedirs(directory, exist_ok=True)
    fd = os.open(os.path.join(directory, LOCK), os.O_RDWR | os.O_CREAT, 0o600)
    limit = time.monotonic() + WAIT
    if deadline is not None:
        limit = min(limit, deadline)
    while True:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return fd
        except BlockingIOError:
            if time.monotonic() >= limit:
                os.close(fd)
                return None
            time.sleep(1)


def scratch_paths(root):
    """Return this worktree's copy locations, one per worker: outside the repository,
    private to this user, and the same on every run, so Go reuses compiled packages
    across runs."""
    digest = hashlib.sha256(os.path.realpath(root).encode()).hexdigest()[:12]
    base = os.path.join(tempfile.gettempdir(), "revert-test-%d-%s" % (os.getuid(), digest))
    return ["%s-w%d" % (base, n) for n in range(WORKERS)]


def clear_scratch(path):
    """Remove path when it is a directory this user owns; refuse any other entry."""
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        return
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid():
        raise RuntimeError("%s is not a directory owned by this user" % path)
    shutil.rmtree(path)


def scrub(line):
    """Rewrite a go test output line so its paths are relative to the repository."""
    line = line.rstrip("\n")
    for root in SCRUB:
        line = line.replace(root, "")
    if line.startswith("./"):
        return MODULE + "/" + line[2:]
    if re.match(r"^[\w.-]+/[\w./-]+\.go:\d+", line) and not line.startswith(MODULE + "/"):
        return MODULE + "/" + line
    return line


def classify(lines):
    """Return (kind, excerpt) for a failing test's output: crash when it panicked,
    assertion when the test reported a message at a file:line, otherwise
    non-panic failure."""
    body = [l for l in lines if not NOISE.match(l)]
    panic = next((l for l in body if l.startswith("panic: ")), None)
    if panic:
        frames = (FRAME.match(l) for l in body)
        frame = next((m.group(1) for m in frames if m and not m.group(1).startswith("/")), "")
        return "crash", panic.strip() + (" at " + frame if frame else "")
    reported = [l.strip() for l in body if REPORTED.match(l)]
    if reported:
        return "assertion", " | ".join(reported[:3])
    return "non-panic failure", " | ".join(l.strip() for l in body[:3]) or "no output"


def excerpt(lines, limit=3):
    """Summarise build or package output: a panic with its first repository frame,
    otherwise the first lines that carry text."""
    if any(l.startswith("panic: ") for l in lines):
        return classify(lines)[1]
    kept = [l.strip() for l in lines if l.strip() and not l.startswith("#")
            and not NOISE.match(l) and not l.startswith("FAIL")]
    return " | ".join(kept[:limit]) or "no output"


def diagnosis(package, notes):
    """Return (result, lines) for a test that produced no outcome: the build errors
    of its package when its build failed, otherwise its package output and the
    stderr of go test."""
    if notes["build"].get(package):
        return "build failed", notes["build"][package]
    return "no result", notes["output"].get(package, []) + notes["stderr"]


def run_tests(module_dir, prefix, by_package, deadline, cancel=None):
    """Return (outcome, notes, expired). outcome maps (package, test) to {result,
    output}. notes holds "build" and "output" (lines by package) and "stderr" (lines).
    expired names the command the deadline stopped, or is None. A panic ends its
    package's binary, so tests left without an outcome are run again until none
    makes progress or the deadline passes. Cancelled propagates from cancel."""
    outcome, notes = {}, {"build": {}, "output": {}, "stderr": []}
    pending = {p: list(ns) for p, ns in by_package.items()}
    while pending:
        try:
            found, found_notes = run_once(module_dir, prefix, pending, deadline, cancel)
        except BudgetExpired as expired:
            return outcome, notes, str(expired)
        for key in ("build", "output"):
            for package, lines in found_notes[key].items():
                notes[key].setdefault(package, []).extend(lines)
        notes["stderr"].extend(found_notes["stderr"])
        if not found:
            break
        outcome.update(found)
        pending = {p: [n for n in ns if (p, n) not in outcome] for p, ns in pending.items()}
        pending = {p: ns for p, ns in pending.items() if ns}
    return outcome, notes, None


def go_test(args, module_dir, deadline, cancel=None):
    """Run go test under the deadline and cancel. Return (stdout, stderr)."""
    return bounded(args, module_dir, deadline, fds=tuple(HELD), cancel=cancel)[1:]


def run_once(module_dir, prefix, by_package, deadline, cancel=None):
    names = sorted({n for ns in by_package.values() for n in ns})
    stdout, stderr = go_test(
        ["go", "test", "-count=1", "-vet=off", "-json", "-timeout", "%ds" % TEST_TIMEOUT,
         "-run", "^(%s)$" % "|".join(names)] + ["./" + p for p in sorted(by_package)],
        module_dir, deadline, cancel)
    outcome, output, built, stream, started = {}, {}, {}, {}, {}
    notes = {"build": {}, "output": {}, "stderr": [scrub(l) for l in stderr.splitlines()]}
    for line in stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        action = event.get("Action")
        if action == "build-output":
            built.setdefault(event.get("ImportPath", ""), []).append(scrub(event.get("Output", "")))
            continue
        if not event.get("Package"):
            continue
        package = os.path.relpath(event["Package"], prefix)
        test = event.get("Test")
        if action == "run" and test and "/" not in test:
            started.setdefault(package, []).append(test)
        if action == "output":
            text = scrub(event.get("Output", ""))
            stream.setdefault(package, []).append(text)
            if test:
                output.setdefault((package, test.split("/")[0]), []).append(text)
            else:
                notes["output"].setdefault(package, []).append(text)
        elif action == "fail" and not test and event.get("FailedBuild"):
            notes["build"][package] = built.get(event["FailedBuild"], [])
        elif action in ("pass", "fail") and test and "/" not in test:
            if test in by_package.get(package, ()):
                outcome[(package, test)] = {"result": action,
                                            "output": output.get((package, test), [])}
    for package, lines in stream.items():
        at = next((i for i, l in enumerate(lines) if TIMED_OUT.match(l)), None)
        if at is None:
            continue
        alarm = [lines[at]]
        for line in lines[at + 1:]:
            if line.strip() and not RUNNING.match(line) and line.strip() != "running tests:":
                break
            alarm.append(line)
        # The alarm lists the tests still running, under whichever test its output is
        # attributed to; without a list, every started test with no outcome was running.
        running = {m.group(1) for m in map(RUNNING.match, alarm) if m}
        running = running or set(started.get(package, []))
        for name in sorted(running & set(by_package.get(package, ()))):
            if (package, name) not in outcome:
                outcome[(package, name)] = {"result": "timed out",
                                            "output": output.get((package, name), []) + alarm}
    return outcome, notes


def run_variant(root, change, by_package, prefix, deadline, cancel):
    """Apply change in the copy at root, run by_package's tests there and restore the
    file. Return run_tests's result, or None when cancel stopped the run."""
    target = os.path.join(root, change["path"])
    with open(target, "rb") as handle:
        original = handle.read()
    try:
        if change["data"] is None:
            os.remove(target)
        else:
            with open(target, "wb") as handle:
                handle.write(change["data"])
        return run_tests(os.path.join(root, MODULE), prefix, by_package, deadline, cancel)
    except Cancelled:
        return None
    finally:
        with open(target, "wb") as handle:
            handle.write(original)


def judge(roots, prefix, deps, changes, targets, decisive, deadline, starts=None):
    """Run targets under each variant and return {test: [attempt]} with one attempt
    per variant. An attempt's result is pass, fail, timed out, build failed, no
    result, not linked, not run (the test already had a decisive failure), ignored
    (it ran, but an earlier variant decided it first; outcome holds its result),
    cancelled (an earlier variant decided it while it ran, and the run was killed),
    or not run: time budget (the run's deadline passed first).

    Variant i runs in roots[i % len(roots)], at most one variant per root at a time.
    Results are taken in variant order; a running variant whose tests an earlier
    one decided is cancelled. starts maps a test to the first variant that runs it;
    the attempts before that index are not meaningful."""
    starts = starts or {}
    recorded = {t: {} for t in targets}
    done, busy, flight, upcoming = set(), set(), collections.deque(), 0

    def take(entry, result):
        """Record entry's attempts from result, run_variant's return or None, add the
        tests they decide to done and return the expiry result names. A test still
        open in a run that never returned is left unrecorded."""
        index = entry["index"]
        outcome, notes, expired = result or ({}, None, None)
        decided = set()
        for test in targets:
            attempt = {"variant": index}
            if test in done and result and test in entry["linked"]:
                if test in outcome:
                    ran = outcome[test]["result"]
                else:
                    ran = "not run: time budget" if expired else diagnosis(test[0], notes)[0]
                attempt.update(result="ignored", outcome=ran)
            elif test in done:
                killed = "future" in entry and test in entry["linked"]
                attempt["result"] = "cancelled" if killed else "not run"
            elif test not in entry["linked"]:
                attempt["result"] = "not linked"
            elif not result:
                continue
            elif test in outcome:
                attempt["result"] = outcome[test]["result"]
                attempt["output"] = outcome[test]["output"]
                if attempt["result"] == "fail":
                    attempt["kind"], attempt["excerpt"] = classify(attempt["output"])
                elif attempt["result"] == "timed out":
                    attempt["excerpt"] = timeout_excerpt(attempt["output"])
            elif expired:
                attempt.update(result="not run: time budget", output=lines_of(notes, test))
            else:
                result_name, lines = diagnosis(test[0], notes)
                attempt.update(result=result_name, output=lines, excerpt=excerpt(lines))
            recorded[test][index] = attempt
            if decisive(attempt):
                decided.add(test)
        done.update(decided)
        return expired

    pool = concurrent.futures.ThreadPoolExecutor(len(roots))
    try:
        while True:
            while (upcoming < len(changes) and not EXPIRED and not done >= targets
                   and upcoming % len(roots) not in busy):
                change = changes[upcoming]
                imported = None if change["kind"] == "file" else import_path(prefix, change["path"])
                entry = {"index": upcoming, "imported": imported,
                         "linked": {t for t in targets if t not in done
                                    and upcoming >= starts.get(t, 0)
                                    and (not imported or imported in deps[t[0]])}}
                if entry["linked"]:
                    reachable = {}
                    for package, name in sorted(entry["linked"]):
                        reachable.setdefault(package, []).append(name)
                    slot = upcoming % len(roots)
                    busy.add(slot)
                    entry["cancel"] = threading.Event()
                    entry["future"] = pool.submit(run_variant, roots[slot], change, reachable,
                                                  prefix, deadline, entry["cancel"])
                flight.append(entry)
                upcoming += 1
            if not flight:
                break
            # The awaited entry stays in flight, so an interrupt here still cancels it.
            entry = flight[0]
            result = entry["future"].result() if "future" in entry else None
            flight.popleft()
            if "future" in entry:
                busy.discard(entry["index"] % len(roots))
            expired = take(entry, result)
            if expired:
                EXPIRED.append(expired)
                break
            if done >= targets:
                break
            for later in flight:
                if "cancel" in later and later["linked"] <= done:
                    later["cancel"].set()
    finally:
        for entry in flight:
            if "cancel" in entry:
                entry["cancel"].set()
        pool.shutdown(wait=True)
    for entry in flight:
        take(entry, entry["future"].result() if "future" in entry else None)
    attempts = {}
    for test in targets:
        rest = "not run" if test in done else "not run: time budget"
        attempts[test] = [recorded[test].get(i, {"variant": i, "result": rest})
                          for i in range(len(changes))]
    return attempts


def confirm(roots, prefix, deps, changes, targets, decisive, deadline):
    """Judge targets under changes, then run again in roots[0], with no other variant
    running, the failure assess would accept for each test: its first decisive
    failure, else its first failure. A decisive failure must be decisive again, any
    other failure must fail again; the attempt then carries the second run's output,
    kind and excerpt, and confirmation.pooled keeps the first run's. One that does
    not becomes unconfirmed, with the second run in its confirmation, and the next
    candidate is checked. A test whose decisive failure was unconfirmed is judged
    again on roots[0] alone from the next variant on; those runs need no second
    run. Return {test: [attempt]} as judge does."""
    attempts = judge(roots, prefix, deps, changes, targets, decisive, deadline)
    if len(roots) == 1:
        return attempts
    alone = {}
    while True:
        picks = {}
        for test in targets:
            fails = [a for a in attempts[test] if a["result"] == "fail"]
            hit = next((a for a in fails if decisive(a)), None) or (fails[0] if fails else None)
            if hit and "confirmation" not in hit and hit["variant"] < alone.get(test, len(changes)):
                picks.setdefault(hit["variant"], []).append(test)
        if not picks:
            return attempts
        starts = {}
        for index in sorted(picks):
            outcome, expired = {}, "not run: time budget"
            if not EXPIRED:
                reachable = {}
                for package, name in sorted(picks[index]):
                    reachable.setdefault(package, []).append(name)
                outcome, _, expired = run_variant(roots[0], changes[index], reachable, prefix,
                                                  deadline, None)
                if expired:
                    EXPIRED.append(expired)
            for test in picks[index]:
                hit = attempts[test][index]
                again = outcome.get(test) or {
                    "result": "not run: time budget" if expired else "no result", "output": []}
                check = {"result": again["result"]}
                if check["result"] == "fail":
                    check["kind"] = classify(again["output"])[0]
                hit["confirmation"] = {"result": again["result"]}
                if decisive(check) if decisive(hit) else check["result"] == "fail":
                    hit["confirmation"]["pooled"] = {"kind": hit["kind"], "excerpt": hit["excerpt"],
                                                     "output": hit["output"]}
                    hit["kind"], hit["excerpt"] = classify(again["output"])
                    hit["output"] = again["output"]
                    continue
                if decisive(hit) and not expired:
                    starts[test] = index + 1
                hit["confirmation"]["output"] = again["output"]
                hit["result"] = "unconfirmed"
        if starts:
            serial = judge(roots[:1], prefix, deps, changes, set(starts), decisive, deadline,
                           starts)
            for test, begin in starts.items():
                attempts[test] = attempts[test][:begin] + serial[test][begin:]
            alone.update(starts)


def timeout_excerpt(lines):
    running = [m.group(1) for m in map(RUNNING.match, lines) if m]
    alarm = next((l.strip() for l in lines if TIMED_OUT.match(l)), "test timed out")
    return alarm + ("; running: " + ", ".join(running) if running else "")


def lines_of(notes, test):
    return notes["output"].get(test[0], []) + notes["stderr"]


def counts(attempts):
    """Summarise attempts, e.g. "1 failed, 3 passed, 2 did not build"."""
    names = [("fail", "failed"), ("pass", "passed"), ("build failed", "did not build"),
             ("timed out", "timed out"), ("no result", "gave no result"),
             ("not linked", "not linked into the test binary"),
             ("not run", "not run after the decisive failure"),
             ("ignored", "run but ignored after an earlier decisive failure"),
             ("cancelled", "cancelled after an earlier decisive failure"),
             ("unconfirmed", "failed, but not again when run alone"),
             ("not run: time budget", "not run before the time budget ran out")]
    tally = {}
    for attempt in attempts:
        tally[attempt["result"]] = tally.get(attempt["result"], 0) + 1
    return ", ".join("%d %s" % (tally[k], label) for k, label in names if tally.get(k)) or "none"


VERDICTS = {"assertion": "an assertion fails under a variant",
            "crash": "crashes under a variant; no assertion fails",
            "non-panic failure": "fails under a variant with no panic and no assertion message"}


def assess(test, baseline, notes, linked, behavioural, attempts, panics, probes, incomplete):
    """Build the evidence record for one new test."""
    record = {"package": test[0], "test": test[1]}
    base = baseline.get(test)
    if not base and incomplete:
        record.update(verdict="rejected", reason=BUDGET_REASON)
        return record
    if base:
        record["baseline"] = {"result": base["result"], "output": base["output"]}
    else:
        result, lines = diagnosis(test[0], notes)
        record["baseline"] = {"result": result, "output": lines}
    if not base or base["result"] != "pass":
        record["verdict"] = "rejected"
        record["reason"] = "fails in the copy of the repository before any variant"
        record["excerpt"] = (classify(base["output"])[1] if base
                             else excerpt(record["baseline"]["output"]))
        return record
    if not linked:
        record["verdict"] = "rejected"
        record["reason"] = "its test binary links none of the changed packages"
        return record
    record["attempts"] = attempts
    record["variants"] = counts(attempts)
    failures = [a for a in attempts if a["result"] == "fail"]
    decided = next((a for a in failures if a["kind"] == "assertion"), None) or \
        (failures[0] if failures else None)
    if decided:
        record["verdict"] = VERDICTS[decided["kind"]]
        record["decisive"] = {"variant": behavioural[decided["variant"]]["label"],
                              "kind": decided["kind"], "excerpt": decided["excerpt"]}
        return record
    record["verdict"] = "rejected"
    ran = [a for a in attempts if a["result"] == "pass"]
    if incomplete and any(a["result"] == "not run: time budget" or a.get("confirmation", {})
                          .get("result") == "not run: time budget" for a in attempts):
        record["reason"] = BUDGET_REASON
    else:
        record["reason"] = ("passes under every variant that ran" if ran
                            else "no variant of the change ran it")
    for result, field in (("build failed", "first_build_error"), ("no result", "first_no_result"),
                          ("timed out", "first_timeout")):
        broken = next((a for a in attempts if a["result"] == result), None)
        if broken:
            record[field] = "%s: %s" % (behavioural[broken["variant"]]["label"], broken["excerpt"])
    record["probe_attempts"] = probes
    hit = next((a for a in probes if a["result"] == "fail"), None)
    record["probe"] = ("%s made it fail, so it executes that function" % panics[hit["variant"]]["label"]
                       if hit else "no panic probe made it fail (%s)" % counts(probes))
    return record


def render(record):
    """Return the console lines for a rejected test."""
    lines = ["  %s: %s: %s" % (record["package"], record["test"], record["verdict"]),
             "    reason: %s" % record["reason"]]
    if "excerpt" in record:
        lines.append("    baseline: %s — %s" % (record["baseline"]["result"].upper(),
                                                record["excerpt"]))
    if "variants" in record:
        lines.append("    variants: %s" % record["variants"])
    if "first_build_error" in record:
        lines.append("    first build error: %s" % record["first_build_error"])
    if "first_no_result" in record:
        lines.append("    first no-result output: %s" % record["first_no_result"])
    if "first_timeout" in record:
        lines.append("    first timeout: %s" % record["first_timeout"])
    if "probe" in record:
        lines.append("    panic probes: %s" % record["probe"])
    return lines


def write_evidence(directory, key, base, behavioural, panics, omitted, incomplete, records,
                   reused):
    os.makedirs(directory, exist_ok=True)
    path = os.path.join(directory, key[:16] + ".json")
    listed = [{k: v for k, v in change.items() if k != "data"} for change in behavioural + panics]
    limits = {"function_cap": FUNCTION_CAP, "run_cap": RUN_CAP, "test_timeout": TEST_TIMEOUT,
              "budget": BUDGET, "workers": WORKERS}
    with open(path, "w", encoding="utf-8") as handle:
        json.dump({"base": base, "key": key, "incomplete": incomplete, "limits": limits,
                   "behavioural_variants": listed[:len(behavioural)],
                   "panic_probes": listed[len(behavioural):], "omitted_candidates": omitted,
                   "tests": records, "previously_accepted": [reused[t] for t in sorted(reused)]},
                  handle, indent=1)
    old = sorted((os.path.join(directory, f) for f in os.listdir(directory) if f.endswith(".json")),
                 key=os.path.getmtime)
    for stale in old[:-KEEP]:
        os.remove(stale)
    return path


def main():
    started = time.monotonic()
    deadline = started + BUDGET
    root = git("rev-parse", "--show-toplevel").strip()
    os.chdir(root)
    directory = os.path.join(git("rev-parse", "--absolute-git-dir").strip(), EVIDENCE)
    lock = hold_lock(directory, deadline)
    if lock is None:
        print("revert-test: another run held %s for %ds; `lsof` on it names the holder"
              % (os.path.join(directory, LOCK), time.monotonic() - started), file=sys.stderr)
        return 1
    # Each go test inherits the lock, so a successor cannot replace the copy under it.
    HELD.append(lock)
    base = base_revision()
    if base is None:
        return 1
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

    data = data_paths(base)
    stamp = os.path.join(directory, "pass")
    key = change_key(base, git("diff", "--name-status", "--no-ext-diff", base),
                     sorted(set(paths) | set(data)))
    evidence = os.path.join(directory, key[:16] + ".json")
    try:
        with open(stamp, encoding="utf-8") as handle:
            if handle.read().strip() == key:
                print("revert-test: unchanged since the last pass; evidence: %s"
                      % os.path.relpath(evidence, root))
                return 0
    except OSError:
        pass

    checker = checker_digest()
    by_package_all = by_package
    inputs = {package: test_inputs(package) for package in by_package}
    reused = {t: r for t, r in read_accepted(directory, checker).items()
              if t[1] in by_package.get(t[0], []) and inputs[t[0]] is not None
              and r.get("inputs") == inputs[t[0]]}
    by_package = {p: [n for n in ns if (p, n) not in reused] for p, ns in by_package.items()}
    by_package = {p: ns for p, ns in by_package.items() if ns}
    if not by_package:
        print("revert-test: base %s, %d new test(s), all previously accepted; test files "
              "unchanged" % (base[:8], len(reused)))
        return 0

    with open(os.path.join(MODULE, "go.mod"), encoding="utf-8") as handle:
        prefix = re.search(r"^module (\S+)", handle.read(), re.MULTILINE).group(1)
    selected = {(p, n) for p, ns in by_package.items() for n in ns}
    baseline, baseline_notes, linked = {}, {"build": {}, "output": {}, "stderr": []}, set()
    behavioural, panics, omitted, attempts, probes = [], [], [], {}, {}
    roots = scratch_paths(root)
    for tmp in roots:
        clear_scratch(tmp)
    try:
        for tmp in roots:
            os.mkdir(tmp, 0o700)
        deps = test_deps(prefix, by_package, deadline)
        SCRUB.extend(os.path.realpath(tmp) + "/" for tmp in roots)
        SCRUB.extend(tmp + "/" for tmp in roots)
        for tmp in roots:
            copy_repository(root, tmp, {p.split("/")[0] for p in data})

        baseline, baseline_notes, expired = run_tests(os.path.join(roots[0], MODULE), prefix,
                                                      by_package, deadline)
        if expired:
            raise BudgetExpired(expired)
        live = {t for t in selected if baseline.get(t, {}).get("result") == "pass"}
        early, late, panics, omitted = variants(base, paths, untracked, deadline)
        mutated_packages = {import_path(prefix, c["path"]) for c in early + late + panics}
        behavioural = early + data_variants(base, data) + late
        linked = {t for t in live if data or mutated_packages & deps[t[0]]}
        asserts = lambda a: a["result"] == "fail" and a["kind"] == "assertion"
        attempts = confirm(roots, prefix, deps, behavioural, linked, asserts, deadline)
        survivors = {t for t in linked
                     if not any(a["result"] == "fail" for a in attempts[t])}
        probes = confirm(roots, prefix, deps, panics, survivors,
                         lambda a: a["result"] == "fail", deadline)
    except BudgetExpired as expired:
        EXPIRED.append(str(expired))
    finally:
        for tmp in roots:
            clear_scratch(tmp)
    incomplete = bool(EXPIRED)
    records = [assess(t, baseline, baseline_notes, t in linked, behavioural, attempts.get(t, []),
                      panics, probes.get(t, []), incomplete) for t in sorted(selected)]
    path = os.path.relpath(write_evidence(directory, key, base, behavioural, panics, omitted,
                                          incomplete, records, reused), root)

    undecided = [r for r in records if r.get("reason") == BUDGET_REASON]
    rejected = [r for r in records if r["verdict"] == "rejected" and r not in undecided]
    accepted = [r for r in records if r["verdict"] != "rejected"]
    # Records of tests outside this run stay, so a branch switch does not discard them.
    # A test edited during the run keeps no acceptance: its judged bytes are gone.
    store = {t: r for t, r in read_accepted(directory, checker).items()
             if t[1] not in by_package_all.get(t[0], [])}
    store.update(reused)
    for record in accepted:
        digest = test_inputs(record["package"])
        if digest is not None and digest == inputs[record["package"]]:
            store[(record["package"], record["test"])] = {
                "package": record["package"], "test": record["test"], "inputs": digest,
                "verdict": record["verdict"], "decisive": record["decisive"], "base": base,
                "evidence": os.path.basename(path)}
    write_accepted(directory, checker, store)
    print("revert-test: base %s, %d new test(s): %d judged, %d accepted, %d previously "
          "accepted; %d variant(s), %d panic probe(s), %d omitted candidate(s), %.0fs; "
          "evidence: %s"
          % (base[:8], len(selected) + len(reused), len(selected), len(accepted), len(reused),
             len(behavioural), len(panics), len(omitted), time.monotonic() - started, path),
          flush=True)
    if not rejected and not incomplete:
        with open(stamp, "w", encoding="utf-8") as handle:
            handle.write(key)
        return 0
    out = []
    if rejected:
        out.append("revert-test: these new tests do not show that they test the change:")
        for record in rejected:
            out.extend(render(record))
        out.append("Keep each rejected test; do not delete or weaken it to pass this check. "
                   "Read the evidence file: it holds each variant's diff and the full test "
                   "output. If a variant breaks the property the test claims, make the test "
                   "fail under it. If no variant can violate that property, extend the variant "
                   "generator in scripts/revert-test.py so that one can. If a test deliberately "
                   "pins behaviour that already existed, put `%s` in its doc comment; the "
                   "commit reviewer sees that claim." % MARKER)
    if incomplete:
        out.append("revert-test: incomplete — the time budget of %ds ran out (%s); no pass is "
                   "recorded. Undecided: %s" % (BUDGET, EXPIRED[0], ", ".join(
                       "%s: %s" % (r["package"], r["test"]) for r in undecided) or "none"))
    print("\n".join(out), file=sys.stderr)
    return 1

if __name__ == "__main__":
    sys.exit(main())
