#!/usr/bin/env python3
"""Check the Claude Code mod that hooks/hooks.json names under modules.

Claude Code's own validator must accept the mod and list no tool event, classic event
or wildcard among the events it handles, and `claude plugin test` must pass. Neither
command starts a session. Without a `claude` that has both commands the check prints
a skip line and exits 0.
"""

import json
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def handled_events(notes):
    """The event names in a validator `hooks:` note, without their filters."""
    events = []
    for note in notes:
        _, sep, listed = note.partition(" hooks: ")
        if not sep:
            continue
        listed = listed.split(" (default matcher key", 1)[0]
        depth, start = 0, 0
        for i, ch in enumerate(listed + ","):
            if ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
            elif ch == "," and depth == 0:
                name = listed[start:i].strip().split("{", 1)[0]
                if name:
                    events.append(name)
                start = i + 1
    return events


def refused(event):
    return event == "*" or event.startswith(("tool.", "classic."))


def main():
    claude = shutil.which("claude")
    if claude is None:
        print("check-mod: skipped: claude was not found, so the mod was not validated or tested")
        return 0
    for command in ("validate", "test"):
        if subprocess.run([claude, "plugin", command, "--help"], capture_output=True).returncode != 0:
            print(f"check-mod: skipped: this claude has no `plugin {command}` command")
            return 0

    run = subprocess.run([claude, "plugin", "validate", str(ROOT), "--json"], capture_output=True, text=True)
    try:
        report = json.loads(run.stdout)
    except ValueError:
        print(f"check-mod: the validator printed no report:\n{run.stdout}{run.stderr}", file=sys.stderr)
        return 1
    hooks_file = str(ROOT / "hooks" / "hooks.json")
    mod_dir = str(ROOT / "hooks" / "mod") + "/"
    failures = []
    events = []
    for entry in report.get("contents", []):
        name = entry.get("file", "")
        if name != hooks_file and not name.startswith(mod_dir):
            continue
        for problem in (*entry.get("errors", []), *entry.get("warnings", [])):
            failures.append(f"{name}: {problem.get('path')}: {problem.get('message')}")
        if entry.get("gatingHooks"):
            failures.append(f"{name}: gating hooks {entry['gatingHooks']}")
        if name == hooks_file:
            events = handled_events(entry.get("notes", []))
    if not events:
        failures.append("the validator listed no event for the mod")
    failures += [f"the mod handles {event}, which Steer reports as an exposure" for event in events if refused(event)]
    if failures:
        print("check-mod: the mod failed validation:\n  " + "\n  ".join(failures), file=sys.stderr)
        return 1

    test = subprocess.run([claude, "plugin", "test", str(ROOT)], capture_output=True, text=True)
    if test.returncode != 0:
        print(f"check-mod: the mod's tests failed:\n{test.stdout}{test.stderr}", file=sys.stderr)
        return 1
    print(f"check-mod: the mod handles {', '.join(events)}; its tests pass")
    return 0


if __name__ == "__main__":
    sys.exit(main())
