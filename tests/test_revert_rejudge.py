#!/usr/bin/env python3
"""Checks how scripts/revert-test.py judges recorded tests again after a checker edit."""

import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCRIPTS = ROOT / "scripts"
SPEC = importlib.util.spec_from_file_location("revert_test", SCRIPTS / "revert-test.py")
revert_test = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(revert_test)

BASE = "package p\n\nfunc F() int {\n\treturn 1\n}\n\n\n\n\nfunc G() int {\n\treturn 2\n}\n"
CHANGED = BASE.replace("return 1", "return 10").replace("return 2", "return 20")
TEST = ('package p\n\nimport "testing"\n\n'
        "func TestG(t *testing.T) {\n\tif G() != 20 {\n\t\tt.Fatal(G())\n\t}\n}\n")


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True,
                          text=True).stdout


def record(test, checker, label=None):
    out = {"package": "p", "test": test, "checker": checker, "inputs": "i"}
    if label:
        out["decisive"] = {"variant": label}
    return out


class Store(unittest.TestCase):
    def test_a_checker_edit_keeps_undecided_and_unselected_records_as_hints(self):
        records = {("p", "TestOld"): record("TestOld", "old"),
                   ("p", "TestOut"): record("TestOut", "old"),
                   ("p", "TestSame"): record("TestSame", "new"),
                   ("p", "TestUndecided"): record("TestUndecided", "old", "v"),
                   ("p", "TestRejected"): record("TestRejected", "old", "v")}
        judged = {("p", "TestOld"), ("p", "TestUndecided"), ("p", "TestRejected")}
        accepted = {("p", "TestOld"): record("TestOld", "new")}
        store = revert_test.next_store(records, "new", judged, {}, accepted,
                                       {("p", "TestUndecided")})
        self.assertEqual(set(store), {("p", "TestOld"), ("p", "TestOut"), ("p", "TestSame"),
                                      ("p", "TestUndecided")})
        self.assertEqual(store[("p", "TestOut")]["checker"], "old")
        self.assertEqual(store[("p", "TestOld")]["checker"], "new")
        self.assertEqual(store[("p", "TestUndecided")]["checker"], "old")

    def test_a_record_without_its_own_digest_takes_the_stores(self):
        with tempfile.TemporaryDirectory() as directory:
            with open(os.path.join(directory, revert_test.ACCEPTED), "w") as handle:
                json.dump({"checker": "old", "tests": [{"package": "p", "test": "TestA"},
                                                       record("TestB", "mine")]}, handle)
            records = revert_test.read_accepted(directory)
        self.assertEqual(records[("p", "TestA")]["checker"], "old")
        self.assertEqual(records[("p", "TestB")]["checker"], "mine")

    def test_hinted_variants_move_to_the_front_in_order(self):
        changes = [{"label": name} for name in ("a", "b", "c", "d")]
        records = {("p", "TestX"): record("TestX", "old", "c"),
                   ("p", "TestY"): record("TestY", "old", "b"),
                   ("p", "TestZ"): record("TestZ", "old", "a")}
        order = revert_test.hinted_first(changes, records, {("p", "TestX"), ("p", "TestY")})
        self.assertEqual([c["label"] for c in order], ["b", "c", "a", "d"])


class CheckerEdit(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.work = Path(temp.name) / "repo"
        self.edited = Path(temp.name) / "edited"
        self.edited.mkdir()
        shutil.copy(SCRIPTS / "revert-test-funcs.go", self.edited)
        script = (SCRIPTS / "revert-test.py").read_text()
        (self.edited / "revert-test.py").write_text(script + "# an edit to the checker\n")
        self.write({"cli/go.mod": "module example.com/fx\n\ngo 1.21\n", "cli/p/p.go": BASE})
        git(self.work, "init", "-q")
        git(self.work, "add", "-A")
        git(self.work, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false",
            "commit", "-qm", "base")
        self.write({"cli/p/p.go": CHANGED, "cli/p/a_test.go": TEST})

    def write(self, files):
        for path, text in files.items():
            target = self.work / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(text)

    def run_check(self, script):
        env = {k: v for k, v in os.environ.items()
               if k not in ("REVERT_TEST_BASE", "GITHUB_ACTIONS", "GOFLAGS")}
        env["REVERT_TEST_WORKERS"] = "1"
        result = subprocess.run([sys.executable, str(script)], cwd=self.work, env=env,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def checkers(self):
        stored = json.loads((self.work / ".git" / "revert-test" / "accepted").read_text())
        return {r["checker"] for r in stored["tests"]}

    def evidence(self):
        store = self.work / ".git" / "revert-test"
        newest = max(store.glob("*.json"), key=os.path.getmtime)
        return json.loads(newest.read_text())

    def test_a_checker_edit_judges_again_with_the_deciding_variant_first(self):
        self.run_check(SCRIPTS / "revert-test.py")
        first = self.evidence()
        before = self.checkers()
        decided = first["tests"][0]["decisive"]["variant"]
        self.assertNotEqual(first["behavioural_variants"][0]["label"], decided)

        result = self.run_check(self.edited / "revert-test.py")
        self.assertIn("1 judged, 1 accepted", result.stdout)
        second = self.evidence()
        self.assertEqual(second["behavioural_variants"][0]["label"], decided)
        self.assertEqual(second["tests"][0]["decisive"]["variant"], decided)
        self.assertEqual(sorted(v["label"] for v in second["behavioural_variants"]),
                         sorted(v["label"] for v in first["behavioural_variants"]))
        after = self.checkers()
        self.assertEqual(len(before | after), 2, (before, after))


if __name__ == "__main__":
    unittest.main()
