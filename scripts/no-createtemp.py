#!/usr/bin/env python3
"""Fail when non-test Go under cli/ calls os.CreateTemp outside the allowed files.

A file that replaces another goes through atomicfile.WriteFile or atomicfile.Copy.
Run from the repository root; prints each offending call and exits 1.
"""

import os
import re
import sys

ROOT = "cli"

# Repository-relative paths that may call os.CreateTemp, with the reason.
ALLOWED_FILES = {
    # The helper itself.
    "cli/internal/atomicfile/atomicfile.go",
    # writeHostTemp feeds os.Link, which claims the host file only when it is
    # absent; a rename would overwrite a concurrent claim.
    "cli/internal/gate/host.go",
    # --to-file opens a new uniquely named dump and keeps it open as stdout; it
    # replaces no file.
    "cli/cmd/tofile.go",
    # A new uniquely named settings file handed to the reviewer CLI; it replaces
    # no file.
    "cli/internal/gate/reviewers/claudecode.go",
}

CALL = re.compile(r"\bos\.CreateTemp\s*\(")


def code_only(src):
    """Return src with comments and string and rune literals blanked, lines kept."""
    out = []
    i, n = 0, len(src)
    while i < n:
        c = src[i]
        if src.startswith("//", i):
            j = src.find("\n", i)
            j = n if j < 0 else j
            i = j
        elif src.startswith("/*", i):
            j = src.find("*/", i + 2)
            j = n if j < 0 else j + 2
            out.append("\n" * src.count("\n", i, j))
            i = j
        elif c == "`":
            j = src.find("`", i + 1)
            j = n if j < 0 else j + 1
            out.append("\n" * src.count("\n", i, j))
            i = j
        elif c in "\"'":
            j = i + 1
            while j < n and src[j] != c and src[j] != "\n":
                j += 2 if src[j] == "\\" else 1
            i = min(j + 1, n)
            out.append(" ")
        else:
            out.append(c)
            i += 1
    return "".join(out)


def main():
    found = []
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = sorted(d for d in dirnames if d != "testdata" and not d.startswith("."))
        for name in sorted(filenames):
            if not name.endswith(".go") or name.endswith("_test.go"):
                continue
            path = os.path.join(dirpath, name).replace(os.sep, "/")
            if path in ALLOWED_FILES:
                continue
            with open(path, encoding="utf-8") as f:
                code = code_only(f.read())
            for lineno, line in enumerate(code.split("\n"), 1):
                if CALL.search(line):
                    found.append(f"{path}:{lineno}")
    if found:
        for where in found:
            print(f"{where}: os.CreateTemp outside atomicfile; replace a file with "
                  "atomicfile.WriteFile or atomicfile.Copy")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
