#!/usr/bin/env python3
"""Offline checks of scripts/check-binaries.py: fake `go version -m` metadata and a
fake host binary stand in for real builds."""

import importlib.util
import os
import struct
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHECKER = ROOT / "scripts" / "check-binaries.py"

spec = importlib.util.spec_from_file_location("check_binaries", CHECKER)
cb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cb)

PKG = cb.VERSION_PKG


def meta(goos="linux", goarch="amd64", cgo="0", version="1.2.3", commit="abc1234",
         ldflags=None):
    if ldflags is None:
        ldflags = (f"\"-s -w -X '{PKG}.Version={version}' -X '{PKG}.GitCommit={commit}' "
                   f"-X '{PKG}.BuildDate=2026-10-17T00:00:00Z'\"")
    lines = ["steer: go1.25.0", "\tpath\tgithub.com/steer-sh/steer-cli",
             f"\tbuild\t-ldflags={ldflags}", f"\tbuild\tCGO_ENABLED={cgo}",
             f"\tbuild\tGOARCH={goarch}", f"\tbuild\tGOOS={goos}"]
    return "\n".join(line for line in lines if not line.endswith("=None")) + "\n"


class TargetErrors(unittest.TestCase):
    def errors(self, text, target="linux-amd64"):
        return cb.target_errors(target, text, "1.2.3", "abc1234")

    def test_matching_metadata_passes(self):
        self.assertEqual(self.errors(meta()), [])

    def test_cgo_enabled_fails(self):
        self.assertEqual(self.errors(meta(cgo="1")), ["CGO_ENABLED=1, want 0"])

    def test_absent_cgo_setting_fails(self):
        self.assertEqual(self.errors(meta(cgo=None)), ["CGO_ENABLED=<absent>, want 0"])

    def test_wrong_architecture_fails(self):
        self.assertEqual(self.errors(meta(goarch="arm64")), ["GOARCH=arm64, want amd64"])

    def test_wrong_os_fails(self):
        self.assertEqual(self.errors(meta(goos="darwin")), ["GOOS=darwin, want linux"])

    def test_wrong_version_fails(self):
        self.assertEqual(self.errors(meta(version="dev")),
                         ["Version stamp is dev, want 1.2.3"])

    def test_version_substring_does_not_pass(self):
        self.assertEqual(self.errors(meta(version="1.2.30")),
                         ["Version stamp is 1.2.30, want 1.2.3"])

    def test_wrong_commit_fails(self):
        self.assertEqual(self.errors(meta(commit="fffffff")),
                         ["GitCommit stamp is fffffff, want abc1234"])

    def test_absent_ldflags_fails(self):
        text = "\n".join(l for l in meta().splitlines() if "-ldflags" not in l)
        self.assertEqual(self.errors(text), ["-ldflags setting is absent"])

    def test_malformed_ldflags_fails(self):
        errors = self.errors(meta(ldflags="\"-X 'unterminated\""))
        self.assertEqual(len(errors), 1)
        self.assertTrue(errors[0].startswith("-ldflags does not parse"), errors)


def macho(*cmds, blob=None):
    """Return a thin arm64 Mach-O header, bare 16-byte load commands, then a trailing
    signature blob that each LC_CODE_SIGNATURE points at."""
    if blob is None:
        blob = struct.pack(">II", cb.CSMAGIC_EMBEDDED_SIGNATURE, 12) + bytes(4)
    blob_offset = 32 + 16 * len(cmds)
    body = b"".join(
        struct.pack("<IIII", cmd, 16, blob_offset, len(blob))
        if cmd == cb.LC_CODE_SIGNATURE else struct.pack("<II", cmd, 16) + bytes(8)
        for cmd in cmds)
    return struct.pack("<IiiIIIII", cb.MACHO_MAGIC_64, 0x0100000C, 0, 2, len(cmds),
                       len(body), 0, 0) + body + blob


class SignatureError(unittest.TestCase):
    def test_signed_image_passes(self):
        self.assertIsNone(cb.signature_error(macho(0x19, cb.LC_CODE_SIGNATURE)))

    def test_unsigned_image_fails(self):
        self.assertIn("no LC_CODE_SIGNATURE", cb.signature_error(macho(0x19, 0x2)))

    def test_signature_past_end_of_file_fails(self):
        image = macho(cb.LC_CODE_SIGNATURE)
        self.assertIn("outside the", cb.signature_error(image[:-8]))

    def test_zeroed_signature_blob_fails(self):
        self.assertIn("no embedded signature blob",
                      cb.signature_error(macho(cb.LC_CODE_SIGNATURE, blob=bytes(16))))

    def test_truncated_signature_command_fails(self):
        image = macho(cb.LC_CODE_SIGNATURE)
        self.assertIn("past the end", cb.signature_error(image[:40]))

    def test_non_macho_fails(self):
        self.assertIn("not a little-endian 64-bit Mach-O",
                      cb.signature_error(b"#!/bin/sh\nexit 0\n" + bytes(32)))

    def test_truncated_commands_fail(self):
        self.assertIn("past the end", cb.signature_error(macho(0x19)[:36]))


FAKE_GO = '''#!/usr/bin/env python3
import os, sys
target = os.path.basename(os.path.dirname(sys.argv[3]))
goos, goarch = target.split("-")
cgo = os.environ.get("FAKE_CGO_" + target.replace("-", "_"), "0")
pkg = "github.com/steer-sh/steer-cli/internal/version"
print(f"steer: go1.25.0\\n\\tbuild\\t-ldflags=\\"-X '{pkg}.Version=1.2.3' -X '{pkg}.GitCommit=abc1234'\\"")
print(f"\\tbuild\\tCGO_ENABLED={cgo}\\n\\tbuild\\tGOARCH={goarch}\\n\\tbuild\\tGOOS={goos}")
'''


class Check(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.tmp = Path(temp.name)
        self.go = self.tmp / "go"
        self.go.write_text(FAKE_GO)
        self.go.chmod(0o755)
        self.bin = self.tmp / "bin"
        for target in cb.TARGETS:
            self.binary(target, "#!/bin/sh\nexit 0\n")
        for target in cb.SIGNED_TARGETS:
            self.write(target, macho(cb.LC_CODE_SIGNATURE))

    def binary(self, target, script):
        self.write(target, script.encode())

    def write(self, target, data):
        path = self.bin / target / "steer"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        path.chmod(0o755)

    def check(self, host="linux-amd64", timeout=5):
        return cb.check(self.bin, "1.2.3", "abc1234", str(self.go), host, timeout)

    def test_all_targets_pass(self):
        self.assertEqual(self.check(), [])

    def test_missing_target_fails(self):
        (self.bin / "darwin-arm64" / "steer").unlink()
        self.assertEqual(len(self.check()), 1)
        self.assertIn("darwin-arm64: ", self.check()[0])
        self.assertIn("is missing", self.check()[0])

    def test_one_cgo_target_fails(self):
        os.environ["FAKE_CGO_linux_amd64"] = "1"
        self.addCleanup(os.environ.pop, "FAKE_CGO_linux_amd64")
        self.assertEqual(self.check(), ["linux-amd64: CGO_ENABLED=1, want 0"])

    def test_unsigned_arm64_target_fails(self):
        self.write("darwin-arm64", macho(0x19))
        self.assertEqual(self.check(), ["darwin-arm64: carries no LC_CODE_SIGNATURE, "
                                        "so Apple Silicon will not start it"])

    def test_failing_host_binary_fails(self):
        self.binary("linux-amd64", "#!/bin/sh\necho broken >&2\nexit 3\n")
        self.assertEqual(self.check(), ["linux-amd64: --help exited 3: broken"])

    def test_hanging_host_binary_fails(self):
        self.binary("linux-amd64", "#!/bin/sh\nexec sleep 5\n")
        self.assertEqual(self.check(timeout=0.5),
                         ["linux-amd64: --help did not exit within 0.5s"])

    def test_only_host_binary_runs(self):
        self.binary("darwin-amd64", "#!/bin/sh\nexit 3\n")
        self.assertEqual(self.check(host="linux-amd64"), [])

    def test_unshipped_host_fails(self):
        self.assertEqual(self.check(host="windows-amd64"),
                         ["host windows-amd64 is not a shipped target, so no binary ran"])

    def test_command_line_exit_status(self):
        env = {**os.environ, "GO": str(self.go)}
        args = [sys.executable, str(CHECKER), "--bin-dir", str(self.bin),
                "--host", "linux-amd64", "--version", "1.2.3", "--commit"]
        ok = subprocess.run(args + ["abc1234"], env=env, capture_output=True, text=True)
        self.assertEqual(ok.returncode, 0, ok.stderr)
        bad = subprocess.run(args + ["fffffff"], env=env, capture_output=True, text=True)
        self.assertEqual(bad.returncode, 1)
        self.assertIn("GitCommit stamp is abc1234, want fffffff", bad.stderr)


if __name__ == "__main__":
    unittest.main()
