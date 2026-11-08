#!/usr/bin/env python3
"""Install the published plugin on a fresh Linux host, as the README says.

Builds tests/docker/Dockerfile, then for each harness runs the commands under
README `## Install` in a new container, places the stable executable with the
installed copy's `scripts/steer bin install`, and requires every version it reads
to equal the manifest version. --snapshot saves the container as a local image, so
a later release can run its update commands against this install: --update-from
arms a global check runner with the saved stable executable, runs README
`## Update` in each saved image, then the new copy's SessionStart command
`scripts/steer report cadence`, which must report the hooks update. --local installs this
checkout's HEAD with a Linux build in place of the published bundle, and
--uninstall then arms Steer's global gate, check runner and status line and removes
them: `readme` runs
README `## Uninstall`, and `plugin-first` removes the plugin, requires the
removed-plugin notice, and runs `uninstall --harness`. Network checks stay outside
the turn-end oracle.
"""

import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOCKERFILE = ROOT / "tests" / "docker" / "Dockerfile"
HARNESSES = {"claude-code": "For Claude Code:", "codex": "For Codex:"}
# The directory each harness installs the plugin copy under.
CACHES = {
    "claude-code": "~/.claude/plugins/cache/steer-marketplace/steer",
    "codex": "~/.codex/plugins/cache/steer-marketplace/steer",
}
# The CLI each harness runs, which names its lines in a block both harnesses share.
CLIS = {"claude-code": "claude", "codex": "codex"}
# The configuration file each harness keeps its global hooks in.
HOOK_FILES = {"claude-code": "~/.claude/settings.json", "codex": "~/.codex/hooks.json"}
# Where a --local run copies the staged checkout inside the container.
LOCAL_SOURCE = "/home/runner/src"
STEER = "~/.steer/bin/steer"
# The marketplace clone each harness renames during an update.
MARKETPLACES = {
    "claude-code": "~/.claude/plugins/marketplaces/steer-marketplace",
    "codex": "~/.codex/.tmp/marketplaces/steer-marketplace",
}


def install_commands(readme, harness, heading="Install"):
    """Return the commands in the bash block after the harness's lead-in under ## <heading>."""
    section = re.search(rf"^## {heading}\n(.*?)^## ", readme, re.S | re.M)
    if not section:
        raise ValueError(f"README has no ## {heading} section")
    lead = re.escape(HARNESSES[harness])
    block = re.search(lead + r"\s*```bash\n(.*?)```", section.group(1), re.S)
    if not block:
        raise ValueError(f"README ## {heading} has no bash block after {HARNESSES[harness]!r}")
    return [line.strip() for line in block.group(1).splitlines() if line.strip()]


def uninstall_commands(readme, harness):
    """Return the commands in README ## Uninstall that this harness runs.

    Both harnesses share the section's blocks, so a line that starts with the other
    harness's CLI is left out.
    """
    section = re.search(r"^## Uninstall\n(.*?)^## ", readme, re.S | re.M)
    if not section:
        raise ValueError("README has no ## Uninstall section")
    others = {cli for h, cli in CLIS.items() if h != harness}
    out = []
    for block in re.findall(r"```bash\n(.*?)```", section.group(1), re.S):
        for line in block.splitlines():
            line = line.strip()
            if line and line.split()[0] not in others:
                out.append(line)
    if not any(line.split()[0] == CLIS[harness] for line in out):
        raise ValueError(f"README ## Uninstall names no {CLIS[harness]} command")
    return out


def local_commands(commands):
    """Point each marketplace add at the staged checkout instead of the published repository."""
    return [re.sub(r"(plugin marketplace add )\S+", rf"\g<1>{LOCAL_SOURCE}", c) for c in commands]


def stage_local(dest):
    """Write HEAD and a Linux build of it into dest, its marketplaces naming dest itself.

    Returns HEAD's manifest version, which the build carries.
    """
    archive = subprocess.run(["git", "-C", str(ROOT), "archive", "HEAD"], check=True, capture_output=True).stdout
    subprocess.run(["tar", "-x", "-C", str(dest)], input=archive, check=True)
    version = manifest_version(dest)
    # The staged copy builds itself, so an uncommitted change never reaches the binary.
    env = {**os.environ, "BIN_DIR": str(dest / "bin"), "VERSION": version}
    subprocess.run([str(dest / "cli" / "build.sh")], cwd=dest / "cli", env=env, check=True)
    for rel in (".claude-plugin/marketplace.json", ".agents/plugins/marketplace.json"):
        path = dest / rel
        doc = json.loads(path.read_text())
        for plugin in doc["plugins"]:
            plugin["source"] = "./"
        path.write_text(json.dumps(doc, indent=2) + "\n")
    for args in (["init", "-q"], ["add", "-A"],
                 ["-c", "user.name=fresh", "-c", "user.email=fresh@steer.invalid", "commit", "-qm", "local"]):
        subprocess.run(["git", "-C", str(dest), *args], check=True)
    return version


def uninstall_lines(harness, commands, mode):
    """Return the bash lines that arm Steer's global channels and remove them.

    commands are the harness's README ## Uninstall lines. readme runs them in order;
    plugin-first removes the plugin, requires the notice, then uninstalls the harness.
    """
    lines = [f"{STEER} commit-gate install --harness {harness} --scope global",
             f"{STEER} check install --harness {harness} --scope global"]
    if harness == "claude-code":
        lines.append(f"{STEER} statusline install --scope global")
    plugin = [c for c in commands if c.split()[0] == CLIS[harness]]
    if mode == "plugin-first":
        steps = plugin + [
            f"row=$({STEER} statusline)", 'echo "$row"',
            f'case "$row" in *"plugin removed"*"uninstall --harness {harness} --apply"*) ;; '
            '*) echo "no removed-plugin notice" >&2; exit 1;; esac',
            f"{STEER} uninstall --harness {harness} --apply",
            f'case "$({STEER} statusline)" in *"plugin removed"*) '
            'echo "the notice outlived the uninstall" >&2; exit 1;; esac',
        ]
    else:
        steps = commands
    for step in steps:
        lines += [f"echo '$ '{shlex.quote(step)}", step]
    hooks = HOOK_FILES[harness]
    lines += [
        f"if grep -nE '\\.steer/(bin/steer|hooks/)' {hooks}; then echo 'Steer left a registration in {hooks}' >&2; exit 1; fi",
        f'{STEER} uninstall | grep -q "no global registration to remove" || '
        f'{{ {STEER} uninstall; echo "a global registration is left" >&2; exit 1; }}',
        "if grep -q '\"steer@' ~/.claude/plugins/installed_plugins.json 2>/dev/null; then "
        "echo 'Claude Code still records the Steer plugin' >&2; exit 1; fi",
        "if grep -qE '^\\[plugins\\..steer@' ~/.codex/config.toml 2>/dev/null; then "
        "echo 'Codex still records the Steer plugin' >&2; exit 1; fi",
        "echo '--- plugin record after removal'",
        "cat ~/.claude/plugins/installed_plugins.json 2>/dev/null || true",
        "grep -n -A2 '^\\[plugins' ~/.codex/config.toml 2>/dev/null || true",
    ]
    return lines


def manifest_version(root=ROOT):
    return json.loads((root / ".codex-plugin" / "plugin.json").read_text())["version"]


def script(commands, harness, version, update=False, local=False, removal=None):
    """Return the bash program the container runs for one harness.

    With update, the program first copies the marketplace clone into the container's
    own layer: overlayfs refuses to rename a directory an image layer holds (EXDEV).
    It arms a global registration that calls the stable executable, which the
    SessionStart update requires, and runs that update in place of bin install.
    With local, it copies the mounted checkout to LOCAL_SOURCE first. removal is the
    output of uninstall_lines, run after the install checks.
    """
    lines = ["set -euo pipefail", "mkdir -p ~/.codex"]
    if local:
        lines.append(f"cp -a /src-ro {LOCAL_SOURCE}")
    if update:
        m = MARKETPLACES[harness]
        lines.append(f"cp -a {m} {m}.layer && rm -rf {m} && mv {m}.layer {m}")
        lines.append(f"{STEER} check install --harness {harness} --scope global")
    for command in commands:
        lines += [f"echo '$ '{shlex.quote(command)}", command]
    lines += [
        f"copy={CACHES[harness]}/{version}",
        *([f'[ -d "$copy" ] || copy={CACHES[harness]}/local'] if local else []),
        'test -x "$copy/scripts/steer" || { echo "no installed copy at $copy" >&2; ls ' + CACHES[harness] + ' >&2 || true; exit 1; }',
        *(['echo "$ $copy/scripts/steer report cadence"',
           'started=$(cd /tmp && "$copy/scripts/steer" report cadence)',
           'echo "$started"',
           'case "$started" in *"hooks updated"*) ;; *) echo "the session start did not update the hooks" >&2; exit 1;; esac']
          if update else
          ['echo "$ $copy/scripts/steer bin install"',
           '"$copy/scripts/steer" bin install']),
        'for bin in "$copy/scripts/steer" ~/.steer/bin/steer; do',
        '  got=$("$bin" --version)',
        '  echo "$bin: $got"',
        f'  case "$got" in "steer {version} "*) ;; *) echo "$bin is not {version}" >&2; exit 1;; esac',
        "done",
        *(removal or []),
    ]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--harness", choices=[*HARNESSES, "both"], default="both")
    parser.add_argument("--image", default="steer-linux-fresh:latest", help="image tag to build and run")
    parser.add_argument("--no-build", action="store_true", help="run the existing image")
    parser.add_argument("--version", help="expected plugin version (default: the manifest's)")
    parser.add_argument("--snapshot", metavar="TAG", help="save each harness's container as TAG-<harness>")
    parser.add_argument("--update-from", metavar="TAG",
                        help="run README ## Update in each saved TAG-<harness> image instead of installing")
    parser.add_argument("--local", action="store_true", help="install this checkout's HEAD instead of the published bundle")
    parser.add_argument("--uninstall", choices=["readme", "plugin-first"],
                        help="after the install, arm Steer's global channels and remove them")
    args = parser.parse_args()
    if args.local and args.update_from:
        parser.error("--local installs; it cannot run with --update-from")
    if args.uninstall and args.snapshot:
        parser.error("--snapshot saves an install for --update-from; --uninstall removes it first")
    stage = None
    readme = (ROOT / "README.md").read_text()
    if args.local:
        stage = Path(tempfile.mkdtemp(prefix="steer-local-"))
        # mkdtemp makes the directory 0700; the container's runner user must read it.
        stage.chmod(0o755)
        # A local run reads HEAD's own README and manifest, never the working tree's.
        built = stage_local(stage)
        args.version = args.version or built
        readme = (stage / "README.md").read_text()
    args.version = args.version or manifest_version()
    try:
        return run_harnesses(args, readme, stage)
    finally:
        if stage:
            shutil.rmtree(stage, ignore_errors=True)


def run_harnesses(args, readme, stage):
    """Run the install, and any update or uninstall, once per selected harness."""
    if not args.no_build and not args.update_from:
        subprocess.run(["docker", "build", "-t", args.image, "-f", str(DOCKERFILE), str(DOCKERFILE.parent)], check=True)
    failed = []
    for harness in (HARNESSES if args.harness == "both" else [args.harness]):
        print(f"=== {harness}", flush=True)
        name = f"steer-fresh-{harness}"
        subprocess.run(["docker", "rm", "-f", name], capture_output=True)
        image = f"{args.update_from}-{harness}" if args.update_from else args.image
        heading = "Update" if args.update_from else "Install"
        commands = install_commands(readme, harness, heading)
        if args.local:
            commands = local_commands(commands)
        removal = uninstall_lines(harness, uninstall_commands(readme, harness), args.uninstall) if args.uninstall else None
        mount = ["-v", f"{stage}:/src-ro:ro"] if stage else []
        run = subprocess.run(["docker", "run", "--name", name, *mount, image, "bash", "-c",
                              script(commands, harness, args.version, update=bool(args.update_from),
                                     local=args.local, removal=removal)])
        if run.returncode != 0:
            failed.append(harness)
        elif args.snapshot:
            tag = f"{args.snapshot}-{harness}"
            subprocess.run(["docker", "commit", name, tag], check=True, capture_output=True)
            print(f"saved {tag}")
        subprocess.run(["docker", "rm", "-f", name], capture_output=True)
    verb = f"update from {args.update_from}" if args.update_from else "fresh install"
    if args.uninstall:
        verb += f" and {args.uninstall} uninstall"
    if failed:
        print(f"{verb} failed: " + ", ".join(failed), file=sys.stderr)
        return 1
    print(f"{verb} of {args.version} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
