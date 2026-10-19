#!/usr/bin/env python3
"""Offline checks of the CI revert-test base: scripts/ci-revert-base.py over fixture
repositories with a fake gh, and REVERT_TEST_BASE handling in scripts/revert-test.py."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
RESOLVER = ROOT / "scripts" / "ci-revert-base.py"
REVERT_TEST = ROOT / "scripts" / "revert-test.py"
ZERO = "0" * 40

FAKE_GH = '''#!/usr/bin/env python3
import json, os, sys
query = sys.argv[2]
runs = json.loads(os.environ["FAKE_RUNS"])
if "&status=success" in query:
    runs = [r for r in runs if r.get("conclusion") == "success"]
print(json.dumps({"workflow_runs": runs}))
'''


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True,
                          text=True).stdout.strip()


class Fixture(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.tmp = Path(temp.name)
        self.origin = self.tmp / "origin.git"
        self.work = self.tmp / "work"
        git(self.tmp, "init", "-q", "--bare", "-b", "main", str(self.origin))
        git(self.tmp, "init", "-q", "-b", "main", str(self.work))
        for key, value in (("user.email", "t@t"), ("user.name", "t"), ("commit.gpgsign", "false")):
            git(self.work, "config", key, value)
        git(self.work, "remote", "add", "origin", str(self.origin))
        self.first = self.commit("a.txt", "1")
        self.second = self.commit("a.txt", "2")
        git(self.work, "push", "-q", "origin", "main")
        git(self.work, "fetch", "-q", "origin")
        bin_dir = self.tmp / "bin"
        bin_dir.mkdir()
        (bin_dir / "gh").write_text(FAKE_GH)
        (bin_dir / "gh").chmod(0o755)
        self.path = "%s%s%s" % (bin_dir, os.pathsep, os.environ["PATH"])
        self.env_file = self.tmp / "github_env"

    def commit(self, name, text):
        (self.work / name).write_text(text)
        git(self.work, "add", name)
        git(self.work, "commit", "-qm", "%s=%s" % (name, text))
        return git(self.work, "rev-parse", "HEAD")

    def resolve(self, runs=(), **values):
        env = {"PATH": self.path, "HOME": str(self.tmp), "GITHUB_ENV": str(self.env_file),
               "DEFAULT_BRANCH": "main", "REPOSITORY": "o/r", "BASE_POLICY": "event",
               "FAKE_RUNS": json.dumps(list(runs)), **values}
        result = subprocess.run([sys.executable, str(RESOLVER)], cwd=self.work, env=env,
                                capture_output=True, text=True)
        written = self.env_file.read_text() if self.env_file.exists() else ""
        return result.returncode, result.stdout + result.stderr, written


class PushBase(Fixture):
    def test_existing_branch_uses_before_exactly(self):
        code, out, written = self.resolve(EVENT_NAME="push", REF_NAME="main", BEFORE=self.first)
        self.assertEqual(code, 0, out)
        self.assertEqual(written, "REVERT_TEST_BASE=%s\n" % self.first)

    def test_force_push_fetches_an_unreachable_before(self):
        git(self.work, "checkout", "-q", "-b", "gone", self.first)
        gone = self.commit("b.txt", "lost")
        git(self.work, "push", "-q", "origin", "gone")
        git(self.work, "checkout", "-q", "main")
        git(self.work, "branch", "-q", "-D", "gone")
        git(self.work, "update-ref", "-d", "refs/remotes/origin/gone")
        git(self.work, "reflog", "expire", "--expire=now", "--all")
        git(self.work, "gc", "-q", "--prune=now")
        self.assertNotEqual(subprocess.run(["git", "cat-file", "-e", gone], cwd=self.work).returncode, 0)
        git(self.origin, "config", "uploadpack.allowAnySHA1InWant", "true")
        code, out, written = self.resolve(EVENT_NAME="push", REF_NAME="main", BEFORE=gone)
        self.assertEqual(code, 0, out)
        self.assertEqual(written, "REVERT_TEST_BASE=%s\n" % gone)

    def test_unfetchable_before_fails(self):
        code, out, written = self.resolve(EVENT_NAME="push", REF_NAME="main", BEFORE="f" * 40)
        self.assertEqual(code, 1)
        self.assertIn("cannot be fetched", out)
        self.assertEqual(written, "")

    def test_new_branch_uses_merge_base_with_default(self):
        git(self.work, "checkout", "-q", "-b", "topic", self.first)
        self.commit("c.txt", "topic")
        code, out, written = self.resolve(EVENT_NAME="push", REF_NAME="topic", BEFORE=ZERO)
        self.assertEqual(code, 0, out)
        self.assertEqual(written, "REVERT_TEST_BASE=%s\n" % self.first)

    def test_new_default_branch_fails(self):
        code, out, _ = self.resolve(EVENT_NAME="push", REF_NAME="main", BEFORE=ZERO)
        self.assertEqual(code, 1)
        self.assertIn("has no base", out)

    def test_unknown_event_fails(self):
        code, out, _ = self.resolve(EVENT_NAME="workflow_dispatch")
        self.assertEqual(code, 1)
        self.assertIn("no base policy", out)


class PullRequestBase(Fixture):
    def merge(self):
        git(self.work, "checkout", "-q", "-b", "pr", self.first)
        head = self.commit("d.txt", "pr")
        git(self.work, "checkout", "-q", "--detach", self.second)
        git(self.work, "merge", "-q", "--no-ff", "-m", "merge", head)
        return head

    def test_base_is_first_parent_of_the_merge(self):
        head = self.merge()
        code, out, written = self.resolve(EVENT_NAME="pull_request", PR_HEAD_SHA=head)
        self.assertEqual(code, 0, out)
        self.assertEqual(written, "REVERT_TEST_BASE=%s\n" % self.second)

    def test_second_parent_must_be_the_pr_head(self):
        self.merge()
        code, out, _ = self.resolve(EVENT_NAME="pull_request", PR_HEAD_SHA=self.first)
        self.assertEqual(code, 1)
        self.assertIn("is not the pull request head", out)

    def test_a_non_merge_head_fails(self):
        code, out, _ = self.resolve(EVENT_NAME="pull_request", PR_HEAD_SHA=self.second)
        self.assertEqual(code, 1)
        self.assertIn("not the pull request's merge commit", out)


class ReleaseBase(Fixture):
    def run_of(self, status="completed", conclusion="success", sha=None, branch="main"):
        return {"head_sha": sha or self.second, "head_branch": branch, "status": status,
                "conclusion": conclusion, "html_url": "https://example.test/run/1"}

    def release(self, runs):
        return self.resolve(runs=runs, BASE_POLICY="release", EVENT_NAME="push", REF_NAME="v1")

    def test_passed_push_run_sets_base_to_head(self):
        code, out, written = self.release([self.run_of()])
        self.assertEqual(code, 0, out)
        self.assertEqual(written, "REVERT_TEST_BASE=%s\n" % self.second)
        self.assertIn("https://example.test/run/1", out)

    def test_unfinished_run_asks_for_a_rerun(self):
        code, out, _ = self.release([self.run_of(status="in_progress", conclusion=None)])
        self.assertEqual(code, 1)
        self.assertIn("rerun the release", out)

    def test_failed_run_fails(self):
        code, out, _ = self.release([self.run_of(conclusion="failure")])
        self.assertEqual(code, 1)
        self.assertIn("did not pass", out)

    def test_a_run_on_another_branch_does_not_count(self):
        code, out, _ = self.release([self.run_of(branch="topic")])
        self.assertEqual(code, 1)
        self.assertIn("no check.yml push run", out)

    def test_commit_off_the_default_branch_fails(self):
        git(self.work, "checkout", "-q", "--detach", self.first)
        off = self.commit("e.txt", "off")
        code, out, _ = self.release([self.run_of(sha=off)])
        self.assertEqual(code, 1)
        self.assertIn("is not on main", out)


class RevertTestBase(Fixture):
    def revert_test(self, **values):
        env = {k: v for k, v in os.environ.items()
               if k not in ("REVERT_TEST_BASE", "GITHUB_ACTIONS")}
        env.update(values)
        return subprocess.run([sys.executable, str(REVERT_TEST)], cwd=self.work, env=env,
                              capture_output=True, text=True)

    def test_explicit_base_is_used(self):
        result = self.revert_test(REVERT_TEST_BASE=self.first)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("since %s" % self.first[:8], result.stdout)

    def test_empty_or_unknown_base_fails(self):
        for value in ("", "f" * 40):
            result = self.revert_test(REVERT_TEST_BASE=value)
            self.assertEqual(result.returncode, 1, value)
            self.assertIn("names no commit", result.stderr)

    def test_unset_base_fails_under_github_actions(self):
        result = self.revert_test(GITHUB_ACTIONS="true")
        self.assertEqual(result.returncode, 1)
        self.assertIn("is unset under GitHub Actions", result.stderr)

    def test_unset_base_outside_ci_keeps_the_upstream_default(self):
        result = self.revert_test()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("since %s" % self.second[:8], result.stdout)


if __name__ == "__main__":
    unittest.main()
