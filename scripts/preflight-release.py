#!/usr/bin/env python3
"""Check that the manifest repository supplies a publicly installable plugin.

Run before publication with --ref <release branch or tag> when testing a ref other
than the default branch. Network checks stay outside the turn-end oracle.
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parent.parent
MARKETS = (".claude-plugin/marketplace.json", ".agents/plugins/marketplace.json")
PLUGINS = (".claude-plugin/plugin.json", ".codex-plugin/plugin.json")
SKILL_FILES = ("skills/steer/SKILL.md", "skills/steer/prompts/routing.md",
               "skills/steer-report/SKILL.md", "skills/steer-report/audit.md",
               "skills/steer-setup/SKILL.md", *("skills/steer/prompts/" + name for name in (
                   "simple-regex.md", "python-security.md", "python-style.md", "terraform-hcl.md",
                   "generic-html.md", "shell-lint.md", "react-jsx.md", "security-layer2.md")))
EXECUTABLES = ("scripts/steer", *(f"bin/{osname}-{arch}/steer"
               for osname in ("darwin", "linux") for arch in ("amd64", "arm64")))


def contained_file(root, name):
    path = root / name
    return path.is_file() and path.resolve().is_relative_to(root.resolve())


def artifact_digests(root, names=EXECUTABLES):
    digests = {}
    for name in names:
        if not contained_file(root, name):
            raise ValueError(f"candidate artifact is missing or outside the bundle: {name}")
        digest = hashlib.sha256()
        with (root / name).open("rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
        digests[name] = digest.hexdigest()
    return digests


def identity(root):
    entries = []
    for name in MARKETS:
        doc = json.loads((root / name).read_text())
        plugins = [p for p in doc["plugins"] if p.get("name") == "steer"]
        if len(plugins) != 1:
            raise ValueError(f"{name}: expected exactly one steer entry")
        entries.append((doc["name"], plugins[0]))
    if entries[0] != entries[1]:
        raise ValueError("marketplace manifests disagree")
    entry = entries[0][1]
    if not re.fullmatch(r"https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", entry["repository"]):
        raise ValueError("repository must be an HTTPS GitHub repository URL")
    for name in PLUGINS:
        plugin = json.loads((root / name).read_text())
        expected = {k: v for k, v in entry.items() if k not in ("source", "strict")}
        if {k: v for k, v in plugin.items() if k not in ("skills", "hooks")} != expected:
            raise ValueError(f"{name}: differs from the marketplace plugin")
        if plugin.get("hooks") != "./hooks/hooks.json" or plugin.get("skills") != [
                "./skills/steer", "./skills/steer-report", "./skills/steer-setup"]:
            raise ValueError(f"{name}: missing bundled skill or hook declarations")
    return entries[0]


def inspect_checkout(root, expected, expected_hooks=None, expected_digests=None, expected_skill_digests=None):
    for name in (*MARKETS, *PLUGINS, "README.md", "hooks/hooks.json", *SKILL_FILES):
        if not contained_file(root, name):
            raise ValueError(f"published bundle file is missing or outside the bundle: {name}")
    if identity(root) != expected:
        raise ValueError("published manifests differ from this checkout")
    marketplace, entry = expected
    readme = (root / "README.md").read_text()
    slug = entry["repository"].removeprefix("https://github.com/")
    adds = re.findall(r"marketplace add ([^\s`'\"]+)", readme)
    installs = re.findall(r"plugin (?:install|add) steer@([^\s`'\"]+)", readme)
    if not adds or any(a.rstrip(".,;:)") not in (slug, entry["repository"]) for a in adds):
        raise ValueError("published README marketplace commands disagree with the repository")
    if not installs or any(i.rstrip(".,;:)") != marketplace for i in installs):
        raise ValueError("published README plugin commands disagree with the marketplace")
    if entry["source"] != "./":
        raise ValueError("pre-flight currently supports the bundled ./ plugin source only")
    for name in EXECUTABLES:
        path = root / name
        if not path.is_file() or path.is_symlink() or not os.access(path, os.X_OK):
            raise ValueError(f"published artifact is missing or not executable: {name}")
    if expected_digests is None:
        expected_digests = artifact_digests(ROOT)
    if artifact_digests(root) != expected_digests:
        raise ValueError("published executable bytes differ from the candidate bundle")
    if expected_skill_digests is None:
        expected_skill_digests = artifact_digests(ROOT, SKILL_FILES)
    if artifact_digests(root, SKILL_FILES) != expected_skill_digests:
        raise ValueError("published skill bytes differ from the candidate bundle")
    if expected_hooks is None:
        expected_hooks = json.loads((ROOT / "hooks" / "hooks.json").read_text())
    if json.loads((root / "hooks" / "hooks.json").read_text()) != expected_hooks:
        raise ValueError("published hook wiring differs from this checkout")


def preflight(root, ref=None):
    expected = identity(root)
    expected_hooks = json.loads((root / "hooks" / "hooks.json").read_text())
    expected_digests = artifact_digests(root)
    expected_skill_digests = artifact_digests(root, SKILL_FILES)
    repository = expected[1]["repository"]
    slug = repository.removeprefix("https://github.com/")
    request = Request(f"https://api.github.com/repos/{slug}",
                      headers={"User-Agent": "steer-release-preflight", "Accept": "application/vnd.github+json"})
    with urlopen(request, timeout=30) as response:
        public = json.load(response)
    if public.get("private") is not False:
        raise ValueError("repository is not public")
    selected = ref or public["default_branch"]
    with tempfile.TemporaryDirectory(prefix="steer-preflight-") as scratch:
        home = Path(scratch) / "home"
        home.mkdir()
        env = {"PATH": os.environ.get("PATH", os.defpath), "HOME": str(home),
               "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
               "GIT_TERMINAL_PROMPT": "0", "GIT_ASKPASS": "/usr/bin/false"}
        checkout = Path(scratch) / "plugin"
        subprocess.run(["git", "-c", "credential.helper=", "clone", "--depth", "1",
                        "--single-branch", "--branch", selected, "--", repository, str(checkout)],
                       cwd=scratch, env=env, check=True, timeout=180)
        inspect_checkout(checkout, expected, expected_hooks, expected_digests, expected_skill_digests)
        sha = subprocess.check_output(["git", "-C", str(checkout), "rev-parse", "HEAD"],
                                      env=env, text=True, timeout=10).strip()
    return {"repository": repository, "ref": selected, "commit": sha,
            "version": expected[1]["version"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ref", help="Published branch or tag (default: repository default branch)")
    args = parser.parse_args()
    try:
        print(json.dumps(preflight(ROOT, args.ref), indent=2))
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"release pre-flight: FAIL: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
