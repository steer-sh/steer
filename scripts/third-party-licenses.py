#!/usr/bin/env python3
"""Check THIRD_PARTY_LICENSES against the modules linked into the shipped binaries.

  python3 scripts/third-party-licenses.py --bin-dir DIR [--write]

Reads the `dep` lines of `go version -m` for every shipped target under DIR, and
renders the license, NOTICE and PATENTS files of each linked module, at any depth
outside testdata, and of the Go standard library's root. A local directory
replacement is refused. The standard library section also reproduces each
// comment block with a permission notice in the standard library sources that
`go list -deps` compiles into the main package for a shipped target. A module whose non-test Go sources cite a unicode.org/Public
data file is named in a closing section that reproduces licenses/Unicode-3.0.txt.
Without --write, exits 1 when THIRD_PARTY_LICENSES at the
repository root differs from that rendering, or when a linked module has no
LICENSE, LICENCE or COPYING file. With --write, replaces the file. GO names the go command (default "go").
"""

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / "THIRD_PARTY_LICENSES"
TARGETS = ("darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64")
NOTICE_PREFIXES = ("licence", "license", "copying", "notice", "patents")
LICENSE_PREFIXES = ("licence", "license", "copying")
UNICODE_LICENSE = ROOT / "licenses" / "Unicode-3.0.txt"
UNICODE_DATA = "unicode.org/Public"
PERMISSION_PHRASES = ("Permission is hereby granted", "Permission to use, copy, modify",
                      "Redistribution and use")
STD_FILES = ('{{if .Standard}}{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}\n{{end}}'
             '{{range .SFiles}}{{$d}}/{{.}}\n{{end}}{{end}}')
RULE = "=" * 80
HEADER = """\
Third-party software in the steer release binaries

The steer executable links the Go standard library and the Go modules below.
Each section reproduces the license, notice and patent files that the module
ships, as the module's license requires for binary redistribution.
"""


def linked_modules(meta):
    """Return the (path, version) pairs a binary links, after replacements."""
    modules = []
    for line in meta.splitlines():
        fields = line.lstrip("\t").split("\t")
        if fields[0] == "dep" and len(fields) >= 3:
            modules.append((fields[1], fields[2]))
        elif fields[0] == "=>" and modules and len(fields) >= 2:
            modules[-1] = (fields[1], fields[2] if len(fields) >= 3 else "")
    return modules


def notice_files(directory, nested=True):
    """Return the license, notice and patent files of a directory, relative to it.

    With nested, files in subdirectories count too, except under testdata."""
    root = Path(directory)
    found = root.rglob("*") if nested else root.iterdir()
    return sorted(p.relative_to(root) for p in found
                  if p.is_file() and p.name.lower().startswith(NOTICE_PREFIXES)
                  and "testdata" not in p.relative_to(root).parts)


def cites_unicode_data(directory):
    """Report whether a module's non-test Go sources cite a Unicode data file."""
    root = Path(directory)
    return any(UNICODE_DATA in p.read_text(errors="replace")
               for p in root.rglob("*.go")
               if not p.name.endswith("_test.go")
               and "testdata" not in p.relative_to(root).parts)


def unicode_section(modules):
    """Return the section that names the Unicode-data modules and the Unicode license."""
    parts = [RULE, "Unicode data", RULE, "",
             "These modules embed tables generated from Unicode Character Database",
             "files, which the Unicode license below covers:", ""]
    parts += [f"  {m}" for m in modules]
    parts += ["", f"--- {UNICODE_LICENSE.name} ---", "",
              UNICODE_LICENSE.read_text().rstrip(), ""]
    return "\n".join(parts) + "\n"


def notice_blocks(text):
    """Return the // comment blocks of a source text that carry a permission notice."""
    blocks, lines = [], []
    for line in text.splitlines() + [""]:
        if line.lstrip().startswith("//"):
            lines.append(line.lstrip()[2:].strip())
            continue
        block = "\n".join(lines).strip()
        if any(phrase in block for phrase in PERMISSION_PHRASES):
            blocks.append(block)
        lines = []
    return blocks


def stdlib_notices(go, goroot):
    """Return {notice: [relative files]} for compiled std sources, and failures."""
    files = set()
    for target in TARGETS:
        goos, goarch = target.split("-")
        env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
        run = subprocess.run([go, "list", "-deps", "-f", STD_FILES, "."], env=env,
                             capture_output=True, text=True, cwd=ROOT / "cli")
        if run.returncode != 0:
            raise RuntimeError(f"go list for {target}: {run.stderr.strip()}")
        files.update(line for line in run.stdout.splitlines() if line)
    notices, failures = {}, []
    for name in sorted(files):
        text = Path(name).read_text(errors="replace")
        if not any(phrase in text for phrase in PERMISSION_PHRASES):
            continue
        relative = Path(name).relative_to(goroot).as_posix()
        blocks = notice_blocks(text)
        if not blocks:
            failures.append(f"{relative} has a permission notice outside // comments")
        for block in blocks:
            notices.setdefault(block, []).append(relative)
    return notices, failures


def section(title, directory, files):
    """Return one rendered section: a title, then each file under its relative path."""
    parts = [RULE, title, RULE]
    for path in files:
        text = (Path(directory) / path).read_text().rstrip()
        parts += ["", f"--- {path.as_posix()} ---", "", text, ""]
    return "\n".join(parts) + "\n"


def module_dir(go, path, version):
    """Return the module cache directory of path@version."""
    run = subprocess.run([go, "mod", "download", "-json", f"{path}@{version}"],
                         capture_output=True, text=True, cwd=ROOT / "cli")
    if run.returncode != 0:
        raise RuntimeError(f"go mod download {path}@{version}: {run.stderr.strip()}")
    return json.loads(run.stdout)["Dir"]


def render(bin_dir, go):
    """Return the rendered file and the failures found while building it."""
    failures, modules, unicode_modules = [], set(), []
    for target in TARGETS:
        binary = bin_dir / target / "steer"
        meta = subprocess.run([go, "version", "-m", str(binary)], capture_output=True,
                              text=True)
        if meta.returncode != 0:
            failures.append(f"{target}: go version -m failed: {meta.stderr.strip()}")
            continue
        modules.update(linked_modules(meta.stdout))
    # From cli, so toolchain selection matches the build that cli/build.sh ran.
    goroot = subprocess.run([go, "env", "GOROOT"], capture_output=True, text=True,
                            check=True, cwd=ROOT / "cli").stdout.strip()
    stdlib = section("Go standard library and runtime", goroot,
                     notice_files(goroot, nested=False))
    notices, notice_failures = stdlib_notices(go, goroot)
    failures += notice_failures
    for block, names in notices.items():
        stdlib += f"\n--- notice in {', '.join(names)} ---\n\n{block}\n"
    sections = [stdlib]
    for path, version in sorted(modules):
        if version in ("", "(devel)") or path.startswith((".", "/")):
            failures.append(f"{path} is a local replacement, which this script refuses")
            continue
        directory = module_dir(go, path, version)
        files = notice_files(directory)
        if not any(f.name.lower().startswith(LICENSE_PREFIXES) for f in files):
            failures.append(f"{path}@{version} has no license file")
        sections.append(section(f"{path} {version}", directory, files))
        if cites_unicode_data(directory):
            unicode_modules.append(f"{path} {version}")
    if unicode_modules:
        sections.append(unicode_section(unicode_modules))
    return HEADER + "\n" + "\n".join(sections), failures


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--bin-dir", required=True, type=Path)
    parser.add_argument("--write", action="store_true")
    args = parser.parse_args()
    try:
        text, failures = render(args.bin_dir, os.environ.get("GO", "go"))
    except RuntimeError as exc:
        failures, text = [str(exc)], None
    if text is not None and not failures:
        if args.write:
            OUTPUT.write_text(text)
        elif not OUTPUT.is_file() or OUTPUT.read_text() != text:
            failures.append(f"{OUTPUT.name} does not match the linked modules; run "
                            "python3 scripts/third-party-licenses.py --bin-dir DIR --write")
    for failure in failures:
        print(f"third-party-licenses: {failure}", file=sys.stderr)
    if failures:
        return 1
    print(f"third-party-licenses: {OUTPUT.name} matches the linked modules")
    return 0


if __name__ == "__main__":
    sys.exit(main())
