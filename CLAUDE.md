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
  A harness runs skills and the launcher from its cached plugin copy, which an
  unchanged version does not refresh and `steer bin install` never touches;
  `posture.plugins` (`cli/internal/status/plugins.go`) compares each copy's binary
  with `~/.steer/bin/steer`.
- Harness support, paths, or migrations: [harnesses and storage](ARCHITECTURE.md#harnesses-and-storage).
  Start with `cli/internal/harness/` and `cli/internal/steerpath/`; registration
  adapters and shared stores have different ownership.
  Pi is unlisted: help text and messages offer only harnesses `harness.Listed`
  accepts, and a status row names Pi only where a Pi registration or evidence
  exists. A new user-facing harness listing must apply the same test.
  Codex hook writers share `cli/internal/codex/hooks.go`'s `save`; every mutation
  must pass its scope so project writes refuse symlinked configuration paths.
  Pi bridge mutations share `cli/internal/pi/registrar.go`'s
  `refuseLinkedBridge`; writes and removals must pass their scope to refuse
  project configuration links while retaining operator-selected global paths.
  Importing a personal check gate is `cli/cmd/check_migrate.go`: it reads the
  operator's harness files and never rewrites one without `--apply`.
- Guard/rule lifecycle or instruction synchronization: [guards and instructions](ARCHITECTURE.md#guards-and-instructions).
  Start with `cli/internal/claude/lifecycle.go`, `rules.go` and `sync.go`.
  `## Steer Rules` is derived from the manifest; change it through the lifecycle.
  A removal archives the fix first (`cli/internal/archive/`); a new removal path
  must pass `RemoveOptions` and archive unless the catalog holds a copy.
  `steer archive restore` (`cli/cmd/archive.go`) goes back through each tier's
  installer, so a change to an installer's refusals changes restore too.
- Guard execution or fixture mining: [runtime and fixtures](ARCHITECTURE.md#guard-runtime-and-fixtures).
  `cli/internal/guard/runtime.py` is embedded in generated masters. A CLI upgrade
  needs `steer hooks rebuild --scope <scope>` to update an installed master;
  fixture runs must keep synthetic firings out of the live audit log.
  To probe a guard, use `steer test` or `tests/sandbox.py`; never pipe a payload
  into the installed master or run an agent against the real home. The runtime
  cannot tell that block from real work, so it lands in the live log as a firing
  and needs `steer audit exclude`.
- Check declarations, validation, execution, adoption or the Stop runner:
  [repository checks](ARCHITECTURE.md#repository-checks). Start with
  `cli/internal/checks/`. Its `guide.md` is embedded in the binary and is the
  authoring contract the skills cite, so a change to it needs a rebuild too.
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
  `cli/internal/sessions/Refresh` holds its per-host shard lock through the complete
  read-build-merge-save transaction; another snapshot writer must share that lock.
- Intervention records, eligibility, or scoring:
  [observations](ARCHITECTURE.md#interventions-and-observations).
  `cli/internal/gate/mine/` owns both corpus engines. Field counts describe
  recurrence; they cannot establish a fix's efficacy or justify retirement.
  Inventory mutations hold `mine.lockInventory` from loading records through
  `save`; locking only the final write still loses concurrent changes.
  A check-tier record's binding is frozen by `cli/internal/checkscope/`, which
  may read the harness adapters that `gate/mine` may not.
- Reading or judging a recorded guard firing or check.sh Stop block:
  [firing audits](ARCHITECTURE.md#firing-audits). Start with
  `cli/internal/firingaudit/` and `cli/cmd/audit.go`. The receipt stream in
  `cli/internal/adjudication/` is append-only, so a field a receipt must carry
  lands before the first receipt is written. A caller authors no identity:
  `Record` resolves the row and the continuation and copies the stamps. The
  installed guard's identity comes from the registered master through
  `claude.ReadMasterIdentity`, never from the manifest or the standalone guard.
- Queue, status, status line, or dashboard:
  [readers and controls](ARCHITECTURE.md#cadence-status-and-controls).
  `cli/internal/status/` owns shared phrasing; the TUI calls shared mutation
  functions. Registration, executable resolution and observed activity are
  distinct facts. A Claude Code mod can skip a registered hook; `posture.mods`
  (`cli/internal/mods/`) reads only the cache `steer mods inspect` writes.

## Changes that must travel together

- Report index and details: `cli/internal/report/digest.go` and `index.go`, and
  Step 2a of `skills/steer-report/SKILL.md`; a shape change bumps `DigestVersion`.
- Status JSON: the projection `cli/internal/status/json.go` and the one documented
  copy in `skills/steer-report/SKILL.md`, whose `interventions[]` row fields and
  their `steer fix score --json` detail rows live in `skills/steer-report/audit.md`;
  `TestJSONContractIsStable` pins the contract and
  `TestContractNamesEveryFixWithinItsSizeCeiling` its size. The scenario builder
  `tests/scenarios/scenarios.py` emits the same three documents.
  Step 1 loads `audit.md` only when `interventions[]` holds a row or
  `telemetry.audit_firings` or `telemetry.check_blocks` is above zero, so a change
  to any of those fields changes when the audit runs. A skill names a file of its own as `./<name>`, which
  `TestSkillOwnFileReferencesResolve` resolves.
  `skills/steer/SKILL.md` names only the fields its Pre-flight reads.
  The check tier's share of it lives in `cli/internal/status/checks.go`.
- Refresh queue: `cli/internal/cadence/refresh.go` and
  `skills/steer-report/SKILL.md`. Read version constants at their source.
- Fixture receipts: `cli/internal/claude/fixturestate.go` reads a guard store,
  `cli/internal/evidence/` resolves it into `mine.EvidenceSet`, and
  `cli/internal/gate/mine/guardfix.go` tests each record's frozen ownership
  before it uses one. `gate/mine` may read no store itself. A project-scope
  record reaches a receipt only in the checkout and on the host it froze at
  registration, so a new record field must be stamped in `SteerHookFacts`.
- Firing reading pass: the pass budget in `skills/steer-report/audit.md` and the
  mechanism in `cli/internal/firingaudit/emit.go`. `steer audit show` bounds its own
  output and reports the exact size; the skill carries the running total, so a change
  to `Emission` changes what a pass can subtract. `steer audit queue` counts the whole
  queue before `--limit`, and `Counts.Remaining` is the one wording for what a listing
  left. A receipt keeps the extent read (`InspectedSteps`) apart from the extent the
  lookup reached (`JoinSteps`).
- Continuation lookups: the neutral contract in
  `cli/internal/transcript/continuation.go`, each harness's reader beside its
  adapter, and the dispatch in `cli/internal/firingaudit/continuation.go`.
  A reader returns the granularity it achieved with its source anchors; adding
  one means teaching `Reach` what that harness's locators allow.
  The Stop block readers (`cc.StopBlock`, `codex.StopBlock`) find a block by
  `checks.RunIDLine`, so the block reason's last line changes with them.
- Firing adjudications: the store `cli/internal/adjudication/` and its component
  name in `cli/internal/statestore/Components`. A corpus absent from that list never
  migrates. Its exclusion stream keys a guard row by `mine.RowDigest` and a check block by
  run; a change to how that digest reads a line orphans every recorded guard exclusion. A receipt states its subject: a guard judgment names its firing by the row's
  `firing_id` alone, a check judgment its run by `run_id`, and the identity it was
  made against is stored with it; `gate/mine` reads no receipt store.
- Guard source agreement: `cli/internal/claude/sourcecompare.go` holds the one
  comparison of a store's guard files against the copies a master embeds.
  `MasterCovers` (`cli/internal/claude/sync.go`) wraps it as the refusal sync
  depends on, and `cli/internal/status/status.go` runs it against each harness's
  registered master, so a change to its states changes both. It compares source,
  never the `hook_digest` in `STEER_HOOK_META`: a digest is the generator's claim
  about the bytes it wrote.
- Check tier readings: `cli/internal/checks/state.go` is the one reader of a
  repository's check state, shared by the status snapshot, the refresh queue and
  `steer check list`. Adoption, registration, validation freshness and observed
  execution stay four separate facts.
- Audit row fields: the writer `cli/internal/guard/runtime.py`, the stamps
  `cli/internal/claude/consolidation.go` puts in `STEER_HOOK_META`, the Pi
  payload `cli/internal/pi/bridge_guards.ts` and the reader
  `cli/internal/gate/mine/session.go`. A new field reaches rows only after
  `steer hooks rebuild --scope <scope>`, so every reader tests presence.
- Review addendum: the manifest key in `cli/internal/claude/manifest.go`, the
  gate's HEAD reader `cli/internal/gate/addendum.go` and `mine.SteerAddendumFacts`
  decode one `review_addendum` shape. The stock `prompts/review.md` is never the
  review tier's target.
- Generation behavior: bump `prompt_version` in `skills/steer/SKILL.md`.
  That includes `skills/steer/prompts/routing.md`, which `/steer-report` also
  reads; its rung names are pinned to `cli/internal/tier` by a plugindist test.
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
`// pins existing behaviour` in a test's doc comment exempts it. A test it accepted
is judged again only when its package's test-side files or the checker change
(`.git/revert-test/accepted`), so unpushed commits do not slow later runs. Its per-test
evidence (deciding variant, failure excerpt, diffs, full output) is written to
`.git/revert-test/<key>.json`, and the console names the file. It copies the module
to one scratch path per worktree and holds `.git/revert-test/lock` for the whole run,
so two runs in one worktree are serialized and a blocked run fails naming the lock.
The variant kinds, their caps and the time budget are in the script's docstring; a
run that the budget stops before it finishes records no pass. When no variant can violate the
property a correct test checks, extend the generator (`scripts/revert-test-funcs.go`
finds statement sites) rather than the test.
`./check.sh` also runs `cli/e2e/` (`go test -tags e2e ./e2e/...` from `cli/`): the
built binary as a subprocess in a throwaway home, with `cli/e2e/stubreviewer` linked
on PATH as `agy`, `claude` and `codex`. Its environment is built from nothing, so a
case reads no operator setting. `revert-test.py` runs no tagged test, so each e2e
test carries `// pins existing behaviour`.
`./check-full.sh` adds `scripts/revert-test-verify.py`, which checks that script's
copy, lock and variant generation over fixture repositories, then the slow headless skill-generation
harness that invokes `claude -p`; it is separate from the turn-end gate.

After CLI changes, rebuild the committed platform binaries before committing:

```bash
cd cli
VERSION=0.1.0 ./build.sh
```

The `binary-freshness` check (`scripts/binary-freshness.sh`) fails `./check.sh` while
any committed binary lacks the short hash of the newest commit to `cli/` outside tests,
`testdata/` and `cli/e2e/`. `.github/workflows/check.yml` runs `./check.sh` on Linux and
macOS with full history, which that check needs, and cross-compiles every shipped target.

The model-driven tests under `tests/` need PyYAML in `tests/.venv`. Read the
runner's help for options and select the model explicitly. Focused generation:
`tests/.venv/bin/python3 tests/skill_test.py --test <name> --model <model>`;
the full serial suite uses `tests/run_tests.py --model <model>` with that Python.

A run that starts a real agent (`claude -p`, `codex exec`) goes through
`tests/sandbox.py`: under the operator's home the installed guards block for real
and write firings into the live audit log. The runners use it; a manual run is
`python3 tests/sandbox.py [--codex] -- <command>`. Claude Code needs
`CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` exported, because the sandbox home
holds no login. A run fails if it changed a real Steer or harness file or wrote a
firing to the real audit log; nothing is restored.

After a change to how the runtime renders a block, `tests/block_delivery.py` runs a
real agent per harness against a probe guard and requires the guard's reason and
footer in the agent's recorded tool result. A unit test sees only the master's JSON,
not what a harness passes to the model.

For routing changes, `tests/routing_scenarios.py --model <model>` runs the
`/steer --headless --route-only` and `--route` scenarios in
`tests/test_data/routing.yaml`.

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
  `hookstore`. Project scope anchors at the repository root, and the filesystem
  path, the registration string and the repository-relative path are three
  different forms; see [harnesses and storage](ARCHITECTURE.md#harnesses-and-storage)
  before changing one. A new write into a store goes through `hookstore.WriteFile`,
  which refuses a symlink the repository may carry. Read the registered master
  when diagnosing execution or audit data.
- Stable hook executable: `~/.steer/bin/steer`; `steer bin install` updates it and
  repairs Steer-owned registrations. Reviewer timeout changes need channel
  re-registration to update the harness timeout too.
