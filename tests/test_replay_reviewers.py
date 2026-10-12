#!/usr/bin/env python3
"""Offline checks of tests/replay_reviewers.py's ledger reader and tally."""

import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import replay_reviewers  # noqa: E402

GIT_ENV = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
           "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
           "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"}


def row(chain, firing, phase, diff_file="d.diff"):
    return {"chain_id": chain, "firing": firing, "phase": phase, "base_sha": "b" * 40,
            "diff_file": diff_file, "subject": "Fix  the\nparser"}


class ChainRoundsTest(unittest.TestCase):
    def test_keeps_reviewed_rounds_of_named_chains_in_order(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp)
            rows = [row("bbb", 2, "deny", "b2"), row("aaa", 1, "deny", "a1"), row("bbb", 1, "deny", "b1"),
                    row("aaa", 2, "retry", "a2"), row("ccc", 1, "deny", "c1"), row("aaa", 2, "clean", "a2"),
                    {**row("aaa", 3, "deny", "a3"), "base_sha": None}]
            (state / "adjudication.host.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))
            (state / "adjudication.host.jsonl.bak.1").write_text(json.dumps(row("aaa", 3, "deny")) + "\n")
            with contextlib.redirect_stderr(io.StringIO()):
                got = replay_reviewers.chain_rounds(state, ["bbb", "aaa"])
        self.assertEqual([(c, n, f) for c, n, _, f, _ in got],
                         [("bbb", 1, "b1"), ("bbb", 2, "b2"), ("aaa", 1, "a1"), ("aaa", 2, "a2")])
        self.assertEqual(got[0][4], "Fix the parser")


class StageTest(unittest.TestCase):
    def git(self, repo, *argv):
        return subprocess.run(["git", "-C", str(repo), *argv], check=True, capture_output=True,
                              text=True, env=GIT_ENV).stdout.strip()

    def test_amend_diff_stages_on_the_parent_of_base(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp) / "repo"
            repo.mkdir()
            self.git(repo, "init", "-q")
            (repo / "a.txt").write_text("one\n")
            self.git(repo, "add", "a.txt")
            self.git(repo, "commit", "-qm", "one")
            parent = self.git(repo, "rev-parse", "HEAD")
            (repo / "a.txt").write_text("two\n")
            self.git(repo, "commit", "-qam", "two")
            head = self.git(repo, "rev-parse", "HEAD")
            diff = Path(tmp) / "amend.diff"
            diff.write_text(self.git(repo, "diff", parent, head) + "\n")
            got = replay_reviewers.stage(repo, Path(tmp) / "wt", head, diff, GIT_ENV)
            self.assertEqual(got, head + "^")
            self.assertEqual((Path(tmp) / "wt" / "a.txt").read_text(), "two\n")
            self.assertEqual(self.git(Path(tmp) / "wt", "rev-parse", "HEAD"), head)
            self.assertEqual(self.git(Path(tmp) / "wt", "diff", "--cached", "--name-only", parent), "a.txt")
            self.assertIsNone(replay_reviewers.stage(repo, Path(tmp) / "wt2", parent, diff.with_name("none"), GIT_ENV))

    def test_amended_root_commit_stages_on_the_empty_tree(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp) / "repo"
            repo.mkdir()
            self.git(repo, "init", "-q")
            (repo / "a.txt").write_text("one\n")
            self.git(repo, "add", "a.txt")
            self.git(repo, "commit", "-qm", "root")
            head = self.git(repo, "rev-parse", "HEAD")
            (repo / "a.txt").write_text("two\n")
            self.git(repo, "add", "a.txt")
            diff = Path(tmp) / "amend.diff"
            empty = self.git(repo, "hash-object", "-t", "tree", "/dev/null")
            diff.write_text(self.git(repo, "diff", "--cached", empty) + "\n")
            wt = Path(tmp) / "wt"
            got = replay_reviewers.stage(repo, wt, head, diff, GIT_ENV)
            self.assertEqual(got, empty)
            self.assertEqual((wt / "a.txt").read_text(), "two\n")
            self.assertEqual(self.git(wt, "rev-parse", "HEAD"), head)

    def test_binary_sections_stay_unstaged(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp) / "repo"
            repo.mkdir()
            self.git(repo, "init", "-q")
            (repo / "a.png").write_bytes(b"x\0")
            (repo / "b.txt").write_text("hi\n")
            self.git(repo, "add", ".")
            self.git(repo, "commit", "-qm", "one")
            head = self.git(repo, "rev-parse", "HEAD")
            (repo / "a.png").write_bytes(b"y\0")
            (repo / "b.txt").write_text("ho\n")
            text = self.git(repo, "diff") + "\n"
            diff = Path(tmp) / "bin.diff"
            diff.write_text(text)
            binaries = replay_reviewers.binary_paths(text)
            self.assertEqual(binaries, ["a.png"])
            wt = Path(tmp) / "wt"
            self.assertEqual(replay_reviewers.stage(repo, wt, head, diff, GIT_ENV, binaries), head)
            self.assertEqual((wt / "b.txt").read_text(), "ho\n")

    def test_elision_names_what_the_budget_left_out(self):
        self.assertEqual(replay_reviewers.elision("[diff is 90000 chars — too large"), "stat only")
        self.assertIn("dropped", replay_reviewers.elision("[The review harness dropped 2 file(s)"))
        self.assertEqual(replay_reviewers.elision("[Excluded from the diff below\n\ndiff --git"), "")
        self.assertIn("dropped", replay_reviewers.elision(
            "[Excluded from the diff below]\n\n[The review harness dropped 1 file(s)]\n\ndiff --git"))
        self.assertEqual(replay_reviewers.elision("diff --git a/x b/x\n+[diff is quoted]"), "")


class TallyTest(unittest.TestCase):
    def test_panel_curve_counts_cells_a_subset_reaches(self):
        for got, want in zip(replay_reviewers.panel_curve([{1}, {1, 2, 3}], 3), [4 / 3, 5 / 3, 2.0]):
            self.assertAlmostEqual(got, want)

    def test_tally_reports_hits_per_run_and_found_markers(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            out.joinpath("arm-abc-r1.json").write_text(json.dumps({
                "verdict": "deny", "elapsed_s": 10, "answered": 2,
                "texts": ["the parser drops a newline", "", "usage key is wrong"]}))
            markers = out / "m.json"
            markers.write_text(json.dumps({"accepted": {"nl": "newline", "other": "zzz"},
                                           "rejected": {"usage": "usage key"}}))
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                replay_reviewers.main(["tally", "--out", str(out), "--markers", str(markers)])
        text = buf.getvalue()
        self.assertIn("abc r1 accepted  nl", text)
        self.assertIn("runs [1] of 3 (1 lost)", text)
        self.assertIn("abc r1 rejected  usage", text)
        self.assertIn("accepted markers found: 1 of 2", text)

    def test_tally_reports_each_panel_size_apart(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            for name, texts in (("arm-abc-r1", ["newline"]), ("arm-abc-r2", ["", "", "newline"])):
                out.joinpath(name + ".json").write_text(json.dumps(
                    {"verdict": "deny", "elapsed_s": 1, "answered": len(texts), "texts": texts}))
            markers = out / "m.json"
            markers.write_text(json.dumps({"accepted": {"nl": "newline"}}))
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                replay_reviewers.main(["tally", "--out", str(out), "--markers", str(markers)])
        text = buf.getvalue()
        self.assertIn("expected cells by panel size: 1:1.00 of 1", text)
        self.assertIn("expected cells by panel size: 1:0.33  2:0.67  3:1.00 of 1", text)


if __name__ == "__main__":
    unittest.main()
