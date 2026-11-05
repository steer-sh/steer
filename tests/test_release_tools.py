#!/usr/bin/env python3
"""Offline release-tool checks; fake reviewer replies exercise the real gate."""

import argparse
import contextlib
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import fresh_install
import live_reviewers
import sandbox

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("preflight", ROOT / "scripts" / "preflight-release.py")
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)

STUB = '''#!/usr/bin/env python3
import json, sys, time
from pathlib import Path
argv = sys.argv[1:]
provider = Path(sys.argv[0]).name
prompt = argv[argv.index('-p') + 1] if provider == 'agy' else sys.stdin.read()
finding = "[1] SEVERITY: BLOCKING\\n    FILE: average.py\\n    CLAIM: Wrong divisor.\\n    FAILS WHEN: A singleton raises division by zero."
text = finding if 'average.py' in prompt else 'NO FINDINGS'
if SLOW and 'average.py' in prompt:
    print('partial review output', flush=True)
    print('partial reviewer diagnostic', file=sys.stderr, flush=True)
    time.sleep(10)
if CLEAN:
    text = 'NO FINDINGS'
if provider == 'claude':
    print(json.dumps({'result': text, 'is_error': False}))
elif provider == 'agy':
    print(json.dumps({'response': text, 'status': 'SUCCESS'}))
else:
    print(text)
'''


class ReleasePreflight(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in (*preflight.MARKETS, *preflight.PLUGINS, "README.md", "hooks/hooks.json"):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, target)
        for name in ("scripts/steer", *(f"bin/{platform}/steer" for platform in
                     ("darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"))):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            header = preflight.HEADERS.get(name)
            data = b"#!/bin/sh\n" if header is None else header[0].ljust(header[1]) + (header[2] or b"")
            target.write_bytes(data + b" fixture executable stamp abc1234")
            target.chmod(0o755)
        for name in preflight.SKILL_FILES:
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("fixture skill")
        self.expected = preflight.identity(self.root)
        self.digests = preflight.artifact_digests(self.root)
        self.skill_digests = preflight.artifact_digests(self.root, preflight.SKILL_FILES)

    def inspect(self):
        return preflight.inspect_checkout(self.root, self.expected, expected_digests=self.digests,
                                          expected_skill_digests=self.skill_digests, expected_stamp="abc1234")

    def test_checkout_and_missing_executable(self):
        self.inspect()
        (self.root / "bin/linux-arm64/steer").chmod(0o644)
        with self.assertRaisesRegex(ValueError, "not executable"):
            self.inspect()

    def test_readme_and_version_disagreement(self):
        (self.root / "README.md").write_text("marketplace add wrong/repository\nplugin install steer@wrong")
        with self.assertRaisesRegex(ValueError, "README marketplace"):
            self.inspect()
        path = self.root / preflight.PLUGINS[0]
        doc = json.loads(path.read_text())
        doc["version"] = "stale"
        path.write_text(json.dumps(doc))
        with self.assertRaisesRegex(ValueError, "differs"):
            preflight.identity(self.root)

    def test_missing_routing_and_audit_files_are_refused(self):
        for name in ("skills/steer/prompts/routing.md", "skills/steer-report/audit.md"):
            with self.subTest(name=name):
                path = self.root / name
                path.unlink()
                with self.assertRaisesRegex(ValueError, name):
                    self.inspect()
                path.write_text("fixture skill")

    def test_empty_published_hook_wiring_is_refused(self):
        (self.root / "hooks/hooks.json").write_text("{}")
        with self.assertRaisesRegex(ValueError, "hook wiring"):
            self.inspect()

    def test_external_skill_symlinks_are_refused(self):
        with tempfile.TemporaryDirectory() as outside:
            target = Path(outside) / "audit.md"
            target.write_text("external skill")
            path = self.root / "skills/steer-report/audit.md"
            path.unlink()
            path.symlink_to(target)
            with self.assertRaisesRegex(ValueError, "outside the bundle"):
                self.inspect()

    def test_wrong_target_unstamped_executable_and_changed_launcher_are_refused(self):
        path = self.root / "bin/linux-arm64/steer"
        good = path.read_bytes()
        for data in (b"", (self.root / "bin/linux-amd64/steer").read_bytes(),
                     (self.root / "bin/darwin-arm64/steer").read_bytes()):
            with self.subTest(data=data):
                path.write_bytes(data)
                with self.assertRaisesRegex(ValueError, "not built for its target"):
                    self.inspect()
        path.write_bytes(good.replace(b"abc1234", b"def5678"))
        with self.assertRaisesRegex(ValueError, "lacks the candidate build stamp"):
            self.inspect()
        path.write_bytes(good)
        (self.root / "scripts/steer").write_text("changed launcher")
        with self.assertRaisesRegex(ValueError, "launcher bytes differ"):
            self.inspect()

    def test_source_other_than_a_repository_ref_is_refused(self):
        repository = self.expected[1]["repository"]
        for source in ("./", {"source": "url", "url": repository + ".git"},
                       {"source": "url", "url": "https://github.com/other/steer-skill.git", "ref": "release"}):
            with self.subTest(source=source):
                for name in preflight.MARKETS:
                    path = self.root / name
                    doc = json.loads(path.read_text())
                    doc["plugins"][0]["source"] = source
                    path.write_text(json.dumps(doc))
                with self.assertRaisesRegex(ValueError, "url source on the repository"):
                    preflight.identity(self.root)

    def test_stale_skill_contents_are_refused(self):
        for name in ("skills/steer/prompts/routing.md", "skills/steer-report/audit.md"):
            with self.subTest(name=name):
                path = self.root / name
                path.write_text("stale published skill")
                with self.assertRaisesRegex(ValueError, "skill bytes differ"):
                    self.inspect()
                path.write_text("fixture skill")

    def test_private_repository_is_refused_before_clone(self):
        with patch.object(preflight, "urlopen", return_value=io.BytesIO(b'{"private":true}')), \
             patch.object(preflight.subprocess, "run") as clone:
            with self.assertRaisesRegex(ValueError, "not public"):
                preflight.preflight(self.root)
            clone.assert_not_called()

    def test_public_ref_is_checked_without_operator_git_settings(self):
        branches = []

        def clone(argv, **kwargs):
            env = kwargs["env"]
            self.assertNotIn("GIT_CONFIG_COUNT", env)
            self.assertNotIn("GH_TOKEN", env)
            self.assertEqual(env["GIT_CONFIG_GLOBAL"], os.devnull)
            self.assertEqual(env["GIT_CONFIG_NOSYSTEM"], "1")
            self.assertEqual(env["GIT_TERMINAL_PROMPT"], "0")
            self.assertIn("credential.helper=", argv)
            branches.append(argv[argv.index("--branch") + 1])
            self.assertNotEqual(Path(kwargs["cwd"]), ROOT)
            shutil.copytree(self.root, argv[-1])

        with patch.object(preflight, "urlopen", return_value=io.BytesIO(
                b'{"private":false,"default_branch":"main"}')), \
             patch.object(preflight.subprocess, "run", side_effect=clone), \
             patch.object(preflight.subprocess, "check_output", side_effect=lambda argv, **_:
                          "abc1234\n" if "--short=7" in argv else "fixture-sha\n"), \
             patch.dict(os.environ, {"GIT_CONFIG_COUNT": "1", "GH_TOKEN": "fixture-token"}):
            result = preflight.preflight(self.root, "release-fixture")
        self.assertEqual(branches, ["main", "release-fixture"])
        self.assertEqual(result["commit"], "fixture-sha")
        self.assertEqual(result["ref"], "release-fixture")
        self.assertEqual(result["listing"], "main")


class SandboxClaudeLogin(unittest.TestCase):
    def test_login_reaches_settings_env_and_leaves_with_the_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            box = sandbox.Sandbox(Path(tmp), guarded=[], audit_log=Path(tmp) / "none",
                                  ledger_dir=Path(tmp) / "none")
            path = box.home / ".claude" / "settings.json"
            path.write_text(json.dumps({"hooks": {}}))
            with patch.dict(os.environ, {"CLAUDE_CODE_OAUTH_TOKEN": "tok"}, clear=True):
                box.write_claude_login()
            settings = json.loads(path.read_text())
            self.assertEqual(settings["env"], {"CLAUDE_CODE_OAUTH_TOKEN": "tok"})
            self.assertEqual(settings["hooks"], {})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            box.drop_claude_login()
            self.assertEqual(json.loads(path.read_text()), {"hooks": {}})


class LiveReviewerRunner(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory()
        cls.steer = Path(cls.build.name) / "steer"
        subprocess.run(["go", "build", "-o", str(cls.steer), "."], cwd=ROOT / "cli", check=True)

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def cell(self, provider, clean=False, slow=False, state_override=None, git_override=None):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            fakehome = root / "operator"
            (fakehome / ".codex").mkdir(parents=True)
            (fakehome / ".codex/auth.json").write_text("{}")
            token = fakehome / sandbox.AGY_TOKEN
            token.parent.mkdir(parents=True)
            token.write_text("fixture login")
            bindir = root / "bin"
            bindir.mkdir()
            for name in live_reviewers.BINARIES.values():
                stub = bindir / name
                stub.write_text(STUB.replace("CLEAN", repr(clean)).replace("SLOW", repr(slow)))
                stub.chmod(0o755)
            args = argparse.Namespace(provider=provider, model="fixture-model", effort="low",
                                      output=root / "result", steer_bin=self.steer, timeout=2 if slow else 15, keep=False)
            extra = {}
            external = root / "operator-state"
            if state_override:
                extra = {state_override: str(external), "COMMIT_GATE_RUNS": "9",
                         "COMMIT_GATE_MODEL": "operator-model", "COMMIT_GATE_EFFORT": "high",
                         "COMMIT_GATE_TIMEOUT": "1"}
            git_env = {"PATH": os.environ["PATH"], "HOME": str(fakehome),
                       "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1", **sandbox.GIT_IDENTITY}
            operator = root / "operator-repo"
            hook_marker = root / "operator-hook-ran"
            head = None
            if git_override == "repository":
                operator.mkdir()
                subprocess.run(["git", "init", "-q", str(operator)], env=git_env, check=True)
                (operator / "average.py").write_text("operator file")
                (operator / "check.sh").write_text("operator check")
                subprocess.run(["git", "-C", str(operator), "add", "."], env=git_env, check=True)
                subprocess.run(["git", "-C", str(operator), "commit", "-qm", "operator commit"], env=git_env, check=True)
                (operator / "average.py").write_text("uncommitted operator change")
                head = subprocess.check_output(["git", "-C", str(operator), "rev-parse", "HEAD"], env=git_env)
                extra.update(GIT_DIR=str(operator / ".git"), GIT_WORK_TREE=str(operator),
                             GIT_INDEX_FILE=str(operator / ".git" / "index"))
            elif git_override == "hooks":
                hooks = root / "operator-hooks"
                hooks.mkdir()
                hook = hooks / "pre-commit"
                hook.write_text("#!/bin/sh\ntouch " + str(hook_marker) + "\n")
                hook.chmod(0o755)
                extra.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0=str(hooks))
            with patch.object(sandbox, "REAL_HOME", fakehome), patch.dict(os.environ, {
                    "PATH": str(bindir) + os.pathsep + os.environ["PATH"],
                    "CLAUDE_CODE_OAUTH_TOKEN": "fixture-not-a-real-token", **extra}), \
                    contextlib.redirect_stdout(io.StringIO()):
                code = live_reviewers.run_cell(args)
            result = json.loads((args.output / "result.json").read_text())
            if state_override:
                self.assertFalse(external.exists(), "cell wrote into operator-selected state")
            self.assertFalse(hook_marker.exists(), "cell executed operator hooks")
            if head is not None:
                self.assertEqual(head, subprocess.check_output(
                    ["git", "-C", str(operator), "rev-parse", "HEAD"], env=git_env))
                self.assertEqual(subprocess.check_output(
                    ["git", "-C", str(operator), "diff", "--cached"], env=git_env), b"")
            if slow:
                trace = sorted(args.output.glob("call-*"))[-1]
                self.assertIn("partial review output", (trace / "stdout.txt").read_text())
                self.assertIn("partial reviewer diagnostic", (trace / "stderr.txt").read_text())
            return code, result

    def test_three_providers_through_registered_gate(self):
        for provider in live_reviewers.BINARIES:
            with self.subTest(provider=provider):
                code, result = self.cell(provider)
                self.assertEqual(code, 0, result["failures"])
                self.assertEqual(result["ledger"][0]["reviewers"], 1)

    def test_clean_reply_cannot_pass_planted_defect_cell(self):
        code, result = self.cell("codex", clean=True)
        self.assertEqual(code, 1)
        self.assertTrue(any("did not block" in f for f in result["failures"]))

    def test_codex_isolation_and_prompt_transport_are_required(self):
        call = {"argv": ["exec", "--model", "fixture", "-"], "stdin_chars": 0, "returncode": 0}
        failures = live_reviewers.check_call(call, "codex", "fixture", "")
        self.assertTrue(any("isolation" in f for f in failures))
        self.assertTrue(any("transport" in f for f in failures))

    def test_deadline_retains_partial_output_and_interrupted_timing(self):
        code, result = self.cell("codex", slow=True)
        self.assertEqual(code, 1)
        call = result["calls"][-1]
        self.assertIsNone(call["returncode"])
        self.assertIn("interrupted", call["termination"])
        self.assertGreaterEqual(call["seconds_upper_bound"], 1)

    def test_legacy_environment_cannot_redirect_state_or_expand_panel(self):
        for key in ("COMMIT_GATE_STATE_DIR", "COMMIT_REVIEW_STATE"):
            with self.subTest(key=key):
                code, result = self.cell("codex", state_override=key)
                self.assertEqual(code, 0, result["failures"])
                self.assertEqual(len(result["calls"]), 2)

    def test_git_environment_cannot_change_operator_repository_or_run_hooks(self):
        for kind in ("repository", "hooks"):
            with self.subTest(kind=kind):
                code, result = self.cell("codex", git_override=kind)
                self.assertEqual(code, 0, result["failures"])


class FreshInstallRunner(unittest.TestCase):
    def test_reads_each_harness_install_block_from_the_readme(self):
        readme = (ROOT / "README.md").read_text()
        self.assertEqual(fresh_install.install_commands(readme, "claude-code"), [
            "claude plugin marketplace add steer-sh/steer",
            "claude plugin install steer@steer-marketplace"])
        self.assertEqual(fresh_install.install_commands(readme, "codex"), [
            "codex plugin marketplace add steer-sh/steer",
            "codex plugin add steer@steer-marketplace"])

    def test_reads_each_harness_update_block_from_the_readme(self):
        readme = (ROOT / "README.md").read_text()
        self.assertEqual(fresh_install.install_commands(readme, "claude-code", "Update"), [
            "claude plugin marketplace update steer-marketplace",
            "claude plugin update steer@steer-marketplace"])
        self.assertEqual(fresh_install.install_commands(readme, "codex", "Update"), [
            "codex plugin marketplace upgrade steer-marketplace"])

    def test_an_update_moves_the_marketplace_clone_out_of_the_image_layer(self):
        program = fresh_install.script(["true"], "codex", "9.9.9", update=True)
        marketplace = fresh_install.MARKETPLACES["codex"]
        self.assertLess(program.index(f"mv {marketplace}.layer {marketplace}"), program.index("true"))
        self.assertNotIn(".layer", fresh_install.script(["true"], "codex", "9.9.9"))

    def test_a_block_outside_install_is_not_read(self):
        readme = "## Install\n\nNothing.\n\n## Update\n\nFor Codex:\n\n```bash\ncodex x\n```\n"
        with self.assertRaises(ValueError):
            fresh_install.install_commands(readme, "codex")

    def test_the_script_requires_the_expected_version(self):
        program = fresh_install.script(["true"], "codex", "9.9.9")
        self.assertIn("~/.codex/plugins/cache/steer-marketplace/steer/9.9.9", program)
        self.assertIn('"steer 9.9.9 "*', program)


if __name__ == "__main__":
    unittest.main()
