#!/usr/bin/env python3
"""A throwaway home for a headless agent run.

A run under the operator's real home loads the operator's installed guards, so
anything they block lands in the live audit log as a firing from real work, and
anything the model registers at user scope lands in the operator's harness
files. A Sandbox gives Claude Code, Codex and the steer CLI their own home, with
no hook registered and an empty guard store, and loads the working tree's plugin.

Claude Code takes its login from CLAUDE_CODE_OAUTH_TOKEN (`claude setup-token`)
or ANTHROPIC_API_KEY in the environment; an empty home carries no login of its
own. Claude Code passes neither variable to its tool or hook commands, so a
claude-code reviewer the gate starts there has no login; `--claude` also writes
the login into the sandbox's user settings `env`, which that reviewer reads. Codex takes a copy of the operator's `~/.codex/auth.json`, and agy a copy
of its OAuth token file. Git reads a sandbox global configuration that carries a
fixed author identity, and never the system configuration.

After a run, `leaks()` names every real harness file the run changed and every
row appended to the real audit log or review ledger from a directory inside the
sandbox. It never restores anything: a changed real file is reported, not
overwritten. `sessions()` lists the agent sessions saved inside the sandbox home.

Manual use runs one command inside a fresh sandbox and fails on a leak:

    python3 tests/sandbox.py [--codex] [--agy] [--claude] [--keep] [--no-sessions] \
        -- <command> [args...]

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
REAL_LEDGER_DIR = REAL_HOME / ".steer" / "state" / "commit-review"
AGY_TOKEN = Path(".gemini") / "antigravity-cli" / "antigravity-oauth-token"
# The operator's harness and steer files a run could register into.
REAL_GUARDED = [
    REAL_HOME / ".claude" / "settings.json",
    REAL_HOME / ".claude" / "CLAUDE.md",
    REAL_HOME / ".claude" / "check-gate-approved.txt",
    REAL_HOME / ".steer" / "config.json",
    REAL_HOME / ".steer" / "bin" / "steer",
    REAL_HOME / ".steer" / "state" / "checks" / "adoptions.jsonl",
    REAL_HOME / ".steer" / "hooks" / "steer-manifest.json",
    REAL_HOME / ".steer" / "hooks" / "steer-hooks.py",
    REAL_HOME / ".steer" / "state" / "firing-audits" / "contests.jsonl",
    REAL_HOME / ".codex" / "hooks.json",
    REAL_HOME / ".codex" / "config.toml",
    REAL_HOME / ".codex" / "AGENTS.md",
]
CLAUDE_LOGIN_VARS = ("CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY")
GIT_IDENTITY = {"GIT_AUTHOR_NAME": "Steer Sandbox", "GIT_AUTHOR_EMAIL": "sandbox@steer.invalid",
                "GIT_COMMITTER_NAME": "Steer Sandbox",
                "GIT_COMMITTER_EMAIL": "sandbox@steer.invalid"}
# Where each harness saves a session, relative to the sandbox home.
SESSION_DIRS = {"claude": Path(".claude") / "projects", "codex": Path(".codex") / "sessions",
                "agy": Path(".gemini") / "antigravity-cli" / "conversations"}


def digest(paths):
    return {str(p): hashlib.sha256(p.read_bytes()).hexdigest() if p.exists() else None
            for p in paths}


def sizes(paths):
    return {p: p.stat().st_size for p in paths if p.exists()}


def rows_after(path, offset):
    """The JSON rows appended to path after byte offset; every row when it shrank."""
    if not path.exists():
        return []
    with open(path, "rb") as f:
        if f.seek(0, os.SEEK_END) >= offset:
            f.seek(offset)
        else:
            f.seek(0)
        data = f.read().decode("utf-8", "replace")
    rows = []
    for line in data.splitlines():
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return rows


class Sandbox:
    """A home under root/home, with root as the boundary a leaked row is tested
    against. Put the run's working directory inside root."""

    def __init__(self, root, codex=False, agy=False, audit_log=REAL_AUDIT_LOG,
                 guarded=REAL_GUARDED, ledger_dir=REAL_LEDGER_DIR):
        self.root = Path(os.path.realpath(root))
        self.home = self.root / "home"
        self.codex = codex
        self.audit_log = Path(audit_log)
        self.ledger_dir = Path(ledger_dir)
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
        if agy:
            token = REAL_HOME / AGY_TOKEN
            if not token.exists():
                raise SystemExit("sandbox: %s is missing; log in to agy first" % token)
            (self.home / AGY_TOKEN).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(token, self.home / AGY_TOKEN)
        self.gitconfig = self.home / ".gitconfig"
        self.gitconfig.write_text("[user]\n\tname = %s\n\temail = %s\n"
                                  % (GIT_IDENTITY["GIT_AUTHOR_NAME"],
                                     GIT_IDENTITY["GIT_AUTHOR_EMAIL"]))
        self._before = digest(self.guarded)
        self._log_offset = self.audit_log.stat().st_size if self.audit_log.exists() else 0
        self._ledger_offsets = sizes(self._ledgers())

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
        env["GIT_CONFIG_GLOBAL"] = str(self.gitconfig)
        env["GIT_CONFIG_NOSYSTEM"] = "1"
        env.update(GIT_IDENTITY)
        return env

    def write_claude_login(self):
        """Put the environment's Claude login into the sandbox user settings' env,
        readable only by the user. Returns the settings path."""
        login = {v: os.environ[v] for v in CLAUDE_LOGIN_VARS if os.environ.get(v)}
        path = self.home / ".claude" / "settings.json"
        settings = json.loads(path.read_text()) if path.exists() else {}
        settings.setdefault("env", {}).update(login)
        path.write_text(json.dumps(settings, indent=2))
        path.chmod(0o600)
        return path

    def drop_claude_login(self):
        """Remove the Claude login write_claude_login put into the user settings, or
        the whole file when the run left it unreadable."""
        path = self.home / ".claude" / "settings.json"
        if not path.exists():
            return
        try:
            settings = json.loads(path.read_text())
        except ValueError:
            path.unlink()
            return
        env = settings.get("env", {})
        for v in CLAUDE_LOGIN_VARS:
            env.pop(v, None)
        if not env:
            settings.pop("env", None)
        path.write_text(json.dumps(settings, indent=2))

    @staticmethod
    def claude_args():
        """Arguments that load the working tree's plugin into a sandboxed claude."""
        return ["--plugin-dir", str(REPO)]

    def leaks(self):
        """What the run wrote outside the sandbox, one sentence per leak."""
        after = digest(self.guarded)
        found = ["the run changed the real %s" % p for p, h in after.items()
                 if h != self._before[p]]
        for row in rows_after(self.audit_log, self._log_offset):
            where = row.get("repository") or (row.get("locators") or {}).get("cwd") or ""
            if self._inside(where):
                found.append("the run wrote firing %s (%s) to the real audit log %s"
                             % (row.get("firing_id") or "without an id",
                                row.get("hook_name"), self.audit_log))
        for shard in self._ledgers():
            for row in rows_after(shard, self._ledger_offsets.get(shard, 0)):
                if self._inside(row.get("repo") or ""):
                    found.append("the run wrote a review of %r to the real ledger %s"
                                 % (row.get("subject"), shard))
        return found

    def sessions(self):
        """The session files each harness saved inside the sandbox home."""
        found = {}
        for harness, rel in SESSION_DIRS.items():
            base = self.home / rel
            files = sorted(str(p) for p in base.rglob("*") if p.is_file()) if base.exists() else []
            if files:
                found[harness] = files
        return found

    def _inside(self, path):
        return bool(path) and os.path.realpath(path).startswith(str(self.root) + os.sep)

    def _ledgers(self):
        return sorted(self.ledger_dir.glob("adjudication.*.jsonl"))


def main():
    parser = argparse.ArgumentParser(
        description="Run one command inside a fresh sandbox home and fail on a leak.")
    parser.add_argument("--codex", action="store_true", help="Give the sandbox a Codex login")
    parser.add_argument("--agy", action="store_true", help="Give the sandbox an agy login")
    parser.add_argument("--no-sessions", action="store_true",
                        help="Fail when any harness saved a session inside the sandbox")
    parser.add_argument("--keep", action="store_true", help="Keep the sandbox directory")
    parser.add_argument("--claude", action="store_true",
                        help="Refuse to start without a Claude login in the environment, "
                             "and write it into the sandbox user settings for the reviewer")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("name a command after --")

    root = Path(tempfile.mkdtemp(prefix="steer-sandbox-"))
    try:
        box = Sandbox(root, codex=args.codex, agy=args.agy)
        work = box.root / "work"
        work.mkdir()
        print("sandbox: %s" % box.root, file=sys.stderr)
        env = box.env(claude=args.claude)
        if args.claude:
            box.write_claude_login()
        try:
            code = subprocess.run(command, cwd=work, env=env).returncode
        finally:
            if args.claude:
                box.drop_claude_login()
        leaks = box.leaks()
        for leak in leaks:
            print("sandbox LEAK: %s" % leak, file=sys.stderr)
        sessions = box.sessions()
        for harness, files in sessions.items():
            print("sandbox: %s saved %d session file(s) under %s"
                  % (harness, len(files), box.home / SESSION_DIRS[harness]), file=sys.stderr)
        if args.no_sessions and sessions:
            return 1
        return 1 if leaks else code
    finally:
        if not args.keep:
            shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
