#!/usr/bin/env python3
"""A throwaway home for a headless agent run.

A run under the operator's real home loads the operator's installed guards, so
anything they block lands in the live audit log as a firing from real work, and
anything the model registers at user scope lands in the operator's harness
files. A Sandbox gives Claude Code, Codex and the steer CLI their own home, with
no hook registered and an empty guard store, and loads the working tree's plugin.

Claude Code takes its login from CLAUDE_CODE_OAUTH_TOKEN (`claude setup-token`)
or ANTHROPIC_API_KEY in the environment; an empty home carries no login of its
own. Codex takes a copy of the operator's `~/.codex/auth.json`.

After a run, `leaks()` names every real harness file the run changed and every
row appended to the real audit log from a directory inside the sandbox. It never
restores anything: a changed real file is reported, not overwritten.

Manual use runs one command inside a fresh sandbox and fails on a leak:

    python3 tests/sandbox.py [--codex] [--claude] [--keep] -- <command> [args...]

The command's working directory is the sandbox's `work/` and its HOME is the
sandbox home, so `steer register` and `codex exec` inside it reach only the
sandbox's guard store and registrations.
"""

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
REAL_HOME = Path.home()
REAL_AUDIT_LOG = REAL_HOME / ".steer" / "hooks" / "steer-audit.jsonl"
# The operator's harness and steer files a run could register into.
REAL_GUARDED = [
    REAL_HOME / ".claude" / "settings.json",
    REAL_HOME / ".claude" / "CLAUDE.md",
    REAL_HOME / ".claude" / "check-gate-approved.txt",
    REAL_HOME / ".steer" / "state" / "checks" / "adoptions.jsonl",
    REAL_HOME / ".steer" / "hooks" / "steer-manifest.json",
    REAL_HOME / ".steer" / "hooks" / "steer-hooks.py",
    REAL_HOME / ".codex" / "hooks.json",
    REAL_HOME / ".codex" / "config.toml",
    REAL_HOME / ".codex" / "AGENTS.md",
]
CLAUDE_LOGIN_VARS = ("CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY")


def digest(paths):
    return {str(p): hashlib.sha256(p.read_bytes()).hexdigest() if p.exists() else None
            for p in paths}


class Sandbox:
    """A home under root/home, with root as the boundary a leaked row is tested
    against. Put the run's working directory inside root."""

    def __init__(self, root, codex=False, audit_log=REAL_AUDIT_LOG, guarded=REAL_GUARDED):
        self.root = Path(os.path.realpath(root))
        self.home = self.root / "home"
        self.codex = codex
        self.audit_log = Path(audit_log)
        self.guarded = list(guarded)
        (self.home / ".claude").mkdir(parents=True, exist_ok=True)
        (self.home / ".steer").mkdir(parents=True, exist_ok=True)
        if codex:
            codex_home = self.home / ".codex"
            codex_home.mkdir(parents=True, exist_ok=True)
            auth = REAL_HOME / ".codex" / "auth.json"
            if not auth.exists():
                raise SystemExit("sandbox: %s is missing; log in to Codex first" % auth)
            shutil.copy2(auth, codex_home / "auth.json")
        self._before = digest(self.guarded)
        self._log_offset = self.audit_log.stat().st_size if self.audit_log.exists() else 0

    def env(self, base=None, claude=True):
        """The environment for a process that must stay inside the sandbox.
        claude=True refuses to build one that holds no Claude login."""
        if claude and not any(os.environ.get(v) for v in CLAUDE_LOGIN_VARS):
            raise SystemExit("sandbox: set CLAUDE_CODE_OAUTH_TOKEN (from `claude setup-token`) "
                             "or ANTHROPIC_API_KEY; the sandbox home holds no Claude login")
        env = dict(os.environ if base is None else base)
        env.pop("CLAUDECODE", None)  # a nested session is refused
        env["HOME"] = str(self.home)
        env["CODEX_HOME"] = str(self.home / ".codex")
        env.pop("CLAUDE_CONFIG_DIR", None)
        return env

    @staticmethod
    def claude_args():
        """Arguments that load the working tree's plugin into a sandboxed claude."""
        return ["--plugin-dir", str(REPO)]

    def leaks(self):
        """What the run wrote outside the sandbox, one sentence per leak."""
        after = digest(self.guarded)
        found = ["the run changed the real %s" % p for p, h in after.items()
                 if h != self._before[p]]
        for row in self._appended_rows():
            where = row.get("repository") or (row.get("locators") or {}).get("cwd") or ""
            if where and os.path.realpath(where).startswith(str(self.root) + os.sep):
                found.append("the run wrote firing %s (%s) to the real audit log %s"
                             % (row.get("firing_id") or "without an id",
                                row.get("hook_name"), self.audit_log))
        return found

    def _appended_rows(self):
        if not self.audit_log.exists():
            return []
        with open(self.audit_log, "rb") as f:
            if f.seek(0, os.SEEK_END) >= self._log_offset:
                f.seek(self._log_offset)
            else:
                f.seek(0)  # the log shrank, so every row is read
            data = f.read().decode("utf-8", "replace")
        rows = []
        for line in data.splitlines():
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError:
                continue
        return rows


def main():
    parser = argparse.ArgumentParser(
        description="Run one command inside a fresh sandbox home and fail on a leak.")
    parser.add_argument("--codex", action="store_true", help="Give the sandbox a Codex login")
    parser.add_argument("--keep", action="store_true", help="Keep the sandbox directory")
    parser.add_argument("--claude", action="store_true",
                        help="Refuse to start without a Claude login in the environment")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("name a command after --")

    root = Path(tempfile.mkdtemp(prefix="steer-sandbox-"))
    try:
        box = Sandbox(root, codex=args.codex)
        work = box.root / "work"
        work.mkdir()
        print("sandbox: %s" % box.root, file=sys.stderr)
        code = subprocess.run(command, cwd=work, env=box.env(claude=args.claude)).returncode
        leaks = box.leaks()
        for leak in leaks:
            print("sandbox LEAK: %s" % leak, file=sys.stderr)
        return 1 if leaks else code
    finally:
        if not args.keep:
            shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
