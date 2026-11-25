#!/usr/bin/env python3
"""Checks that scripts/revert-test.py keeps its Go build cache and work directory
private to the run and removes the cache files no build used recently."""

import importlib.util
import os
import tempfile
import time
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent.parent / "scripts"
SPEC = importlib.util.spec_from_file_location("revert_test", SCRIPTS / "revert-test.py")
revert_test = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(revert_test)


class GoCacheTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.directory = Path(self.tmp.name)
        self.saved = {k: os.environ.get(k) for k in ("GOCACHE", "GOTMPDIR")}

    def tearDown(self):
        for key, value in self.saved.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value
        self.tmp.cleanup()

    def test_go_commands_use_directories_under_the_evidence_directory(self):
        stale = self.directory / revert_test.GO_WORK / "go-build123"
        stale.mkdir(parents=True)
        cache, work = revert_test.private_go_dirs(str(self.directory))
        self.assertEqual(os.environ["GOCACHE"], cache)
        self.assertEqual(os.environ["GOTMPDIR"], work)
        self.assertTrue(cache.startswith(str(self.directory)))
        self.assertTrue(os.path.isdir(cache))
        self.assertEqual(os.listdir(work), [])

    def test_trim_removes_only_entries_older_than_the_use_window(self):
        cache = self.directory / "gocache"
        (cache / "ab").mkdir(parents=True)
        begun = time.time()
        unused = cache / "ab" / "unused-a"
        used = cache / "ab" / "used-a"
        top = cache / "trim.txt"
        for path in (unused, used, top):
            path.write_text("x")
        executable = cache / "ab" / "tool-d"
        executable.mkdir()
        old = begun - revert_test.GO_TOUCH - 10
        os.utime(unused, (old, old))
        os.utime(top, (old, old))
        os.utime(executable, (old, old))
        within = begun - revert_test.GO_TOUCH + 10
        os.utime(used, (within, within))
        revert_test.trim_go_cache(str(cache), begun)
        self.assertFalse(unused.exists())
        self.assertTrue(used.exists())
        self.assertTrue(top.exists())
        self.assertTrue(executable.is_dir())


if __name__ == "__main__":
    unittest.main()
