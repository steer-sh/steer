#!/usr/bin/env python3
"""Check the shipped binaries that cli/build.sh wrote, before anything runs or
publishes them.

  python3 scripts/check-binaries.py --bin-dir DIR --version V --commit C

For each shipped target, DIR/<os>-<arch>/steer must exist and `go version -m` must
report that GOOS and GOARCH, CGO_ENABLED=0, and -X stamps of Version=V and
GitCommit=C. The binary for this host then runs `--help` and must exit 0 within the
timeout. GO names the go command (default "go"). Exits 1 naming every failure.
"""

import argparse
import os
import platform
import shlex
import subprocess
import sys
from pathlib import Path

TARGETS = ("darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64")
VERSION_PKG = "github.com/steer-sh/steer-cli/internal/version"
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


def host_target():
    """Return this host's <os>-<arch> name."""
    arch = ARCH_NAMES.get(platform.machine().lower(), platform.machine().lower())
    return f"{platform.system().lower()}-{arch}"


def smoke_error(binary, timeout):
    """Run the binary with --help; return the failure, or None."""
    try:
        run = subprocess.run([str(binary), "--help"], capture_output=True, text=True,
                             timeout=timeout)
    except subprocess.TimeoutExpired:
        return f"--help did not exit within {timeout}s"
    except OSError as exc:
        return f"--help did not start: {exc}"
    if run.returncode != 0:
        return f"--help exited {run.returncode}: {run.stderr.strip()[:200]}"
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
    if host not in TARGETS:
        failures.append(f"host {host} is not a shipped target, so no binary ran")
    elif (bin_dir / host / "steer").is_file():
        error = smoke_error((bin_dir / host / "steer").resolve(), timeout)
        if error:
            failures.append(f"{host}: {error}")
    return failures


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--bin-dir", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--timeout", type=float, default=30)
    args = parser.parse_args()
    failures = check(args.bin_dir, args.version, args.commit,
                     os.environ.get("GO", "go"), host_target(), args.timeout)
    for failure in failures:
        print(f"check-binaries: {failure}", file=sys.stderr)
    if failures:
        return 1
    print(f"check-binaries: {len(TARGETS)} targets match; {host_target()} --help ran")
    return 0


if __name__ == "__main__":
    sys.exit(main())
