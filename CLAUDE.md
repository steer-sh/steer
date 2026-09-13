# Writing this file

This is a public repository document. Include contributor-facing project guidance
only; keep private development workflows, personal context and internal discussions
out of this file.

CLAUDE.md is the operational map for this repository: where to find knowledge
that is expensive to reconstruct from the codebase, and when to consult it.

- Add or update an entry when a change affects a cross-component editing obligation,
  a non-obvious entry point, a build/verification workflow, or a storage/registration
  trap that would otherwise require reading several files to discover.
- Write the task trigger, the authoritative path or symbol, and the essential
  constraint. Prefer a short pointer over an implementation walkthrough.
- Put current component relationships, data flow and design invariants in
  [ARCHITECTURE.md](ARCHITECTURE.md); keep the trigger for reading them here.
- Put decision rationale, rejected alternatives and superseded approaches in the
  relevant commit body/trailers. Neither document is a session log, a changelog,
  or a list of completed work.
- Read the owning code and relevant tests before asserting behavior. Update the
  existing entry in the same change; remove stale or duplicate claims. Link to
  machine-readable defaults, schemas and CLI help instead of copying them.
- If an addition becomes a detailed subsection, fold it into the relevant
  architecture section and retain only its navigation value here. An ordinary
  implementation change needs no CLAUDE.md addition.

# steer-skill

Steer reviews commits, mines recurring failures, installs guards or instructions,
and reports recurrence. The Go CLI and bundled skills share contracts; a change
can affect both even when they live in different directories.

## Where to start

Read the linked architecture section before changing the corresponding boundary:

- Plugin packaging or setup: [distribution](ARCHITECTURE.md#distribution-and-installation).
  `scripts/steer` dispatches to committed `bin/<os>-<arch>/` builds;
  `cli/internal/plugindist/` checks the four plugin/marketplace manifests together.
  The working skills live under `skills/`, including `steer`, `steer-report` and
  `steer-setup`; there is no root generation `SKILL.md`.
- Harness support, paths, or migrations: [harnesses and storage](ARCHITECTURE.md#harnesses-and-storage).
  Start with `cli/internal/harness/` and `cli/internal/steerpath/`; registration
  adapters and shared stores have different ownership.
- Guard/rule lifecycle or instruction synchronization: [guards and instructions](ARCHITECTURE.md#guards-and-instructions).
  Start with `cli/internal/claude/lifecycle.go`, `rules.go` and `sync.go`.
  `## Steer Rules` is derived from the manifest; change it through the lifecycle.
- Guard execution or fixture mining: [runtime and fixtures](ARCHITECTURE.md#guard-runtime-and-fixtures).
  `cli/internal/guard/runtime.py` is embedded in generated masters. A CLI upgrade
  needs `steer hooks rebuild --scope <scope>` to update an installed master;
  fixture runs must keep synthetic firings out of the live audit log.
- Commit review, reviewer settings, or outcome reconciliation:
  [commit gate](ARCHITECTURE.md#commit-gate-and-resolver). Start with
  `cli/internal/gate/`; its adapters own harness payloads and replies.
  Codex hook payloads can omit a nested shell workdir: select the repository
  explicitly with `git -C /absolute/path commit` when committing through code mode.
- Transcript ingestion, classification, or snapshots:
  [transcripts](ARCHITECTURE.md#transcripts-and-session-evidence).
  `cli/internal/transcript/` owns normalization; `cli/internal/report/` classifies
  events. Root sessions are the unit; subagent history and abandoned branches
  require adapter-specific handling.
- Intervention records, eligibility, or scoring:
  [observations](ARCHITECTURE.md#interventions-and-observations).
  `cli/internal/gate/mine/` owns both corpus engines. Field counts describe
  recurrence; they cannot establish a fix's efficacy or justify retirement.
  A check-tier record's binding is frozen by `cli/internal/checkscope/`, which
  may read the harness adapters that `gate/mine` may not.
- Queue, status, status line, or dashboard:
  [readers and controls](ARCHITECTURE.md#cadence-status-and-controls).
  `cli/internal/status/` owns shared phrasing; the TUI calls shared mutation
  functions. Registration, executable resolution and observed activity are
  distinct facts.

## Changes that must travel together

- Report digest: `cli/internal/report/digest.go` and
  `skills/steer-report/SKILL.md`.
- Status JSON: `cli/internal/status/status.go`, `skills/steer/SKILL.md` and
  `skills/steer-report/SKILL.md`; `TestJSONContractIsStable` pins the contract.
  The check tier's share of it lives in `cli/internal/status/checks.go`.
- Refresh queue: `cli/internal/cadence/refresh.go` and
  `skills/steer-report/SKILL.md`. Read version constants at their source.
- Check tier readings: `cli/internal/checks/state.go` is the one reader of a
  repository's check state, shared by the status snapshot, the refresh queue and
  `steer check list`. Adoption, registration, validation freshness and observed
  execution stay four separate facts.
- Generation behavior: bump `prompt_version` in `skills/steer/SKILL.md`.
  Review templates have separate pinned digests in `cli/internal/gate/`.
- Transcript normalization: each adapter's `Instrument` is pinned by replay of
  its `testdata/instrument/` corpus. Classification changes also affect
  `report.ParserInstrument`; see the architecture section before changing either.
- Display wording: use shared `status`/`styles` helpers and check
  `cli/internal/status/vocabulary_test.go`. Stored tokens and human labels have
  different compatibility obligations.

## Build and verification

Run `./check.sh` before reporting a change complete. It checks gofmt, builds,
vets and runs Go tests, then `scripts/revert-test.py`: each Go test added since
the upstream branch must fail under some variant of the change, and
`// pins existing behaviour` in a test's doc comment exempts it. Its per-test
evidence (deciding variant, failure excerpt, diffs, full output) is written to
`.git/revert-test/<key>.json`, and the console names the file. `./check-full.sh`
adds the slow headless skill-generation harness and invokes `claude -p`; it is
separate from the turn-end gate.

After CLI changes, rebuild the committed platform binaries before committing:

```bash
cd cli
VERSION=0.1.0 ./build.sh
```

The model-driven tests under `tests/` need PyYAML in `tests/.venv`. Read the
runner's help for options and select the model explicitly. Focused generation:
`tests/.venv/bin/python3 tests/skill_test.py --test <name> --model <model>`;
the full serial suite uses `tests/run_tests.py --model <model>` with that Python.

For report prose changes, `tests/report_scenarios.py` copies the working-tree
skill into saved scenarios and writes output under `tests/scenarios/out/`.
Read `tests/scenarios/README.md` for the human assessment checklist; the runner
has no mechanical pass/fail oracle for comprehension.

## Operational locations

- Operator settings: `~/.steer/config.json`; use `steer config show` to see values
  and their source. `cli/internal/settings/` owns the shared settings metadata.
- Evidence: `~/.steer/state/<component>/`, resolved through `statestore` with
  legacy-path fallback. Ledgers, snapshots and logs are machine-local and partial.
- Guard/rule stores: `.steer/hooks` and `~/.steer/hooks`, resolved through
  `hookstore`. Read the registered master when diagnosing execution or audit data.
- Stable hook executable: `~/.steer/bin/steer`; `steer bin install` updates it and
  repairs Steer-owned registrations. Reviewer timeout changes need channel
  re-registration to update the harness timeout too.
