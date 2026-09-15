#!/usr/bin/env python3
"""Revert-test check: each Go test added by the unpushed change must fail under a variant of it.

The change is the working tree against the merge base with the upstream branch
(HEAD when there is none). The script builds variants of the change in a copy of
the repository and runs the new top-level test functions under each one:

  zero    a changed function returns the zero values of its results at once
  revert  one changed block of non-test code put back to its base version
  file    one changed text file outside the Go sources put back whole, or
          removed when it is new; every new test runs under it

A new test that fails under at least one variant passes the check. Every other
new test is reported and the script exits 1: a test that fails before any
variant, one that no variant ran, and one that passes under every variant that
ran. Panic probes run only for the reported tests, to show whether a probe in a
changed function makes each one fail. A test whose doc comment carries MARKER is
exempt.

Each run writes one evidence record per new test to EVIDENCE/<key>.json in the
git directory: the variant that decided it, the failure excerpt and its kind
(assertion, crash, or non-panic failure), and the outcome of every other
variant, with each variant's diff and the full test output.
"""

import difflib
import fcntl
import hashlib
import json
import os
import re
import shutil
import stat
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
EVIDENCE = "revert-test"
LOCK = "lock"
WAIT = 120
KEEP = 20
NOISE = re.compile(r"^(=== (RUN|PAUSE|CONT|NAME)|--- (FAIL|PASS|SKIP)|\s*$)")
FRAME = re.compile(r"^\s+(\S+\.go:\d+)")
REPORTED = re.compile(r"^\s+\S+\.go:\d+: ")
SCRUB = []
HELD = []


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


def variants(base, paths, untracked):
    """Return (behavioural, panics): lists of variant dicts, block reverts first."""
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
                where = ("%s:%d-%d" % (path, start, start + count - 1) if count
                         else "%s: after line %d" % (path, start))
                mutated = "".join(revert(lines, start, count, old)).encode()
                reverts.append(variant("revert", path, "block reverted to base (%s)" % where,
                                       data, mutated))
        for fn in funcs[path]:
            if any(fn["first"] <= t <= fn["last"] for t in touched):
                where = "%s:%d" % (path, fn["first"])
                zeros.append(variant("zero", path, "func %s returns zero values (%s)"
                                     % (fn["name"], where), data,
                                     insert(data, fn["body"], fn["zero"])))
                panics.append(variant("panic", path, "panic probe in func %s (%s)"
                                      % (fn["name"], where), data,
                                      insert(data, fn["body"], PANIC)))
    return reverts + zeros, panics


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


def hold_lock(directory):
    """Take the exclusive per-worktree lock in directory, waiting up to WAIT seconds.
    Return its descriptor, or None when another holder keeps it. The lock file is
    never removed."""
    os.makedirs(directory, exist_ok=True)
    fd = os.open(os.path.join(directory, LOCK), os.O_RDWR | os.O_CREAT, 0o600)
    deadline = time.time() + WAIT
    while True:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return fd
        except BlockingIOError:
            if time.time() >= deadline:
                os.close(fd)
                return None
            time.sleep(1)


def scratch_path(root):
    """Return this worktree's copy location: outside the repository, private to this
    user, and the same on every run, so Go reuses compiled packages across runs."""
    digest = hashlib.sha256(os.path.realpath(root).encode()).hexdigest()[:12]
    return os.path.join(tempfile.gettempdir(), "revert-test-%d-%s" % (os.getuid(), digest))


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


def run_tests(module_dir, prefix, by_package):
    """Return (outcome, notes). outcome maps (package, test) to {result, output}.
    notes holds "build" and "output" (lines by package) and "stderr" (lines).
    A panic ends its package's binary, so tests left without an outcome are run
    again until none makes progress."""
    outcome, notes = {}, {"build": {}, "output": {}, "stderr": []}
    pending = {p: list(ns) for p, ns in by_package.items()}
    while pending:
        found, found_notes = run_once(module_dir, prefix, pending)
        for key in ("build", "output"):
            for package, lines in found_notes[key].items():
                notes[key].setdefault(package, []).extend(lines)
        notes["stderr"].extend(found_notes["stderr"])
        if not found:
            break
        outcome.update(found)
        pending = {p: [n for n in ns if (p, n) not in outcome] for p, ns in pending.items()}
        pending = {p: ns for p, ns in pending.items() if ns}
    return outcome, notes


def run_once(module_dir, prefix, by_package):
    names = sorted({n for ns in by_package.values() for n in ns})
    run = subprocess.run(
        ["go", "test", "-count=1", "-vet=off", "-json", "-run", "^(%s)$" % "|".join(names)]
        + ["./" + p for p in sorted(by_package)],
        cwd=module_dir, capture_output=True, text=True, pass_fds=tuple(HELD))
    outcome, output, built = {}, {}, {}
    notes = {"build": {}, "output": {}, "stderr": [scrub(l) for l in run.stderr.splitlines()]}
    for line in run.stdout.splitlines():
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
        if action == "output":
            text = scrub(event.get("Output", ""))
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
    return outcome, notes


def judge(tmp, module_dir, prefix, by_package, deps, changes, targets, decisive):
    """Run targets under each variant and return {test: [attempt]} with one attempt
    per variant. An attempt's result is pass, fail, build failed, no result, not
    linked, or not run (the test already had a decisive failure)."""
    attempts = {t: [] for t in targets}
    done = set()
    for index, change in enumerate(changes):
        imported = None if change["kind"] == "file" else import_path(prefix, change["path"])
        linked = set()
        for test in targets:
            if test in done:
                attempts[test].append({"variant": index, "result": "not run"})
            elif imported and imported not in deps[test[0]]:
                attempts[test].append({"variant": index, "result": "not linked"})
            else:
                linked.add(test)
        if not linked:
            continue
        reachable = {}
        for package, name in sorted(linked):
            reachable.setdefault(package, []).append(name)
        target = os.path.join(tmp, change["path"])
        with open(target, "rb") as handle:
            original = handle.read()
        if change["data"] is None:
            os.remove(target)
        else:
            with open(target, "wb") as handle:
                handle.write(change["data"])
        outcome, notes = run_tests(module_dir, prefix, reachable)
        with open(target, "wb") as handle:
            handle.write(original)
        for test in linked:
            attempt = {"variant": index}
            if test in outcome:
                attempt["result"] = outcome[test]["result"]
                attempt["output"] = outcome[test]["output"]
                if attempt["result"] == "fail":
                    attempt["kind"], attempt["excerpt"] = classify(attempt["output"])
            else:
                result, lines = diagnosis(test[0], notes)
                attempt.update(result=result, output=lines, excerpt=excerpt(lines))
            attempts[test].append(attempt)
            if decisive(attempt):
                done.add(test)
        if done >= targets:
            break
    for test in targets:
        seen = len(attempts[test])
        attempts[test].extend({"variant": i, "result": "not run"}
                              for i in range(seen, len(changes)))
    return attempts


def counts(attempts):
    """Summarise attempts, e.g. "1 failed, 3 passed, 2 did not build"."""
    names = [("fail", "failed"), ("pass", "passed"), ("build failed", "did not build"),
             ("no result", "gave no result"), ("not linked", "not linked into the test binary"),
             ("not run", "not run after the decisive failure")]
    tally = {}
    for attempt in attempts:
        tally[attempt["result"]] = tally.get(attempt["result"], 0) + 1
    return ", ".join("%d %s" % (tally[k], label) for k, label in names if tally.get(k)) or "none"


VERDICTS = {"assertion": "an assertion fails under a variant",
            "crash": "crashes under a variant; no assertion fails",
            "non-panic failure": "fails under a variant with no panic and no assertion message"}


def assess(test, baseline, notes, linked, behavioural, attempts, panics, probes):
    """Build the evidence record for one new test."""
    record = {"package": test[0], "test": test[1]}
    base = baseline.get(test)
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
    record["reason"] = ("passes under every variant that ran" if ran
                        else "no variant of the change ran it")
    for result, field in (("build failed", "first_build_error"), ("no result", "first_no_result")):
        broken = next((a for a in attempts if a["result"] == result), None)
        if broken:
            record[field] = "%s: %s" % (behavioural[broken["variant"]]["label"], broken["excerpt"])
    record["probe_attempts"] = probes
    hit = next((a for a in probes if a["result"] == "fail"), None)
    record["probe"] = ("%s made it fail, so it executes that function" % panics[hit["variant"]]["label"]
                       if hit else "no panic probe made it fail (%s)" % counts(probes))
    return record


def render(record, full):
    head = "  %s: %s: %s" % (record["package"], record["test"], record["verdict"])
    if record["verdict"] != "rejected":
        decisive = record["decisive"]
        if not full:
            return [head + " — %s — %s" % (decisive["variant"], decisive["excerpt"])]
        return [head,
                "    mutation: %s" % decisive["variant"],
                "    result: FAIL (%s) — %s" % (decisive["kind"], decisive["excerpt"]),
                "    variants: %s" % record["variants"]]
    lines = [head, "    reason: %s" % record["reason"]]
    if "excerpt" in record:
        lines.append("    baseline: %s — %s" % (record["baseline"]["result"].upper(),
                                                record["excerpt"]))
    if "variants" in record:
        lines.append("    variants: %s" % record["variants"])
    if "first_build_error" in record:
        lines.append("    first build error: %s" % record["first_build_error"])
    if "first_no_result" in record:
        lines.append("    first no-result output: %s" % record["first_no_result"])
    if "probe" in record:
        lines.append("    panic probes: %s" % record["probe"])
    return lines


def write_evidence(directory, key, base, behavioural, panics, records):
    os.makedirs(directory, exist_ok=True)
    path = os.path.join(directory, key[:16] + ".json")
    listed = [{k: v for k, v in change.items() if k != "data"} for change in behavioural + panics]
    with open(path, "w", encoding="utf-8") as handle:
        json.dump({"base": base, "key": key, "behavioural_variants": listed[:len(behavioural)],
                   "panic_probes": listed[len(behavioural):], "tests": records}, handle, indent=1)
    old = sorted((os.path.join(directory, f) for f in os.listdir(directory) if f.endswith(".json")),
                 key=os.path.getmtime)
    for stale in old[:-KEEP]:
        os.remove(stale)
    return path


def main():
    root = git("rev-parse", "--show-toplevel").strip()
    os.chdir(root)
    directory = os.path.join(git("rev-parse", "--absolute-git-dir").strip(), EVIDENCE)
    lock = hold_lock(directory)
    if lock is None:
        print("revert-test: another run held %s for %ds; `lsof` on it names the holder"
              % (os.path.join(directory, LOCK), WAIT), file=sys.stderr)
        return 1
    # Each go test inherits the lock, so a successor cannot replace the copy under it.
    HELD.append(lock)
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

    with open(os.path.join(MODULE, "go.mod"), encoding="utf-8") as handle:
        prefix = re.search(r"^module (\S+)", handle.read(), re.MULTILINE).group(1)
    deps = test_deps(prefix, by_package)
    selected = {(p, n) for p, ns in by_package.items() for n in ns}
    started = time.time()
    tmp = scratch_path(root)
    clear_scratch(tmp)
    os.mkdir(tmp, 0o700)
    try:
        SCRUB.extend([os.path.realpath(tmp) + "/", tmp + "/"])
        copy_repository(root, tmp, {p.split("/")[0] for p in data})
        module_dir = os.path.join(tmp, MODULE)

        baseline, baseline_notes = run_tests(module_dir, prefix, by_package)
        live = {t for t in selected if baseline.get(t, {}).get("result") == "pass"}
        behavioural, panics = variants(base, paths, untracked)
        mutated_packages = {import_path(prefix, c["path"]) for c in behavioural + panics}
        behavioural += data_variants(base, data)
        linked = {t for t in live if data or mutated_packages & deps[t[0]]}
        attempts = judge(tmp, module_dir, prefix, by_package, deps, behavioural, linked,
                         lambda a: a["result"] == "fail" and a["kind"] == "assertion")
        survivors = {t for t in linked
                     if not any(a["result"] == "fail" for a in attempts[t])}
        probes = judge(tmp, module_dir, prefix, by_package, deps, panics, survivors,
                       lambda a: a["result"] == "fail")
    finally:
        clear_scratch(tmp)
    records =[assess(t, baseline, baseline_notes, t in linked, behavioural, attempts.get(t, []),
                      panics, probes.get(t, [])) for t in sorted(selected)]
    path = os.path.relpath(write_evidence(directory, key, base, behavioural, panics, records), root)

    rejected = [r for r in records if r["verdict"] == "rejected"]
    accepted = [r for r in records if r["verdict"] != "rejected"]
    print("revert-test: base %s, %d new test(s), %d variant(s), %d panic probe(s), %.0fs; "
          "evidence: %s" % (base[:8], len(selected), len(behavioural), len(panics),
                            time.time() - started, path), flush=True)
    if not rejected:
        for record in accepted:
            print("\n".join(render(record, full=False)))
        with open(stamp, "w", encoding="utf-8") as handle:
            handle.write(key)
        return 0
    out = []
    if accepted:
        out.append("revert-test: these new tests fail under a variant:")
        for record in accepted:
            out.extend(render(record, full=False))
    out.append("revert-test: these new tests do not show that they test the change:")
    for record in rejected:
        out.extend(render(record, full=True))
    out.append("Make each rejected test fail when the changed code is wrong. If a test "
               "deliberately pins behaviour that already existed, put `%s` in its doc "
               "comment; the commit reviewer sees that claim. The evidence file holds each "
               "variant's diff and the full test output." % MARKER)
    print("\n".join(out), file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
