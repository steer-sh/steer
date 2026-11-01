#!/usr/bin/env python3
"""Check the shipped binaries that cli/build.sh wrote, before anything runs or
publishes them.

  python3 scripts/check-binaries.py --bin-dir DIR --version V --commit C

For each shipped target, DIR/<os>-<arch>/steer must exist and `go version -m` must
report that GOOS and GOARCH, CGO_ENABLED=0, and -X stamps of Version=V and
GitCommit=C. Each target in SIGNED_TARGETS must be a Mach-O that carries an
LC_CODE_SIGNATURE whose data starts with the embedded-signature magic, since
Apple Silicon refuses to start an unsigned binary. The binary for this host then runs `--help` and must exit 0 within the
timeout, and runs `--version`, which must exit 0 and print `steer V (C, ...`. GO names the go command (default "go"). Exits 1 naming every failure.
"""

import argparse
import os
import platform
import shlex
import struct
import subprocess
import sys
from pathlib import Path

TARGETS = ("darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64")
VERSION_PKG = "github.com/steer-sh/steer-cli/internal/version"
SIGNED_TARGETS = ("darwin-arm64",)
MACHO_MAGIC_64 = 0xFEEDFACF
LC_CODE_SIGNATURE = 0x1D
CSMAGIC_EMBEDDED_SIGNATURE = 0xFADE0CC0
ARCH_NAMES = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}


def build_settings(meta):
    """Return the `build` key=value settings from `go version -m` output."""
    settings = {}
    for line in meta.splitlines():
        fields = line.strip().split("\t", 1)
        if len(fields) == 2 and fields[0] == "build" and "=" in fields[1]:
            key, value = fields[1].split("=", 1)
            settings[key] = value
    return settings


def ldflag_stamps(ldflags):
    """Return the -X assignments in a -ldflags setting, keyed by symbol."""
    words = shlex.split(ldflags)
    if len(words) == 1 and " " in words[0]:
        words = shlex.split(words[0])
    stamps = {}
    for flag, value in zip(words, words[1:]):
        if flag == "-X" and "=" in value:
            symbol, stamp = value.split("=", 1)
            stamps[symbol] = stamp
    return stamps


def target_errors(target, meta, version, commit):
    """Return the failures of one target's `go version -m` output."""
    goos, goarch = target.split("-")
    settings = build_settings(meta)
    expected = {"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"}
    errors = [f"{key}={settings.get(key, '<absent>')}, want {want}"
              for key, want in expected.items() if settings.get(key) != want]
    if "-ldflags" not in settings:
        return errors + ["-ldflags setting is absent"]
    try:
        stamps = ldflag_stamps(settings["-ldflags"])
    except ValueError as exc:
        return errors + [f"-ldflags does not parse: {exc}"]
    for name, want in (("Version", version), ("GitCommit", commit)):
        got = stamps.get(f"{VERSION_PKG}.{name}")
        if got != want:
            errors.append(f"{name} stamp is {got or '<absent>'}, want {want}")
    return errors


def signature_error(data):
    """Return why a thin 64-bit Mach-O image carries no code signature, or None."""
    if len(data) < 32 or struct.unpack_from("<I", data)[0] != MACHO_MAGIC_64:
        return "is not a little-endian 64-bit Mach-O"
    ncmds = struct.unpack_from("<I", data, 16)[0]
    offset = 32
    for _ in range(ncmds):
        if offset + 8 > len(data):
            return "load commands run past the end of the file"
        cmd, size = struct.unpack_from("<II", data, offset)
        if offset + size > len(data):
            return "load commands run past the end of the file"
        if cmd == LC_CODE_SIGNATURE:
            if size < 16:
                return f"LC_CODE_SIGNATURE has size {size}"
            dataoff, datasize = struct.unpack_from("<II", data, offset + 8)
            if datasize < 4 or dataoff + datasize > len(data):
                return (f"LC_CODE_SIGNATURE points at {datasize} bytes at offset {dataoff}, "
                        f"outside the {len(data)}-byte file")
            if struct.unpack_from(">I", data, dataoff)[0] != CSMAGIC_EMBEDDED_SIGNATURE:
                return "LC_CODE_SIGNATURE points at no embedded signature blob"
            return None
        if size < 8:
            return f"load command at offset {offset} has size {size}"
        offset += size
    return "carries no LC_CODE_SIGNATURE, so Apple Silicon will not start it"


def host_target():
    """Return this host's <os>-<arch> name."""
    arch = ARCH_NAMES.get(platform.machine().lower(), platform.machine().lower())
    return f"{platform.system().lower()}-{arch}"


def run_error(binary, flag, timeout):
    """Run the binary with one flag; return (failure or None, stdout)."""
    try:
        run = subprocess.run([str(binary), flag], capture_output=True, text=True,
                             timeout=timeout)
    except subprocess.TimeoutExpired:
        return f"{flag} did not exit within {timeout}s", ""
    except OSError as exc:
        return f"{flag} did not start: {exc}", ""
    if run.returncode != 0:
        return f"{flag} exited {run.returncode}: {run.stderr.strip()[:200]}", ""
    return None, run.stdout


def smoke_error(binary, timeout, version, commit):
    """Run the binary with --help, then --version; return the failure, or None."""
    error, _ = run_error(binary, "--help", timeout)
    if error:
        return error
    error, out = run_error(binary, "--version", timeout)
    if error:
        return error
    want = f"steer {version} ({commit},"
    if not out.startswith(want):
        return f"--version printed {out.strip()[:200]!r}, want it to start with {want!r}"
    return None


def check(bin_dir, version, commit, go, host, timeout):
    """Return every failure across the shipped targets."""
    failures = []
    for target in TARGETS:
        binary = bin_dir / target / "steer"
        if not binary.is_file():
            failures.append(f"{target}: {binary} is missing")
            continue
        meta = subprocess.run([go, "version", "-m", str(binary)], capture_output=True,
                              text=True)
        if meta.returncode != 0:
            failures.append(f"{target}: go version -m failed: {meta.stderr.strip()}")
            continue
        failures += [f"{target}: {e}" for e in target_errors(target, meta.stdout,
                                                              version, commit)]
        if target in SIGNED_TARGETS:
            error = signature_error(binary.read_bytes())
            if error:
                failures.append(f"{target}: {error}")
    if host not in TARGETS:
        failures.append(f"host {host} is not a shipped target, so no binary ran")
    elif (bin_dir / host / "steer").is_file():
        error = smoke_error((bin_dir / host / "steer").resolve(), timeout, version, commit)
        if error:
            failures.append(f"{host}: {error}")
    return failures


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--bin-dir", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--timeout", type=float, default=30)
    parser.add_argument("--host", default=host_target(),
                        help="the <os>-<arch> target whose binary runs --help")
    args = parser.parse_args()
    failures = check(args.bin_dir, args.version, args.commit,
                     os.environ.get("GO", "go"), args.host, args.timeout)
    for failure in failures:
        print(f"check-binaries: {failure}", file=sys.stderr)
    if failures:
        return 1
    print(f"check-binaries: {len(TARGETS)} targets match; {', '.join(SIGNED_TARGETS)} signed; {args.host} --help and --version ran")
    return 0


if __name__ == "__main__":
    sys.exit(main())
