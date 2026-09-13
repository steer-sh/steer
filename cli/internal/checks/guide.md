# Authoring repository checks

A repository check is an executable test of repository content. `check.sh` at the
repository root is the single command that runs every blocking check; a harness
Stop hook runs it at the end of an agent's turn and blocks the turn while it
exits non-zero. Steer manages individual checks inside that oracle so each one
has a stable id, replayable proof and its own execution record.

## What belongs in the blocking tier

Put a check in `check.sh` only when its failure is always the code's fault and
always fixable by editing code in this turn:

- build, type check, and lint with warnings as errors
- unit and integration tests against local, deterministic fixtures
- searches for rules a linter cannot express

Keep these out. Run them by hand or in CI instead:

- anything that reaches the network, a live API, credentials, a device or a simulator
- anything that depends on the host's clock, time zone or locale
- long end-to-end suites and anything with a known flake
- a proxy for quality that an agent can game, such as a coverage number it reads

A check that fails for environmental reasons blocks turns the agent cannot clear.
When you are unsure whether a check is deterministic, keep it out.

## Runtime cost

The whole Stop run has one 300-second deadline. Order `check.sh` cheap and broad
first, expensive and narrow last. A slow oracle pushes the agent to reason
instead of verifying, so keep each managed check to seconds.

Use `set -euo pipefail`, and never pipe a test runner: `go test ./... | tail`
exits with the status of `tail`, so a failing suite reads as a pass.

## Representative failure cases

A check that has never failed is untested. Every managed check needs a
known-good case that passes and a defect case that fails with the diagnostic the
check exists to produce. The defect case reproduces the real defect: an
unrelated failure, such as a missing command or a build error elsewhere, does
not show that the check detects anything.

## Binding kinds

Declarations live in `.steer/checks/manifest.json`. Each has a stable `id` and
one binding kind:

- `script`: a repository-relative script that Steer runs. Use it for searches
  and for checks that no test runner reports on.
- `go-test`: one top-level test function in the repository's Go suite, named by
  its package directory (repository-relative, such as `cli/internal/status`)
  and its exact name. The test stays in the ordinary suite; Steer reads its
  result from that run and never runs it a second time.

`inputs` lists further repository-relative files that belong to the detector:
helpers, data files and fixtures. For a Go test, list what the test reads at
run time; for a script, list every file it sources or calls. Production code
under test is not a detector input.

## Cases

A check's cases live in `.steer/checks/cases/<id>.json`:

```json
{
  "version": 1,
  "cases": [
    {"name": "clean", "exit_code": 0, "provenance": "the fixed tree",
     "files": {"src/run.sh": "echo \"tests passed\"\n"}},
    {"name": "literal-count", "exit_code": 1, "diagnostic": "literal test count",
     "provenance": "the defect from the review of commit abc123",
     "files": {"src/run.sh": "echo \"42 tests passed\"\n"}}
  ]
}
```

Each case runs in its own temporary tree holding its `files` and a copy of the
detector (the script and its `inputs`). `setup` is an optional Bash script run
in the tree first, for state plain files cannot express, such as `git init`.
The repository itself is never written.

For a `go-test` binding, a case supplies the module and the production code
(`go.mod` and the package's non-test files); `exit_code` 0 means the named test
passes and 1 means it fails. Steer removes the package's test files from the
tree and writes one generated test file holding the named test and the
test-side declarations it reaches: helpers, types, variables and constants.
Replay runs `go test -count=1` with `GOPROXY=off`, so a missing module makes the
case invalid instead of downloading. The diagnostic must appear in the named
test's own output.

What the Go analysis sees but cannot track, such as an imported helper package
or a `testdata` path, is listed as not tracked. Declare such files with
`--input` so an edit to them marks the receipt stale.

A defect case matches only when the check exits with its `exit_code` and prints
its `diagnostic`. A failed setup, a command that cannot run (exit 126 or 127),
or a timeout makes the case invalid: it proves nothing either way.

## Snapshot cases

When the defect lives in code too large to copy into `files`, such as a Go
package and its dependencies, start the case from a commit instead:

```json
{"name": "fixed", "exit_code": 0, "snapshot": "<full commit hash of the fix>",
 "provenance": "the fix in commit abc123"},
{"name": "omits-id", "exit_code": 1, "diagnostic": "missing field id",
 "snapshot": "<the same hash>",
 "patch": ".steer/checks/cases/resp-fields/omits-id.patch",
 "provenance": "the defect commit abc123 fixed"}
```

Replay writes the commit's tree into a temporary directory, copies the current
detector over it, applies the case's `patch`, runs `setup`, then runs the
check. The repository's work tree and index are never written. Make the patch
restore the defect in production code only, for example
`git diff <fix> <fix>^ -- <production paths>`: a patch that changes a detector
file makes the case invalid. A snapshot case takes no `files`.

Validation keeps each commit a case names under `refs/steer/snapshots/`, so
garbage collection does not remove it. A clone that lacks the commit reports
the case as snapshot unavailable. A patch that no longer applies, or code that
no longer builds with the current detector, is an incompatible case: author it
again. A module or toolchain that the offline replay cannot load is an
unavailable environment. None of these satisfies an expected outcome, and the
previous receipt stays as history.

## Running checks from check.sh

`steer check init` writes a `check.sh` scaffold when the repository has none.
It never changes an existing `check.sh`: add the managed lines to its sequence
yourself. The scaffold resolves Steer from `STEER_BIN`, then from `PATH`, and
fails with an instruction when neither holds it; a missing Steer never skips
the checks.

- `"$steer" check exec --all` runs every `script` binding, one after another,
  from the repository root. A failing check does not stop the checks after it.
  It exits non-zero when any check did not pass.
- `"$steer" check go-test -- ./...` replaces `go test ./...`. It runs the suite
  once with `-json`, prints the build errors, failing test output and package
  results, and exits with the suite's status. Each `go-test` binding is read
  from that run; no test runs twice. Put go test flags after `--`.

Let each line record its failure and continue, so one failing line does not
hide the others:

```bash
status=0
"$steer" check exec --all || status=1
"$steer" check go-test -- ./... || status=1
exit "$status"
```

Each run appends one record per check to machine-local execution evidence:
passed, failed, error (the check or a command it calls could not run),
timeout, or not executed (the deadline came first). A `go-test` binding can
also read skipped, passed-cached (the toolchain replayed a cached pass; this
is not a fresh run), or unavailable (its package ran but did not build, or
reported no result for the test, for example because `-run` excluded it). A
binding whose package the run never reached gets no record from the adapter.
A passing `check.sh` alone never shows that a particular check ran.

## Running the oracle and adopting the repository

`steer check run` runs `check.sh` once, the way the Stop hook does: with bash
from the repository root, under a new run ID and one deadline for the whole
run (`--timeout`, 300 seconds by default), and records the aggregate outcome
apart from the per-check records. It then compares the declared checks with the
records under that run ID. A declared check with no record is recorded as not
executed. When `check.sh` passed without running a declared check, that is an
integration failure: the oracle does not call its executor, which says nothing
about the check's assertion. `check run` exits non-zero unless `check.sh`
passed and every declared check reported.

Automatic runs need adoption. `steer check adopt [repo]` authorizes them for
the repository's exact resolved root on this machine; a clone at another path
is not adopted. Adoption replays each declared check that has no current
receipt, then runs `check.sh` with isolated evidence that stays out of the
live records. It is refused unless every check validated and `check.sh`
passed with every declared check reported. Adoption survives later edits; a
check whose detector changed keeps running with stale validation.
`steer check unadopt [repo]` withdraws it and keeps all evidence.

## Commands

- `steer check init [repo]` creates an empty manifest and, when absent, the
  `check.sh` scaffold. It keeps existing files.
- `steer check exec (--all | <id>...)` runs script checks and records each
  outcome.
- `steer check go-test [-- <go test arguments>]` runs the Go suite once and
  records each bound test's result.
- `steer check add <id> --script <path> [--input <path>]...` replays the cases
  and declares the check only when every case matched.
- `steer check add <id> --go-package <dir> --go-test <name> [--input <path>]...`
  does the same for a test in the Go suite.
- `steer check test <id>` replays a declared check's cases with its current
  detector. Run it after you edit the check, its inputs or its cases.
- `steer check list` shows the adoption, the latest `check.sh` run, and each
  check with its validation (current, stale with what changed since the
  receipt, or never validated) and its latest execution on this machine. A
  stale check keeps running.
- `steer check remove <id>` drops the declaration and keeps its cases and
  validation history.
- `steer check run [--timeout <duration>]` runs `check.sh` once and reports each
  declared check's result under that run.
- `steer check adopt [repo]` and `steer check unadopt [repo]` grant and withdraw
  this machine's authorization for automatic runs.
- `steer check guide` prints this guide.
