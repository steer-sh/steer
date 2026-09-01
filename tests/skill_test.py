#!/usr/bin/env python3
"""
Skill-based test harness for steer.

Generates hooks via the steer Claude Code skill (headless mode), then validates
them against canonical positive/negative test files from prompts.yaml.

Usage:
    python3 tests/skill_test.py --test python-command-injection
    python3 tests/skill_test.py --test console-log-block --model claude-sonnet-4-6
    python3 tests/skill_test.py --test sql-injection-prevention --save
    python3 tests/skill_test.py --test sql-injection-prevention --save /tmp/hooks
    python3 tests/skill_test.py --list

Note: Run from the steer repo root.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml


TESTS_DIR = Path(__file__).parent
PROMPTS_YAML = TESTS_DIR / "test_data" / "prompts.yaml"
POSITIVE_DIR = TESTS_DIR / "test_data" / "positive_files"
NEGATIVE_DIR = TESTS_DIR / "test_data" / "negative_files"
IMAGES_DIR = TESTS_DIR / "test_data" / "images"
FAILURES_DIR = TESTS_DIR / "failures"
DEFAULT_MODEL = "claude-opus-4-6"
DEFAULT_SAVE_DIR = TESTS_DIR.parent / "cli" / "internal" / "presets" / "hooks"


def load_test_cases():
    with open(PROMPTS_YAML) as f:
        data = yaml.safe_load(f)
    return {tc["name"]: tc for tc in data["test_cases"]}


GLOBAL_CLAUDE_DIR = Path.home() / ".claude"
GLOBAL_SETTINGS = GLOBAL_CLAUDE_DIR / "settings.json"
GLOBAL_CLAUDE_MD = GLOBAL_CLAUDE_DIR / "CLAUDE.md"
# The guard store is neutral. The legacy directory stays under watch because a
# model can still write a hook there by hand.
GLOBAL_HOOKS_DIRS = [Path.home() / ".steer" / "hooks", GLOBAL_CLAUDE_DIR / "hooks"]


def _read_hooks_tree() -> dict[str, bytes]:
    """Map of "<store index>/<relative path>" → content for every guard store file."""
    tree = {}
    for index, hooks_dir in enumerate(GLOBAL_HOOKS_DIRS):
        if not hooks_dir.exists():
            continue
        for path in sorted(hooks_dir.rglob("*")):
            if path.is_file():
                tree[f"{index}/{path.relative_to(hooks_dir)}"] = path.read_bytes()
    return tree


def _snapshot_global_state() -> dict:
    """Snapshot the steer-managed global surface before a generation run.

    The skill is instructed to register at project scope, but models sometimes
    register at user scope anyway (observed with haiku: hook files written into
    the global guard store, rule lines added to global CLAUDE.md, master hook wired
    into global settings.json). Snapshot everything a leak can touch.
    """
    return {
        "settings": GLOBAL_SETTINGS.read_text() if GLOBAL_SETTINGS.exists() else None,
        "claude_md": GLOBAL_CLAUDE_MD.read_text() if GLOBAL_CLAUDE_MD.exists() else None,
        "hooks": _read_hooks_tree(),
    }


def _restore_file(path: Path, snapshot: str | None):
    if snapshot is None:
        path.unlink(missing_ok=True)
    else:
        path.write_text(snapshot)


def _restore_global_state(snapshot: dict) -> bool:
    """Restore the global surface to its pre-test state.

    Returns True if anything had been changed (i.e. the generation leaked
    out of its temp workdir).
    """
    leaked = snapshot != _snapshot_global_state()
    _restore_file(GLOBAL_SETTINGS, snapshot["settings"])
    _restore_file(GLOBAL_CLAUDE_MD, snapshot["claude_md"])

    for index, hooks_dir in enumerate(GLOBAL_HOOKS_DIRS):
        if not hooks_dir.exists():
            continue
        for path in list(hooks_dir.rglob("*")):
            if path.is_file() and f"{index}/{path.relative_to(hooks_dir)}" not in snapshot["hooks"]:
                path.unlink()
    for key, content in snapshot["hooks"].items():
        index, rel = key.split("/", 1)
        target = GLOBAL_HOOKS_DIRS[int(index)] / rel
        if not target.exists() or target.read_bytes() != content:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)

    return leaked


def generate_hook(rule: str, workdir: Path, model: str) -> dict:
    """Run the steer skill headlessly and return the parsed JSON result."""
    steer_path = shutil.which("steer")
    if not steer_path:
        print("  ERROR: steer CLI not found in PATH.")
        print("  Build and install: cd cli && bash build.sh --install")
        sys.exit(1)

    env = os.environ.copy()
    env.pop("CLAUDECODE", None)  # Prevent nested session error

    cmd = [
        "claude", "-p",
        f"/steer --headless {rule}",
        "--output-format", "json",
        "--model", model,
        "--no-session-persistence",
        "--dangerously-skip-permissions",
    ]

    # Snapshot the global steer surface before the run — a generation that
    # registers at user scope would poison live sessions and later tests.
    state_snapshot = _snapshot_global_state()

    print(
        f"  Running: claude -p \"/steer --headless {rule[:60]}...\" "
        f"--model {model} --no-session-persistence"
    )
    result = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        cwd=str(workdir),
        env=env,
    )

    # Always restore global state after the run, whether it succeeded or not.
    if _restore_global_state(state_snapshot):
        print("  WARNING: generation leaked into the global store (restored). "
              "The hook may have been registered at user scope instead of the temp workdir.")

    if result.returncode != 0:
        print(f"  ERROR: claude exited with code {result.returncode}")
        print(f"  stderr: {result.stderr[:500]}")
        sys.exit(1)

    try:
        outer = json.loads(result.stdout)
    except json.JSONDecodeError:
        print(f"  ERROR: could not parse claude output as JSON")
        print(f"  stdout: {result.stdout[:500]}")
        sys.exit(1)

    if outer.get("is_error"):
        print(f"  ERROR: skill returned error")
        print(f"  result: {outer.get('result', '')[:500]}")
        sys.exit(1)

    # The skill's JSON result is embedded in the 'result' field as a ```json block
    raw_result = outer.get("result", "")
    cost = outer.get("total_cost_usd", 0)
    turns = outer.get("num_turns", 0)

    # Extract JSON from markdown code block if present
    if "```json" in raw_result:
        start = raw_result.index("```json") + 7
        end = raw_result.index("```", start)
        raw_result = raw_result[start:end].strip()

    try:
        skill_result = json.loads(raw_result)
    except json.JSONDecodeError:
        # Maybe the result field IS the JSON (no markdown wrapper)
        try:
            skill_result = json.loads(outer.get("result", "{}"))
        except json.JSONDecodeError:
            print(f"  ERROR: could not parse skill result JSON")
            print(f"  raw: {outer.get('result', '')[:500]}")
            sys.exit(1)

    skill_result["_cost_usd"] = cost
    skill_result["_turns"] = turns
    return skill_result


def resolve_save_dir(save_arg: str | None) -> Path | None:
    """Resolve --save argument to an absolute directory path."""
    if save_arg is None:
        return None
    if save_arg == "__factory_default__":
        return DEFAULT_SAVE_DIR.resolve()
    return Path(save_arg).expanduser().resolve()


def save_hook_artifact(hook_path: Path, test_name: str, save_dir: Path) -> Path:
    """Persist a passing generated hook to a stable filename."""
    save_dir.mkdir(parents=True, exist_ok=True)
    destination = save_dir / f"{test_name}.py"
    shutil.copy2(hook_path, destination)
    destination.chmod(0o755)
    return destination


def _prepare_fixture_workspace(workdir: Path) -> Path:
    """
    Create a temporary workspace tree with canonical fixtures.

    Hooks receive file paths under this workspace so path-sensitive logic
    (e.g., resolving ../images/* from markdown) behaves like real usage.
    """
    workspace_src = workdir / "workspace" / "src"
    workspace_src.mkdir(parents=True, exist_ok=True)

    shutil.copytree(POSITIVE_DIR, workspace_src / "positive_files")
    shutil.copytree(NEGATIVE_DIR, workspace_src / "negative_files")

    if IMAGES_DIR.exists():
        shutil.copytree(IMAGES_DIR, workspace_src / "images")

    return workspace_src


def _workspace_path_for_test_file(test_file: Path, workspace_src: Path) -> Path:
    """Map a canonical fixture file to its staged workspace path."""
    try:
        rel = test_file.relative_to(POSITIVE_DIR)
        return workspace_src / "positive_files" / rel
    except ValueError:
        pass

    try:
        rel = test_file.relative_to(NEGATIVE_DIR)
        return workspace_src / "negative_files" / rel
    except ValueError:
        pass

    # Fallback for unexpected paths; keeps extension/language semantics.
    return workspace_src / test_file.name


def run_hook_against_file(
    hook_path: Path,
    test_file: Path,
    workspace_src: Path,
) -> tuple[bool, str]:
    """Pipe a test file through the hook. Returns (was_blocked, stdout)."""
    with open(test_file) as f:
        content = f.read()

    staged_file_path = _workspace_path_for_test_file(test_file, workspace_src)
    inferred_tool_name = "Bash" if staged_file_path.suffix in {".sh", ".bash", ".zsh"} else "Write"

    # Some hooks correctly scope to canonical package.json edits.
    # For package fixtures, preserve realistic filename semantics in the payload.
    payload_file_path = staged_file_path
    if staged_file_path.suffix == ".json" and staged_file_path.name.startswith("package_"):
        payload_file_path = staged_file_path.with_name("package.json")

    if inferred_tool_name == "Bash":
        # Shell fixtures may include explanatory comments. Provide executable
        # command text so Bash-targeted hooks evaluate the actual command.
        command_lines = []
        for line in content.splitlines():
            stripped = line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            command_lines.append(stripped)
        command_text = "\n".join(command_lines).strip() or content

        payload = json.dumps({
            "tool_name": "Bash",
            "tool_input": {
                "command": command_text,
            }
        })
    else:
        payload = json.dumps({
            "tool_name": "Write",
            "tool_input": {
                "file_path": str(payload_file_path),
                "content": content,
            }
        })

    result = subprocess.run(
        ["python3", str(hook_path)],
        input=payload,
        capture_output=True,
        text=True,
    )

    stdout = result.stdout.strip()
    blocked = bool(stdout) and '"decision": "block"' in stdout
    return blocked, stdout


def run_test_case(test_case: dict, model: str, save_dir: Path | None = None) -> tuple[bool, float]:
    """Generate hook and validate against canonical files. Returns (passed, cost_usd)."""
    name = test_case["name"]
    rule = test_case["rule"]

    print(f"\n{'='*60}")
    print(f"Test: {name}")
    print(f"Rule: {rule[:80]}{'...' if len(rule) > 80 else ''}")
    print(f"{'='*60}")

    with tempfile.TemporaryDirectory(prefix=f"steer-test-{name}-") as tmpdir:
        workdir = Path(tmpdir)

        # Step 1: Generate hook via skill
        print("\n[1/2] Generating hook...")
        skill_result = generate_hook(rule, workdir, model)

        self_passed = skill_result.get("self_test_passed", False)
        summary = skill_result.get("self_test_summary", {})
        cost = skill_result.get("_cost_usd", 0)
        turns = skill_result.get("_turns", 0)
        hook_path_rel = skill_result.get("hook_path", "")

        print(f"  Self-test: {'PASS' if self_passed else 'FAIL'} "
              f"({summary.get('positive_pass', 0)}+ / {summary.get('negative_pass', 0)}-)")
        print(f"  Cost: ${cost:.4f}  Turns: {turns}")

        if not hook_path_rel:
            print("  ERROR: no hook_path in skill result")
            return False, cost

        hook_path = workdir / hook_path_rel
        if not hook_path.exists():
            print(f"  ERROR: hook file not found at {hook_path}")
            return False, cost

        fixture_workspace = _prepare_fixture_workspace(workdir)

        # Step 2: Run canonical test files
        print("\n[2/2] Validating against canonical test files...")

        all_pass = True
        positive_files = test_case.get("positive_files", [])
        negative_files = test_case.get("negative_files", [])

        for pf in positive_files:
            path = POSITIVE_DIR / pf["path"]
            if not path.exists():
                print(f"  SKIP [+] {pf['path']} (file not found)")
                continue
            blocked, _ = run_hook_against_file(hook_path, path, fixture_workspace)
            status = "PASS" if blocked else "FAIL"
            if not blocked:
                all_pass = False
            print(f"  {status} [POSITIVE] {pf['path']} → {'blocked' if blocked else 'ALLOWED (should have blocked)'}")

        for nf in negative_files:
            path = NEGATIVE_DIR / nf["path"]
            if not path.exists():
                print(f"  SKIP [-] {nf['path']} (file not found)")
                continue
            blocked, stdout = run_hook_against_file(hook_path, path, fixture_workspace)
            status = "PASS" if not blocked else "FAIL"
            if blocked:
                all_pass = False
            print(f"  {status} [NEGATIVE] {nf['path']} → {'allowed' if not blocked else 'BLOCKED (false positive)'}")
            if blocked:
                # Show what triggered it
                try:
                    reason = json.loads(stdout).get("stopReason", stdout)[:120]
                    print(f"         reason: {reason}")
                except Exception:
                    print(f"         output: {stdout[:120]}")

        if not all_pass:
            # Save hook and result to persistent directory for debugging
            fail_dir = FAILURES_DIR / name
            fail_dir.mkdir(parents=True, exist_ok=True)
            shutil.copy2(hook_path, fail_dir / hook_path.name)
            (fail_dir / "skill_result.json").write_text(
                json.dumps(skill_result, indent=2)
            )
            print(f"\n  Hook saved for inspection: {fail_dir}/")
        elif save_dir is not None:
            saved_path = save_hook_artifact(hook_path, name, save_dir)
            print(f"\n  Hook saved to factory library path: {saved_path}")

        print(f"\nResult: {'ALL PASS' if all_pass else 'FAILURES DETECTED'}")
        return all_pass, cost


def main():
    parser = argparse.ArgumentParser(
        description="Validate steer skill hook generation against canonical test cases"
    )
    parser.add_argument(
        "--test", "-t",
        help="Test case name to run (from prompts.yaml)"
    )
    parser.add_argument(
        "--model", "-m",
        default=DEFAULT_MODEL,
        help=f"Claude model to use (default: {DEFAULT_MODEL})"
    )
    parser.add_argument(
        "--list", "-l",
        action="store_true",
        help="List available test case names"
    )
    parser.add_argument(
        "--save",
        nargs="?",
        const="__factory_default__",
        metavar="DIR",
        help="If test passes, save generated hook to DIR. Use --save with no DIR to save into cli/internal/presets/hooks/"
    )
    args = parser.parse_args()

    test_cases = load_test_cases()

    if args.list:
        print("Available test cases:")
        for name in sorted(test_cases):
            rule = test_cases[name]["rule"][:70]
            print(f"  {name:<40} {rule}")
        return

    if not args.test:
        parser.print_help()
        sys.exit(1)

    if args.test not in test_cases:
        print(f"ERROR: test case '{args.test}' not found in prompts.yaml")
        print(f"Run with --list to see available test cases")
        sys.exit(1)

    save_dir = resolve_save_dir(args.save)
    passed, cost = run_test_case(test_cases[args.test], args.model, save_dir=save_dir)
    sys.exit(0 if passed else 1)


if __name__ == "__main__":
    main()
