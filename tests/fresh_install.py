#!/usr/bin/env python3
"""Install the published plugin on a fresh Linux host, as the README says.

Builds tests/docker/Dockerfile, then for each harness runs the commands under
README `## Install` in a new container, places the stable executable with the
installed copy's `scripts/steer bin install`, and requires every version it reads
to equal the manifest version. --snapshot saves the container as a local image, so
a later release can run its update commands against this install: --update-from
runs README `## Update` in each saved image instead. Network checks stay outside
the turn-end oracle.
"""

import argparse
import json
import re
import shlex
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOCKERFILE = ROOT / "tests" / "docker" / "Dockerfile"
HARNESSES = {"claude-code": "For Claude Code:", "codex": "For Codex:"}
# The directory each harness installs the plugin copy under.
CACHES = {
    "claude-code": "~/.claude/plugins/cache/steer-marketplace/steer",
    "codex": "~/.codex/plugins/cache/steer-marketplace/steer",
}
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


def manifest_version():
    return json.loads((ROOT / ".codex-plugin" / "plugin.json").read_text())["version"]


def script(commands, harness, version, update=False):
    """Return the bash program the container runs for one harness.

    With update, the program first copies the marketplace clone into the container's
    own layer: overlayfs refuses to rename a directory an image layer holds (EXDEV).
    """
    lines = ["set -euo pipefail", "mkdir -p ~/.codex"]
    if update:
        m = MARKETPLACES[harness]
        lines.append(f"cp -a {m} {m}.layer && rm -rf {m} && mv {m}.layer {m}")
    for command in commands:
        lines += [f"echo '$ '{shlex.quote(command)}", command]
    lines += [
        f"copy={CACHES[harness]}/{version}",
        'test -x "$copy/scripts/steer" || { echo "no installed copy at $copy" >&2; ls ' + CACHES[harness] + ' >&2 || true; exit 1; }',
        'echo "$ $copy/scripts/steer bin install"',
        '"$copy/scripts/steer" bin install',
        'for bin in "$copy/scripts/steer" ~/.steer/bin/steer; do',
        '  got=$("$bin" --version)',
        '  echo "$bin: $got"',
        f'  case "$got" in "steer {version} "*) ;; *) echo "$bin is not {version}" >&2; exit 1;; esac',
        "done",
    ]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--harness", choices=[*HARNESSES, "both"], default="both")
    parser.add_argument("--image", default="steer-linux-fresh:latest", help="image tag to build and run")
    parser.add_argument("--no-build", action="store_true", help="run the existing image")
    parser.add_argument("--version", default=manifest_version(), help="expected plugin version (default: the manifest's)")
    parser.add_argument("--snapshot", metavar="TAG", help="save each harness's container as TAG-<harness>")
    parser.add_argument("--update-from", metavar="TAG",
                        help="run README ## Update in each saved TAG-<harness> image instead of installing")
    args = parser.parse_args()

    if not args.no_build and not args.update_from:
        subprocess.run(["docker", "build", "-t", args.image, "-f", str(DOCKERFILE), str(DOCKERFILE.parent)], check=True)
    readme = (ROOT / "README.md").read_text()
    failed = []
    for harness in (HARNESSES if args.harness == "both" else [args.harness]):
        print(f"=== {harness}", flush=True)
        name = f"steer-fresh-{harness}"
        subprocess.run(["docker", "rm", "-f", name], capture_output=True)
        image = f"{args.update_from}-{harness}" if args.update_from else args.image
        heading = "Update" if args.update_from else "Install"
        run = subprocess.run(["docker", "run", "--name", name, image, "bash", "-c",
                              script(install_commands(readme, harness, heading), harness, args.version,
                                     update=bool(args.update_from))])
        if run.returncode != 0:
            failed.append(harness)
        elif args.snapshot:
            tag = f"{args.snapshot}-{harness}"
            subprocess.run(["docker", "commit", name, tag], check=True, capture_output=True)
            print(f"saved {tag}")
        subprocess.run(["docker", "rm", "-f", name], capture_output=True)
    verb = f"update from {args.update_from}" if args.update_from else "fresh install"
    if failed:
        print(f"{verb} failed: " + ", ".join(failed), file=sys.stderr)
        return 1
    print(f"{verb} of {args.version} passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
