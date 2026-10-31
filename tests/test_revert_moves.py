#!/usr/bin/env python3
"""Checks which moved tests scripts/revert-test.py counts as new, over fixture repositories."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REVERT_TEST = ROOT / "scripts" / "revert-test.py"
HEADER = 'package p\n\nimport "testing"\n\n'
FOO = "func TestFoo(t *testing.T) {\n\tif 1+1 != 2 {\n\t\tt.Fatal(1)\n\t}\n}\n"
BAR = "func TestBar(t *testing.T) {\n\tif 2+2 != 4 {\n\t\tt.Fatal(2)\n\t}\n}\n"
LINKS_NONE = "its test binary links none of the changed packages"


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True,
                          text=True).stdout


class Moves(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.work = Path(temp.name)
        git(self.work, "init", "-q")
        self.write({"cli/go.mod": "module example.com/fx\n\ngo 1.21\n",
                    "cli/p/p.go": "package p\n\nfunc F() int {\n\treturn 1\n}\n",
                    "cli/q/q.go": "package q\n",
                    "cli/p/a_test.go": HEADER + FOO})

    def write(self, files):
        for path, text in files.items():
            target = self.work / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(text)

    def commit(self):
        git(self.work, "add", "-A")
        git(self.work, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false",
            "commit", "-qm", "base")

    def run_check(self):
        env = {k: v for k, v in os.environ.items()
               if k not in ("REVERT_TEST_BASE", "GITHUB_ACTIONS", "GOFLAGS")}
        return subprocess.run([sys.executable, str(REVERT_TEST)], cwd=self.work, env=env,
                              capture_output=True, text=True)

    def assertNothingNew(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("no new test functions", result.stdout)

    def assertJudged(self, result, *names):
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(self.judged(), set(names), result.stderr)

    def judged(self):
        store = self.work / ".git" / "revert-test"
        newest = max(store.glob("*.json"), key=os.path.getmtime)
        return {r["test"] for r in json.loads(newest.read_text())["tests"]}

    def move_to_b(self, text):
        (self.work / "cli/p/a_test.go").unlink()
        self.write({"cli/p/b_test.go": text})

    def test_git_mv_is_not_new(self):
        self.commit()
        git(self.work, "mv", "cli/p/a_test.go", "cli/p/b_test.go")
        self.assertNothingNew(self.run_check())

    def test_a_move_between_existing_files_is_not_new(self):
        self.write({"cli/p/b_test.go": HEADER + BAR})
        self.commit()
        self.write({"cli/p/a_test.go": "package p\n",
                    "cli/p/b_test.go": HEADER + BAR + "\n" + FOO})
        self.assertNothingNew(self.run_check())

    def test_a_moved_test_may_lose_its_doc_comment(self):
        self.write({"cli/p/a_test.go": HEADER + "// TestFoo adds.\n" + FOO})
        self.commit()
        self.move_to_b(HEADER + FOO)
        self.assertNothingNew(self.run_check())

    def test_a_changed_body_is_judged(self):
        self.commit()
        self.move_to_b(HEADER + FOO.replace("t.Fatal(1)", "t.Log(1)"))
        result = self.run_check()
        self.assertJudged(result, "TestFoo")
        self.assertIn(LINKS_NONE, result.stderr)

    def test_a_changed_comment_in_the_body_is_judged(self):
        self.commit()
        self.move_to_b(HEADER + FOO.replace("\tif 1+1", "\t// adds\n\tif 1+1"))
        self.assertJudged(self.run_check(), "TestFoo")

    def test_a_change_past_a_raw_string_closing_brace_is_judged(self):
        body = ("func TestFoo(t *testing.T) {\n\tconst src = `\nfunc x() {\n}\n`\n"
                "\tif len(src) == 0 {\n\t\tt.Fatal(src)\n\t}\n}\n")
        self.write({"cli/p/a_test.go": HEADER + body})
        self.commit()
        self.move_to_b(HEADER + body.replace("t.Fatal(src)", "t.Log(src)"))
        self.assertJudged(self.run_check(), "TestFoo")

    def test_a_move_across_packages_is_judged(self):
        self.commit()
        (self.work / "cli/p/a_test.go").unlink()
        self.write({"cli/q/a_test.go": HEADER.replace("package p", "package q") + FOO})
        result = self.run_check()
        self.assertJudged(result, "TestFoo")
        self.assertIn(LINKS_NONE, result.stderr)

    def test_a_move_to_the_external_test_package_is_judged(self):
        self.commit()
        self.move_to_b(HEADER.replace("package p", "package p_test") + FOO)
        self.assertJudged(self.run_check(), "TestFoo")

    def test_a_rename_is_judged(self):
        self.commit()
        self.move_to_b(HEADER + FOO.replace("TestFoo", "TestFooRenamed"))
        self.assertJudged(self.run_check(), "TestFooRenamed")

    def test_a_new_test_beside_a_moved_one_is_judged_alone(self):
        self.commit()
        new = "func TestF(t *testing.T) {\n\tif F() != 2 {\n\t\tt.Fatal(F())\n\t}\n}\n"
        self.move_to_b(HEADER + FOO + "\n" + new)
        self.write({"cli/p/p.go": "package p\n\nfunc F() int {\n\treturn 2\n}\n"})
        result = self.run_check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.judged(), {"TestF"})

    def test_a_vacuous_new_test_beside_a_moved_one_is_rejected(self):
        self.commit()
        self.move_to_b(HEADER + FOO + "\n" + BAR)
        self.assertJudged(self.run_check(), "TestBar")

    def test_the_marker_still_exempts_a_changed_move(self):
        self.commit()
        self.move_to_b(HEADER + "// pins existing behaviour\n" + FOO.replace("t.Fatal(1)", "t.Log(1)"))
        self.assertNothingNew(self.run_check())

    def test_a_move_under_a_build_tag_is_judged(self):
        self.commit()
        self.move_to_b("//go:build integration\n\n" + HEADER + FOO)
        self.assertJudged(self.run_check(), "TestFoo")

    def test_a_move_between_files_of_one_build_tag_is_not_new(self):
        tagged = "//go:build integration\n\n" + HEADER + FOO
        self.write({"cli/p/a_test.go": tagged})
        self.commit()
        self.move_to_b(tagged)
        self.assertNothingNew(self.run_check())

    def test_a_move_to_a_platform_file_is_judged(self):
        self.commit()
        (self.work / "cli/p/a_test.go").unlink()
        self.write({"cli/p/a_plan9_test.go": HEADER + FOO})
        self.assertJudged(self.run_check(), "TestFoo")

    def test_a_move_to_a_file_go_ignores_is_judged(self):
        self.commit()
        (self.work / "cli/p/a_test.go").unlink()
        self.write({"cli/p/_a_test.go": HEADER + FOO})
        self.assertJudged(self.run_check(), "TestFoo")

    def test_a_move_out_of_a_file_go_ignores_is_judged(self):
        (self.work / "cli/p/a_test.go").unlink()
        self.write({"cli/p/_a_test.go": HEADER + FOO})
        self.commit()
        (self.work / "cli/p/_a_test.go").unlink()
        self.write({"cli/p/a_test.go": HEADER + FOO})
        self.assertJudged(self.run_check(), "TestFoo")

    def imports(self, alias_of_check):
        return ('package p\n\nimport (\n\t"testing"\n\n\t%s\n)\n\n' % alias_of_check
                + "func TestFoo(t *testing.T) {\n\tif check.ToUpper(\"a\") == \"\" {\n"
                  "\t\tt.Fatal(1)\n\t}\n}\n")

    def test_a_move_where_an_alias_names_another_package_is_judged(self):
        self.write({"cli/p/a_test.go": self.imports('check "strings"')})
        self.commit()
        self.move_to_b(self.imports('check "bytes"'))
        self.assertJudged(self.run_check(), "TestFoo")

    def test_a_move_beside_imports_it_does_not_use_is_not_new(self):
        self.write({"cli/p/a_test.go": self.imports('check "strings"')})
        self.commit()
        self.move_to_b(self.imports('check "strings"').replace(
            '"testing"\n', '"testing"\n\t"unicode"\n') + "\nvar _ = unicode.MaxRune\n")
        self.assertNothingNew(self.run_check())

    def test_a_name_inside_a_raw_string_at_the_base_is_not_a_test(self):
        self.write({"cli/p/a_test.go": HEADER + "const src = `\n" + BAR + "`\n"})
        self.commit()
        self.write({"cli/p/b_test.go": HEADER + BAR})
        self.assertJudged(self.run_check(), "TestBar")

    def test_a_spent_budget_leaves_a_moved_test_undecided(self):
        self.commit()
        self.move_to_b(HEADER + FOO)
        load = ("import importlib.util,sys;s=importlib.util.spec_from_file_location('r',%r);"
                "m=importlib.util.module_from_spec(s);s.loader.exec_module(m);m.BUDGET=0;"
                "sys.exit(m.main())" % str(REVERT_TEST))
        result = subprocess.run([sys.executable, "-c", load], cwd=self.work, capture_output=True,
                                text=True)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("incomplete", result.stderr)
        self.assertIn("Undecided: p: TestFoo", result.stderr)


if __name__ == "__main__":
    unittest.main()
