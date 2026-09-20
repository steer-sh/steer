#!/usr/bin/env python3
"""Checks revert-test.py against throwaway fixture repositories.

Each fixture is a repository shaped like this one: a `cli` module, changed code and
new tests. The first cases are the properties the fixed scratch path depends on:
compiled packages reused between runs, tests still executed, the lock excluding a
second run, an orphaned `go test` holding the lock after its parent dies, and an
unexpected entry at the scratch path refused. The rest cover the statement
variants: which statements become candidates, dedup and caps, every variant
building, the kind of variant that decides each negative-property test, a test
with no assertion staying rejected, and a hung variant stopped and cleaned up.

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


def make_repo(work, name, base, changed):
    """Build a repository whose base commit holds base and whose working tree adds changed."""
    root = os.path.join(work, name)
    files = dict(base, **{"cli/go.mod": "module example.com/fx\n\ngo 1.21\n"})
    for tree in (files, changed):
        for path, text in tree.items():
            os.makedirs(os.path.dirname(os.path.join(root, path)), exist_ok=True)
            with open(os.path.join(root, path), "w") as handle:
                handle.write(text)
        if tree is files:
            sh("git", "init", "-q", cwd=root)
            sh("git", "add", ".", cwd=root)
            sh("git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base",
               cwd=root)
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


def run_with(root, cache, **settings):
    """Run revert-test.py's main() over root with module constants replaced."""
    assign = "".join("m.%s=%r;" % item for item in settings.items())
    return subprocess.run(
        [sys.executable, "-c",
         "import importlib.util,sys;s=importlib.util.spec_from_file_location('r',%r);"
         "m=importlib.util.module_from_spec(s);s.loader.exec_module(m);%s"
         "sys.exit(m.main())" % (SCRIPT, assign)],
        cwd=root, capture_output=True, text=True, env=dict(os.environ, GOCACHE=cache, GOFLAGS=""))


def latest_evidence(root):
    return json.load(open(sorted(glob.glob(os.path.join(evidence_dir(root), "*.json")),
                                 key=os.path.getmtime)[-1]))


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
    fd = os.open(os.path.join(evidence_dir(root), LOCK_NAME), os.O_RDWR)
    fcntl.flock(fd, fcntl.LOCK_EX)
    started = time.time()
    bounded = run_with(root, cache, WAIT=120, BUDGET=3)
    waited = time.time() - started
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)
    report("the time budget bounds the lock wait", bounded.returncode == 1 and waited < 10,
           "(%.1fs) %s" % (waited, bounded.stderr))


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
    waited = time.time() - started
    grace = time.time() + 2
    while any(alive(p) for p in inherited) and time.time() < grace:
        time.sleep(0.05)
    report("the lock frees when that go test exits",
           held and waited >= SLEEP - 2 and not any(alive(p) for p in inherited),
           "(%.1fs)" % waited)
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


SITES_BASE = """package p

var sink []int

func Old(xs []int) {
	sink = xs
}

func Mixed(xs []int) int {
	n := 0
	n++
	return n
}
"""

SITES_CHANGED = """package p

var sink []int

func Old(xs []int) {
	sink = xs
}

func Mixed(xs []int) int {
	n := 0
	n++
	var y int
	y = n
	n += y
	_ = y
	for i := 0; i < 2; i++ {
		if i == 0 {
			continue
		}
	}
	switch k := n; k {
	case 0:
		return 0
	}
L:
	for {
		break L
	}
	func() { sink = xs }()
	if n > 5 {
		return 1
	} else {
		n = 2
	}
	return n
}

func Blank(xs []int) {
	y := len(xs)
	_ = y
	_, _ = y, xs
	_ = len(xs)
	_ = copy(sink, xs)
}
"""


def generated(root):
    """Return (early, late, omitted) from revert-test.py's variants() over root."""
    loaded, here = module(), os.getcwd()
    os.chdir(root)
    try:
        base = loaded.base_revision()
        paths, untracked = loaded.changed_paths(base)
        early, late, _, omitted = loaded.variants(base, paths, untracked, time.monotonic() + 120)
    finally:
        os.chdir(here)
    return early, late, omitted


def sites(changes):
    return sorted((c["kind"], c["label"].split(": ", 1)[1][:-1])
                  for c in changes if c["kind"] in ("copy", "alias", "guard", "suppress"))


def check_sites(work):
    """Only eligible statements on added lines become candidates, guards before the rest."""
    root = make_repo(work, "sites", {"cli/p/p.go": SITES_BASE}, {"cli/p/p.go": SITES_CHANGED})
    early, late, omitted = generated(root)
    want = [("copy", "_ = copy(sink, xs)"), ("guard", "if i == 0 {"),
            ("suppress", "_ = len(xs)"), ("suppress", "func() { sink = xs }()"),
            ("suppress", "n = 2"), ("suppress", "y = n")]
    got = sites(early + late)
    report("only added eligible statements are candidates", got == want, repr(got))
    report("guards run before other suppressions",
           [c["kind"] for c in late] == ["guard"] + ["suppress"] * 4,
           repr([c["label"] for c in late]))
    report("no candidate is omitted under the caps", omitted == [], repr(omitted))


def check_caps(work):
    """The caps keep 4 per function and RUN_CAP per run, the run cap spends itself one
    site per function at a time, and a revert's bytes are not repeated."""
    names = [chr(ord("A") + i) for i in range(15)]
    counts = [(names[0], 6)] + [(name, 4) for name in names[1:]]
    source = "package c\n\nvar n int\n\n" + "".join(
        "func %s() {\n%s}\n\n" % (name, "\tn++\n" * count) for name, count in counts)
    root = make_repo(work, "caps",
                     {"cli/c/a.go": "package c\n\nfunc h() {}\n\nfunc G() {\n\tif false { h() }\n}\n"},
                     {"cli/c/a.go": "package c\n\nfunc h() {}\n\nfunc G() {\n\th()\n}\n",
                      "cli/c/c.go": source})
    early, late, omitted = generated(root)
    kept = [c for c in late if c["kind"] == "suppress"]
    reasons = [o["reason"] for o in omitted]
    loaded = module()
    cap, eligible = loaded.RUN_CAP, sum(min(n, loaded.FUNCTION_CAP) for _, n in counts)
    # The fixture must exceed the run cap, or every case below asserts nothing.
    report("the fixture overruns the run cap", eligible > cap,
           "(%d sites, cap %d)" % (eligible, cap))
    report("the run cap keeps its whole budget", len(kept) == cap,
           "(%d, cap %d)" % (len(kept), cap))
    report("the function cap omits 2 of A's 6",
           sum(1 for o in omitted if "func A (" in o["label"] and "per function" in o["reason"]) == 2,
           repr(omitted))
    per_function = [sum(1 for c in kept if "func %s (" % name in c["label"]) for name in names]
    # The old order was alphabetical by path and offset, which spent the whole budget on
    # the first functions and left the last ones with nothing.
    report("the run cap starves no function", min(per_function) >= cap // len(names),
           repr(dict(zip(names, per_function))))
    report("the run cap omits the overflow, not a whole function",
           sum(1 for o in omitted if "per run" in o["reason"]) == eligible - cap, repr(reasons))
    report("a suppression equal to a block revert is omitted as a duplicate",
           reasons.count("same bytes as an earlier variant") == 1
           and any("func G" in o["label"] for o in omitted), repr(reasons))


REFERENCES = """package r

import (
	"fmt"
	"os"
	"slices"
)

func Use(xs []int) []int {
	v := len(xs)
	fmt.Println(v)
	ys := slices.Clone(xs)
	copy(ys, xs)
	defer os.Getenv("X")
	for i := range xs {
		if i > 2 {
			goto done
		}
	}
done:
	return ys
}
"""


def check_references(work, cache):
    """Every statement variant builds where the statement holds the only reference."""
    root = make_repo(work, "references", {}, {"cli/r/r.go": REFERENCES})
    early, late, _ = generated(root)
    changes = [c for c in early + late if c["kind"] != "zero"]
    target = os.path.join(root, "cli/r/r.go")
    failed = []
    for change in changes:
        with open(target, "wb") as handle:
            handle.write(change["data"])
        built = sh("go", "build", "./...", cwd=os.path.join(root, "cli"), check=False)
        if built.returncode != 0:
            failed.append("%s: %s" % (change["label"], built.stderr.strip()))
    with open(target, "w") as handle:
        handle.write(REFERENCES)
    report("each statement kind has a variant",
           {c["kind"] for c in changes} == {"copy", "alias", "guard", "suppress"},
           repr(sorted({c["kind"] for c in changes})))
    report("every statement variant builds", not failed, " | ".join(failed))


KILLS = """package k

import (
	"slices"
	"strings"
)

type Store struct{ items []int }

func (s *Store) Keep(xs []int) {
	s.items = xs
	s.items = slices.Clone(s.items)
}

func CloneA(xs []int) []int {
	c := slices.Clone(xs)
	return c
}

func CloneB(xs []int) []int {
	c := append([]int(nil), xs...)
	return c
}

func CloneC(xs []int) []int {
	c := append([]int{}, xs...)
	return c
}

func Join(root, name string) string {
	if strings.Contains(name, "..") {
		return ""
	}
	return root + "/" + name
}

func SumUntilNegative(xs []int) int {
	s := 0
	for _, v := range xs {
		if v < 0 {
			break
		}
		s += v
	}
	return s
}

func CountNonNegative(xs []int) int {
	n := 0
	for _, v := range xs {
		if v < 0 {
			continue
		}
		n++
	}
	return n
}

func Clamp(xs []int) (out []int) {
	for _, v := range xs {
		if v < 0 {
			goto stop
		}
		out = append(out, v)
	}
stop:
	return
}

func Touch(s *Store) {
	s.items = nil
}
"""

KILL_TESTS = """package k

import "testing"

func aliased(t *testing.T, clone func([]int) []int) {
	xs := []int{1}
	c := clone(xs)
	xs[0] = 9
	if len(c) > 0 && c[0] == 9 {
		t.Fatal("the result aliases its input")
	}
}

func TestKeepCopies(t *testing.T) {
	xs := []int{1}
	s := &Store{}
	s.Keep(xs)
	xs[0] = 9
	if len(s.items) > 0 && s.items[0] == 9 {
		t.Fatal("Keep aliases its input")
	}
}

func TestCloneA(t *testing.T) { aliased(t, CloneA) }

func TestCloneB(t *testing.T) { aliased(t, CloneB) }

func TestCloneC(t *testing.T) { aliased(t, CloneC) }

func TestJoinRefusesParent(t *testing.T) {
	if got := Join("r", "../x"); got != "" {
		t.Fatalf("Join = %q", got)
	}
}

func TestSumStopsAtNegative(t *testing.T) {
	if got := SumUntilNegative([]int{-1, 5}); got != 0 {
		t.Fatalf("sum = %d", got)
	}
}

func TestCountSkipsNegative(t *testing.T) {
	if got := CountNonNegative([]int{-1}); got != 0 {
		t.Fatalf("count = %d", got)
	}
}

func TestClampStopsAtNegative(t *testing.T) {
	if got := Clamp([]int{-1, 5}); len(got) != 0 {
		t.Fatalf("clamp = %v", got)
	}
}

func TestTouchRuns(t *testing.T) {
	Touch(&Store{})
}
"""


def check_kills(work, cache):
    """Each negative-property test survives the zero variant and is decided by its kind."""
    root = make_repo(work, "kills", {}, {"cli/k/k.go": KILLS, "cli/k/k_test.go": KILL_TESTS})
    run = run_script(root, cache)
    evidence = latest_evidence(root)
    kinds = {v["label"]: v["kind"] for v in evidence["behavioural_variants"]}
    records = {r["test"]: r for r in evidence["tests"]}
    want = {"TestKeepCopies": "copy", "TestCloneA": "alias", "TestCloneB": "alias",
            "TestCloneC": "alias", "TestJoinRefusesParent": "guard",
            "TestSumStopsAtNegative": "guard", "TestCountSkipsNegative": "guard",
            "TestClampStopsAtNegative": "guard"}
    for test, kind in sorted(want.items()):
        record = records.get(test, {})
        decided = kinds.get(record.get("decisive", {}).get("variant"))
        zero = [a for a in record.get("attempts", [])
                if evidence["behavioural_variants"][a["variant"]]["kind"] == "zero"]
        report("%s is decided by a %s variant" % (test, kind),
               decided == kind and all(a["result"] in ("pass", "not run") for a in zero),
               "decided by %s; %s" % (decided, record.get("verdict")))
    touch = records.get("TestTouchRuns", {})
    report("a test with no assertion stays rejected",
           run.returncode == 1 and touch.get("verdict") == "rejected"
           and touch.get("reason") == "passes under every variant that ran", repr(touch.get("reason")))
    report("the rejection tells the author to keep the test and extend the generator",
           "Keep each rejected test" in run.stderr and "extend the variant generator" in run.stderr,
           run.stderr[-300:])
    broken = [a for r in evidence["tests"] for a in r.get("attempts", [])
              if a["result"] == "build failed"]
    report("no variant in the kill fixture fails to build", not broken,
           repr([evidence["behavioural_variants"][a["variant"]]["label"] for a in broken]))


def events(package, *rows):
    return "\n".join(json.dumps(dict(row, Package="example.com/fx/" + package)) for row in rows)


ALARM = ["panic: test timed out after 3s\n", "\trunning tests:\n", "\t\tTestHang (3s)\n",
         "\t\tTestOther (3s)\n", "\n", "goroutine 4 [running]:\n"]


def check_alarm_attribution():
    """A timeout alarm names its running tests wherever go test attributes the output."""
    loaded = module()
    selected = {"t": ["TestFast", "TestHang", "TestOther"]}
    runs = [{"Action": "run", "Test": name} for name in selected["t"]]
    cases = {
        "a completed test": [dict(Action="output", Test="TestFast", Output=l) for l in ALARM],
        "the package": [dict(Action="output", Output=l) for l in ALARM],
        "a running test": [dict(Action="output", Test="TestOther", Output="log\n")]
                          + [dict(Action="output", Test="TestHang", Output=l) for l in ALARM],
    }
    for name, alarm in cases.items():
        stdout = events("t", *(runs + [{"Action": "pass", "Test": "TestFast"}] + alarm
                               + [{"Action": "fail"}]))
        loaded.go_test = lambda args, cwd, deadline, stdout=stdout: (stdout, "")
        outcome, _ = loaded.run_once("cli", "example.com/fx", selected, 0)
        results = {test: row["result"] for (_, test), row in outcome.items()}
        other = outcome.get(("t", "TestOther"), {}).get("output", [])
        report("an alarm attributed to %s times out both running tests" % name,
               results == {"TestFast": "pass", "TestHang": "timed out", "TestOther": "timed out"}
               and ALARM[0].rstrip("\n") in other, repr(results))


def hang_repo(work, name):
    pidfile = os.path.join(work, name + ".pid")
    source = "package h\n\nfunc Spin(n int) int {\n\ti := 0\n\tfor i < n {\n\t\ti++\n\t}\n\treturn i\n}\n"
    test = ("package h\n\nimport (\n\t\"os\"\n\t\"strconv\"\n\t\"testing\"\n)\n\n"
            "func TestSpinBounded(t *testing.T) {\n"
            "\tos.WriteFile(%r, []byte(strconv.Itoa(os.Getpid())), 0o600)\n"
            "\tif Spin(3) > 3 {\n\t\tt.Fatal(\"spun past n\")\n\t}\n}\n" % pidfile).replace("'", '"')
    return make_repo(work, name, {}, {"cli/h/h.go": source, "cli/h/h_test.go": test}), pidfile


def dead(pidfile, wait=10):
    try:
        pid = open(pidfile).read().strip()
    except OSError:
        return False
    deadline = time.time() + wait
    while alive(pid) and time.time() < deadline:
        time.sleep(0.2)
    return not alive(pid)


def check_timeout(work, cache):
    """A hung variant times out, never counts as a kill, and leaves no process behind."""
    root, pidfile = hang_repo(work, "timeout")
    started = time.time()
    run = run_with(root, cache, TEST_TIMEOUT=3, BUDGET=120)
    record = latest_evidence(root)["tests"][0]
    results = [a["result"] for a in record.get("attempts", [])]
    report("a hung variant gives a timed-out attempt, not a kill",
           "timed out" in results and "fail" not in results and record["verdict"] == "rejected",
           "%s in %.0fs" % (results, time.time() - started))
    report("the timed-out test binary exits", dead(pidfile))

    root, pidfile = hang_repo(work, "budget")
    started = time.time()
    run = run_with(root, cache, TEST_TIMEOUT=600, BUDGET=15)
    evidence = latest_evidence(root)
    report("an exhausted budget writes incomplete evidence and fails the run",
           run.returncode == 1 and evidence["incomplete"] and "incomplete" in run.stderr,
           "%.0fs %s" % (time.time() - started, run.stderr[-200:]))
    report("an exhausted budget gives no test-repair instruction",
           "Keep each rejected test" not in run.stderr
           and "Undecided: h: TestSpinBounded" in run.stderr, run.stderr[-300:])
    report("an exhausted budget records no pass",
           not os.path.exists(os.path.join(evidence_dir(root), "pass")))
    report("the budget kill ends the whole process group", dead(pidfile))

    root, _ = hang_repo(work, "setup")
    run = run_with(root, cache, BUDGET=0)
    evidence = latest_evidence(root)
    report("a budget spent before setup ends writes incomplete evidence",
           run.returncode == 1 and evidence["incomplete"]
           and [r.get("reason") for r in evidence["tests"]] == [module().BUDGET_REASON]
           and not os.path.exists(os.path.join(evidence_dir(root), "pass")), run.stderr[-300:])


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
        check_sites(work)
        check_alarm_attribution()
        check_caps(work)
        check_references(work, cache)
        check_kills(work, cache)
        check_timeout(work, cache)
    finally:
        subprocess.run(["chmod", "-R", "u+w", work])
        shutil.rmtree(work, ignore_errors=True)
    print("revert-test-verify: %d/%d passed" % (sum(results), len(results)))
    return 0 if all(results) else 1


if __name__ == "__main__":
    sys.exit(main())
