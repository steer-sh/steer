#!/usr/bin/env python3
"""Checks revert-test.py's scratch copy and lock against a throwaway fixture repository.

The fixture is a repository shaped like this one: a `cli` module, a changed function
and one new test. The cases are the properties the fixed scratch path depends on:
compiled packages reused between runs, tests still executed, the lock excluding a
second run, an orphaned `go test` holding the lock after its parent dies, and an
unexpected entry at the scratch path refused.

Run it from anywhere; check-full.sh runs it. Exit 0 when every case passes.
"""

import fcntl
import glob
import importlib.util
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "revert-test.py")
FUNCS = os.path.join(HERE, "revert-test-funcs.go")
SLEEP = 6
LOCK_NAME = "lock"
results = []


def report(name, ok, detail=""):
    results.append(ok)
    print("%s  %s %s" % ("PASS" if ok else "FAIL", name, detail.strip()[:200]), flush=True)


def sh(*args, cwd=None, check=True):
    return subprocess.run(args, cwd=cwd, capture_output=True, text=True, check=check)


def fixture(work, name, sleep=0):
    """Build a repository with a committed base, a changed F() and one new test."""
    root = os.path.join(work, name)
    os.makedirs(os.path.join(root, "cli/p"))
    os.makedirs(os.path.join(root, "docs"))
    with open(os.path.join(root, "docs/readme.txt"), "w") as handle:
        handle.write("keep me\n")
    with open(os.path.join(root, "cli/go.mod"), "w") as handle:
        handle.write("module example.com/fx\n\ngo 1.21\n")
    source = os.path.join(root, "cli/p/p.go")
    with open(source, "w") as handle:
        handle.write("package p\n\nfunc F() int {\n\treturn 1\n}\n")
    sh("git", "init", "-q", cwd=root)
    sh("git", "add", ".", cwd=root)
    sh("git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base", cwd=root)
    with open(source, "w") as handle:
        handle.write("package p\n\nfunc F() int {\n\treturn 2\n}\n")
    with open(os.path.join(root, "cli/p/p_test.go"), "w") as handle:
        handle.write("package p\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\n"
                     "func TestF(t *testing.T) {\n\ttime.Sleep(%d * time.Second)\n"
                     "\tif F() != 2 {\n\t\tt.Fatalf(\"F() = %%d\", F())\n\t}\n}\n" % sleep)
    return root


def alive(pid):
    try:
        os.kill(int(pid), 0)
    except (ProcessLookupError, ValueError):
        return False
    return True


def evidence_dir(root):
    return os.path.join(root, ".git/revert-test")


def entries(cache):
    return len(glob.glob(os.path.join(cache, "*/*-d")))


def run_script(root, cache):
    """Run revert-test.py over root with its own build cache, ignoring the pass stamp."""
    stamp = os.path.join(evidence_dir(root), "pass")
    if os.path.exists(stamp):
        os.remove(stamp)
    return subprocess.run([sys.executable, SCRIPT], cwd=root, capture_output=True, text=True,
                          env=dict(os.environ, GOCACHE=cache, GOFLAGS=""))


def module():
    spec = importlib.util.spec_from_file_location("revert_test", SCRIPT)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


def check_reuse(work, cache):
    """A repeated run must reuse every compiled package and still run the test."""
    root = fixture(work, "reuse")
    first = run_script(root, cache)
    run_script(root, cache)
    before = entries(cache)
    last = run_script(root, cache)
    report("run exits 0", first.returncode == 0 and last.returncode == 0,
           first.stderr + last.stderr)
    report("a repeated run adds no cache entries", entries(cache) == before,
           "(+%d)" % (entries(cache) - before))
    record = json.load(open(sorted(glob.glob(os.path.join(evidence_dir(root), "*.json")),
                                   key=os.path.getmtime)[-1]))["tests"][0]
    report("the repeated run ran the test",
           record["baseline"]["result"] == "pass"
           and any(a["result"] == "fail" for a in record.get("attempts", [])),
           "verdict=%s" % record.get("verdict"))
    report("the scratch copy is removed", not os.path.exists(module().scratch_path(root)))
    report("a symlinked entry's target is kept",
           open(os.path.join(root, "docs/readme.txt")).read() == "keep me\n")
    return root


def check_lock(root, cache):
    """A second run waits for the lock, gives up at WAIT, then proceeds once it frees."""
    loaded = module()
    loaded.WAIT = 3
    fd = os.open(os.path.join(evidence_dir(root), LOCK_NAME), os.O_RDWR)
    fcntl.flock(fd, fcntl.LOCK_EX)
    started = time.time()
    denied = loaded.hold_lock(evidence_dir(root))
    waited = time.time() - started
    report("a held lock stops hold_lock at WAIT", denied is None and 2.5 <= waited < 6,
           "(%.1fs)" % waited)
    blocked = subprocess.run(
        [sys.executable, "-c",
         "import importlib.util,sys;s=importlib.util.spec_from_file_location('r',%r);"
         "m=importlib.util.module_from_spec(s);s.loader.exec_module(m);m.WAIT=2;"
         "sys.exit(m.main())" % SCRIPT],
        cwd=root, capture_output=True, text=True, env=dict(os.environ, GOCACHE=cache))
    report("a held lock fails the run and names the lock",
           blocked.returncode == 1 and "lsof" in blocked.stderr, blocked.stderr)
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)
    freed = loaded.hold_lock(evidence_dir(root))
    report("a freed lock is acquired", freed is not None)
    if freed is not None:
        os.close(freed)


def check_orphan(work, cache):
    """Killing the parent must leave the lock with the go test it started."""
    root = fixture(work, "orphan", sleep=SLEEP)
    running = subprocess.Popen([sys.executable, SCRIPT], cwd=root,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                               env=dict(os.environ, GOCACHE=cache, GOFLAGS=""))
    lock_path = os.path.join(evidence_dir(root), LOCK_NAME)
    holders, deadline = [], time.time() + 120
    while time.time() < deadline and len(holders) < 2:
        holders = sh("lsof", "-t", lock_path, check=False).stdout.split()
        time.sleep(0.2)
    inherited = [pid for pid in holders if pid != str(running.pid)]
    os.kill(running.pid, signal.SIGKILL)
    running.wait()
    fd = os.open(lock_path, os.O_RDWR)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        held = False
        fcntl.flock(fd, fcntl.LOCK_UN)
    except BlockingIOError:
        held = True
    report("a killed parent leaves the lock with its go test", bool(inherited) and held,
           "(pid %s)" % " ".join(inherited))
    started = time.time()
    fcntl.flock(fd, fcntl.LOCK_EX)
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)
    report("the lock frees when that go test exits", held and not any(alive(p) for p in inherited),
           "(%.1fs)" % (time.time() - started))
    report("the next run clears the copy the orphan left",
           run_script(root, cache).returncode == 0)


def check_refusal(work, root, cache):
    """An entry at the scratch path that this script did not make stops the run."""
    victim = os.path.join(work, "victim")
    os.makedirs(victim)
    open(os.path.join(victim, "file"), "w").close()
    scratch = module().scratch_path(root)
    os.symlink(victim, scratch)
    try:
        planted = run_script(root, cache)
        report("a symlink at the scratch path fails the run",
               planted.returncode != 0 and "not a directory owned" in planted.stderr,
               planted.stderr)
        report("its target is untouched", os.path.exists(os.path.join(victim, "file")))
    finally:
        os.remove(scratch)


def main():
    for path in (SCRIPT, FUNCS):
        if not os.path.exists(path):
            print("revert-test-verify: %s is missing" % path, file=sys.stderr)
            return 1
    work = tempfile.mkdtemp(prefix="revert-test-verify-")
    cache = os.path.join(work, "cache")
    try:
        root = check_reuse(work, cache)
        check_lock(root, cache)
        check_orphan(work, cache)
        check_refusal(work, root, cache)
    finally:
        subprocess.run(["chmod", "-R", "u+w", work])
        shutil.rmtree(work, ignore_errors=True)
    print("revert-test-verify: %d/%d passed" % (sum(results), len(results)))
    return 0 if all(results) else 1


if __name__ == "__main__":
    sys.exit(main())
