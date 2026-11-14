#!/usr/bin/env python3
"""Offline checks of scripts/third-party-licenses.py: fake `go version -m` metadata
and temporary module directories stand in for real builds."""

import importlib.util
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "third-party-licenses.py"

spec = importlib.util.spec_from_file_location("third_party_licenses", SCRIPT)
tpl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tpl)


class LinkedModules(unittest.TestCase):
    def test_dep_lines_are_read_and_others_ignored(self):
        meta = ("steer: go1.25.0\n\tpath\tgithub.com/steer-sh/steer-cli\n"
                "\tmod\tgithub.com/steer-sh/steer-cli\t(devel)\t\n"
                "\tdep\tgithub.com/a/b\tv1.0.0\th1:x=\n"
                "\tbuild\tGOOS=linux\n")
        self.assertEqual(tpl.linked_modules(meta), [("github.com/a/b", "v1.0.0")])

    def test_replacement_names_the_module_that_is_linked(self):
        meta = ("\tdep\tgithub.com/a/b\tv1.0.0\t\n"
                "\t=>\tgithub.com/fork/b\tv1.0.1\th1:y=\n"
                "\tdep\tgithub.com/c/d\tv2.0.0\th1:z=\n")
        self.assertEqual(tpl.linked_modules(meta),
                         [("github.com/fork/b", "v1.0.1"), ("github.com/c/d", "v2.0.0")])

    def test_local_replacement_keeps_its_empty_version(self):
        meta = "\tdep\texample.com/lib\tv1.0.0\th1:x=\n\t=>\t../lib\t\t\n"
        self.assertEqual(tpl.linked_modules(meta), [("../lib", "")])

    def test_notice_blocks_read_a_block_comment(self):
        text = "/*\n * Header\n * Redistribution and use in source and binary forms\n */\npackage p\n/* other */\n"
        self.assertEqual(tpl.notice_blocks(text), ["Header\nRedistribution and use in source and binary forms"])

    def test_notice_blocks_keep_only_comment_blocks_with_a_permission_notice(self):
        text = ("// Copyright 2009 The Go Authors.\n// license in the LICENSE file.\n\n"
                "package math\n\n// ====\n// Copyright (C) 1993 Sun\n"
                "// Permission to use, copy, modify, and distribute this\n"
                "// software is freely granted.\n// ====\n\nfunc f() {}\n"
                "\t// Permission is hereby granted\n")
        self.assertEqual(tpl.notice_blocks(text), [
            "====\nCopyright (C) 1993 Sun\nPermission to use, copy, modify, and "
            "distribute this\nsoftware is freely granted.\n====",
            "Permission is hereby granted"])

class NoticeFiles(unittest.TestCase):
    def test_license_notice_and_patents_files_are_kept(self):
        with tempfile.TemporaryDirectory() as d:
            for name in ("LICENSE.txt", "NOTICE", "PATENTS", "COPYING", "README.md",
                         "go.mod"):
                (Path(d) / name).write_text(name)
            (Path(d) / "license").mkdir()
            names = [p.as_posix() for p in tpl.notice_files(d)]
        self.assertEqual(names, ["COPYING", "LICENSE.txt", "NOTICE", "PATENTS"])

    def test_nested_license_counts_and_testdata_does_not(self):
        with tempfile.TemporaryDirectory() as d:
            for rel in ("LICENSE", "utf8/LICENSE", "testdata/LICENSE"):
                (Path(d) / rel).parent.mkdir(parents=True, exist_ok=True)
                (Path(d) / rel).write_text(rel)
            nested = [p.as_posix() for p in tpl.notice_files(d)]
            top = [p.as_posix() for p in tpl.notice_files(d, nested=False)]
        self.assertEqual(nested, ["LICENSE", "utf8/LICENSE"])
        self.assertEqual(top, ["LICENSE"])

    def test_section_reproduces_each_file_under_its_name(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "LICENSE"
            path.write_text("MIT License\n\nCopyright (c) 2020 Someone\n")
            (Path(d) / "sub").mkdir()
            (Path(d) / "sub" / "NOTICE").write_text("Sub notice\n")
            text = tpl.section("github.com/a/b v1.0.0", d, [Path("LICENSE"),
                                                            Path("sub/NOTICE")])
        self.assertIn("github.com/a/b v1.0.0\n", text)
        self.assertIn("--- LICENSE ---\n\nMIT License\n\nCopyright (c) 2020 Someone\n", text)
        self.assertIn("--- sub/NOTICE ---\n\nSub notice\n", text)


class UnicodeData(unittest.TestCase):
    def test_non_test_source_citing_unicode_data_counts(self):
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "tables.go").write_text(
                "// from https://www.unicode.org/Public/15.0.0/ucd/LineBreak.txt\n")
            self.assertTrue(tpl.cites_unicode_data(d))

    def test_test_and_testdata_citations_do_not_count(self):
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "x_test.go").write_text("// unicode.org/Public/x.txt\n")
            (Path(d) / "testdata").mkdir()
            (Path(d) / "testdata" / "gen.go").write_text("// unicode.org/Public/x.txt\n")
            (Path(d) / "x.go").write_text("// see unicode.org/reports/tr29\n")
            self.assertFalse(tpl.cites_unicode_data(d))

    def test_section_names_modules_and_reproduces_the_license(self):
        text = tpl.unicode_section(["github.com/a/b v1.0.0"])
        self.assertIn("  github.com/a/b v1.0.0\n", text)
        self.assertIn("UNICODE LICENSE V3", text)


if __name__ == "__main__":
    unittest.main()
