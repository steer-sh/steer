#!/usr/bin/env python3
"""
Run all (or filtered) steer skill tests sequentially.

Uses the same skill-based headless generation as skill_test.py, but loops
over every test case in prompts.yaml and produces a summary with total cost.

Usage:
    python3 tests/run_tests.py                          # run all tests
    python3 tests/run_tests.py --filter python           # run tests matching "python"
    python3 tests/run_tests.py --stop-on-fail            # bail on first failure
    python3 tests/run_tests.py --model claude-sonnet-4-6 # override model
    python3 tests/run_tests.py --filter security --save  # save passing hooks to factory library
    python3 tests/run_tests.py --list                    # list available tests
"""

import argparse
import sys
import time
from pathlib import Path

from skill_test import (
    load_test_cases,
    run_test_case,
    DEFAULT_MODEL,
    resolve_save_dir,
)


def main():
    parser = argparse.ArgumentParser(
        description="Run all steer skill tests against canonical test files"
    )
    parser.add_argument(
        "--filter", "-f",
        help="Only run tests whose name contains this substring"
    )
    parser.add_argument(
        "--model", "-m",
        default=DEFAULT_MODEL,
        help=f"Claude model to use (default: {DEFAULT_MODEL})"
    )
    parser.add_argument(
        "--stop-on-fail", "-s",
        action="store_true",
        help="Stop after the first failing test"
    )
    parser.add_argument(
        "--list", "-l",
        action="store_true",
        help="List test cases that would run (with filter applied) and exit"
    )
    parser.add_argument(
        "--save",
        nargs="?",
        const="__factory_default__",
        metavar="DIR",
        help="If a test passes, save generated hook to DIR. Use --save with no DIR to save into cli/internal/presets/hooks/"
    )
    args = parser.parse_args()
    save_dir: Path | None = resolve_save_dir(args.save)

    all_cases = load_test_cases()

    # Apply filter
    if args.filter:
        cases = {k: v for k, v in all_cases.items() if args.filter in k}
    else:
        cases = all_cases

    if not cases:
        print(f"No test cases match filter '{args.filter}'")
        sys.exit(1)

    if args.list:
        print(f"Tests to run ({len(cases)}/{len(all_cases)}):")
        for name in cases:
            rule = cases[name]["rule"][:70]
            print(f"  {name:<40} {rule}")
        if save_dir is not None:
            print(f"\nPassing hooks will be saved to: {save_dir}")
        return

    # Run
    total = len(cases)
    passed = 0
    failed = 0
    errors = []
    total_cost = 0.0
    start = time.time()

    print(f"Running {total} test(s) with model {args.model}\n")

    for i, (name, tc) in enumerate(cases.items(), 1):
        print(f"\n[{i}/{total}] ", end="")
        try:
            ok, cost = run_test_case(tc, args.model, save_dir=save_dir)
            total_cost += cost
        except SystemExit:
            # run_test_case calls sys.exit on generation errors — catch it
            ok = False
        except Exception as e:
            print(f"  EXCEPTION: {e}")
            ok = False

        if ok:
            passed += 1
        else:
            failed += 1
            errors.append(name)
            if args.stop_on_fail:
                print(f"\n--stop-on-fail: stopping after {name}")
                break

    elapsed = time.time() - start

    # Summary
    print(f"\n{'='*60}")
    print(f"SUMMARY")
    print(f"{'='*60}")
    print(f"  Total:   {passed + failed}/{total}")
    print(f"  Passed:  {passed}")
    print(f"  Failed:  {failed}")
    print(f"  Cost:    ${total_cost:.2f}")
    print(f"  Time:    {elapsed:.0f}s ({elapsed/60:.1f}m)")

    if errors:
        print(f"\nFailed tests:")
        for name in errors:
            print(f"  - {name}")
        print(f"\nRe-run individually:")
        print(f"  python3 tests/skill_test.py --test <name>")

    print()
    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    sys.exit(main())
