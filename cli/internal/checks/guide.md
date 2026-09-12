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

## Commands

- `steer check init [repo]` creates an empty manifest. It keeps an existing one.
- `steer check add <id> --script <path> [--input <path>]...` replays the cases
  and declares the check only when every case matched.
- `steer check add <id> --go-package <dir> --go-test <name> [--input <path>]...`
  does the same for a test in the Go suite.
- `steer check test <id>` replays a declared check's cases with its current
  detector. Run it after you edit the check, its inputs or its cases.
- `steer check list` shows each check and its validation: current, stale (what
  changed since the receipt), or never validated. A stale check keeps running.
- `steer check remove <id>` drops the declaration and keeps its cases and
  validation history.
- `steer check guide` prints this guide.
