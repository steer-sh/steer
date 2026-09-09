# Steer architecture

This is a public repository document. Include contributor-facing architecture
only; keep private development workflows, personal context and internal discussions
out of this file.

This document describes current component boundaries and the invariants that cross
those boundaries. CLAUDE.md supplies task-specific reading triggers. Keep this
file focused on current design, with source/test pointers; decision history belongs
in commits. Read constants and wire schemas from their owning code.

## Distribution and installation

The plugin bundles skills, a launcher, platform binaries and a cadence SessionStart
hook. Guard and commit-gate activation are explicit setup operations.
`skills/steer-setup/SKILL.md` handles reviewer selection and installation;
`skills/steer/SKILL.md` handles guard generation, and `skills/steer-report/SKILL.md`
reads the queue and evidence to propose interventions.

The two marketplace manifests and two root plugin manifests describe one identity:
`.claude-plugin/marketplace.json`, `.agents/plugins/marketplace.json`,
`.claude-plugin/plugin.json`, and `.codex-plugin/plugin.json`.
`cli/internal/plugindist/` pins identity, declared paths and bundled hook wiring.
`scripts/steer` selects `bin/<os>-<arch>/steer` (or `steer.exe`);
`cli/build.sh` builds those artifacts and stamps `internal/version`.
Bundled commands resolve the plugin root through
`${PLUGIN_ROOT:-$CLAUDE_PLUGIN_ROOT}`.

`cli/internal/steerbin/` owns the stable installed executable at
`~/.steer/bin/steer`. `HookCommand` supplies commands to CLI and dashboard
registrations, with a reported PATH fallback if installation fails.
`steer bin install` copies the binary and relinks Steer-owned registrations;
custom wrapper commands retain their ownership. Guard master registration is
separate: project store paths can remain relative, while global and bridge paths
can be absolute. Reviewer flags in older registrations are stripped
when repointing because those flags would override operator settings.

## Harnesses and storage

`cli/internal/harness/` is the registry and registrar contract. It owns harness
identities, capabilities, instruction paths, environment overrides and detection.
Concrete adapters under `internal/claude`, `internal/codex`, `internal/pi`, and
`internal/gate/` own their configuration and event formats.
`STEER_HARNESS` resolves ambiguity when multiple session markers are present.
Pi uses `.pi` in a project and `~/.pi/agent` globally; Pi and Codex share project
`AGENTS.md` but have separate global instruction documents.

`internal/steerpath/` is the common path entry point. Two stores have different
migration ownership:

- `internal/statestore/`: evidence under `~/.steer/state/<component>`.
  Reads may fall back to a populated legacy `~/.claude/state/<component>`.
  `Prepare` copies the component before writing the `.steer-state` marker last;
  `PrepareAll` prepares all components. Writing entry points call `prepareState`
  before resolving paths. Reads leave migration to writers.
- `internal/hookstore/`: project/global guard stores under `.steer/hooks` and
  `~/.steer/hooks`. Legacy `.claude/hooks` is eligible while it has the manifest
  and the neutral store does not. Migration copies manifest-owned guards, the
  master and audit log, landing the manifest last. This migration does not copy
  `fixtures/`; when migrating a legacy store, check fixture availability separately.

Both migrations retain legacy files and preserve files already in the destination.
A retained legacy master can still be registered, so diagnostics read
`Registrar.RegisteredMasterPath` and the audit log beside that master.
Store location alone cannot identify what a harness executes.

## Guards and instructions

One store per scope holds guards, `steer-manifest.json`, the generated
`steer-hooks.py`, fixtures and `steer-audit.jsonl`. Each harness separately
registers the shared master. The registrar contract owns master registration,
instruction synchronization and reading registration back.

`internal/claude/lifecycle.go`, `rules.go` and `sync.go` compose store mutations
with harness updates. These are sequential writes across artifacts and targets,
not one transaction; synchronization and rebuild are retry paths. `SyncTargets` derives `## Steer Rules` from `ManifestRules`;
`internal/instructions/` replaces that managed section while preserving surrounding
prose. Rebuild and removal retries also synchronize it. Handwritten lines inside
the managed section do not survive regeneration.

The manifest has `hooks` and prose-only `rules`. Their ids are mutually exclusive
at both installation boundaries. Rule text is stored so its normalized digest can
be recomputed; duplicate normalized text under another rule id is refused.
A prose edit does not consolidate the master or invalidate unrelated guard tests.
Removing a shared artifact removes its derived rule for every target, including
Pi and Codex's shared project document.

Target drift includes master registration and instruction text, as well as the
store. Explicit harness flags select targets; without a flag, mutations target
installed hook-registering harnesses, with a Claude Code fallback when none are
found. `cmd.currentRegistrars` supplies the target list to commands and dashboard.
The guard master and commit gate have independent registrations.

`internal/tier/` owns the current and legacy tier spellings.
`internal/guardscope/` validates the harness scope of guard-tier records: evidence
eligibility is distinct from installation reach, and a record must name harnesses
with a registered master unless forced. Instruction-tier registration binds a
`rule_id` and requires `not_mechanizable`, explaining why a check or guard cannot
detect the class. `mine.ValidateArgument` validates the argument's presence;
it cannot establish its soundness.

## Guard runtime and fixtures

`internal/guard/runtime.py` is embedded by `claude.generateMasterHook`.
Guard sources are compiled into separate module namespaces, preserving their
source and imports. Module initialization runs with empty stdin and discarded
output, then the runtime calls each guard with a normalized payload.
`guard.RequiredImports` supplies the generated runtime's import contract.

Virtual edits turn MultiEdit hunks and Codex patch file sections into Claude-shaped
Edit/Write calls. Only added/replacement content is presented as changed content;
patch syntax is never presented as a shell command. Guards run in fixed order
over the virtual edits, and the first objection wins. Reply rendering is specific
to Claude Code, Codex or Pi; guards continue using the shared Claude-shaped input
and block contract. Audit rows add harness, root session, thread and repository
provenance. Runtime tests under `internal/guard/` pin this translation.

An installed master embeds the runtime it was built with. Its runtime digest stamp
lets readers distinguish current, stale and unknown builds.
`claude.RebuildMaster` regenerates and synchronizes registrations together;
`steer hooks rebuild` exposes it. A runtime must never rebuild itself from a hook.

`internal/guard/fixture/` stores replayable cases at `fixtures/<hook-id>.json`.
Each case has payload, expected result, optional filesystem context and provenance.
The runner copies the guard and master into a temporary store so synthetic firings
cannot enter the live audit log. Individual guards must pass both allow and block
cases. Master positives distinguish interception by the intended guard, shadowing
by an earlier guard, and failure to block. Shadowing leaves integration unverified.

Fixture bindings cover guard digest, fixture digest, rule hash and master digest.
Consolidation clears tested state before rewriting; lifecycle operations rerun
fixtures afterwards and report failures without preventing removal or repair.
`internal/claude/fixtures.go` owns binding and verification integration.

Fixture mining reconstructs candidates from stored diffs; it does not recover a
historical hook payload. The git unified-diff parser emits candidates per hunk,
skips deletion-only hunks, and checks whether required diff content survived
truncation. `gate.ParseFindings` narrows candidates by finding file. Selection and
allow/block labeling remain the author's work.

## Commit gate and resolver

`internal/gate/` is the review engine; `cc`, `codex`, `pi` and `gitgate` adapt it
to native hooks and git pre-commit. Reviewer processes live under
`internal/gate/reviewers/`; prompts and their pinned digests live under `prompts/`.
The review prompt digest is part of reviewer identity. Ledger rows record both a
legacy instrument string and structured identity dimensions.

`gate.Load` resolves defaults, `~/.steer/config.json`, then environment overrides;
command flags override those values. `internal/settings/` shares setting metadata
and mutation previews between CLI and TUI. `mine.ImpactOf` warns about reviewer
identity changes across live review-corpus records. The observation scorer reports
identity provenance without grading efficacy from it.

Engine timeout and harness hook timeout are separately stored. Registration gives
the hook the review timeout plus a margin; changing settings alone does not update
an existing harness timeout. Native installation couples review and resolver
registration. Guard registration remains separate.

Codex's adapter rejects commits with ambiguous repository selection when its hook
payload lacks a workdir. An absolute repository selection in command text resolves
that ambiguity (`internal/gate/codex/ambiguity.go`). Codex thread identity keys the
review chain while root session identity remains evidence provenance. Review text
is delivered in `additionalContext`; the denial reason supplies a short notice.
Pi's bridge supplies its execution cwd and session metadata directly.

Every decision that does not block carries one receipt to both the agent's context
and the operator (`internal/gate/receipt.go`); no path lets a commit through
silently, because absent agent-facing text is indistinguishable from an absent
gate. Claude Code carries it beside an `allow`, so a clean review keeps its
auto-approval; the other adapters carry it without a decision. The receipt
identifies the commit by repository, subject and diff digest — the hook runs
before a commit hash exists.

`internal/gate/resolve/` joins adjudication rows to harness-neutral `LedgerMsg`
values supplied by each adapter's harvest. The join uses thread id when present,
otherwise session id. `steer commit-gate backfill` previews; `--apply` rewrites the
local host shard with backup and concurrent-append preservation. Stop hooks run
the resolver; Codex also registers SubagentStop, and Pi uses turn_end.
The git pre-commit channel has no session resolver. Pending rows have a grace
period before they count as missing evidence.

## Transcripts and session evidence

`internal/transcript/` defines Session, Event and Adapter. A root session is one
unit; subagent threads carry provenance. Consumers read normalized events and the
`Shell` flag rather than interpreting harness tool names. Adapters own discovery,
streaming, loss counts and the fields the classifier sees.

Codex joins subagent rollouts to their root and drops replayed parent history.
Tagged user messages are primary human turns, with legacy event-message fallback.
Pi session files are trees: the adapter emits only the active leaf's ancestry,
retains history across model-context compaction, and excludes user-run shell
commands. Forked history is deduplicated against the parent when it is available.
Adapter behavior tests cover these rules; fixture replay separately pins each
adapter's declared `Instrument` digest.

`internal/report/scan.go` owns the shared friction classifier. `command.go`
distinguishes executed programs from source-printing commands and extracts causal
failure keys, including traceback exception lines. Parser identity includes these
rules. Session snapshots stamp parser plus adapter identity, so readers can show
which pipeline produced their evidence.

`internal/sessions/` persists `sessions.<host>.jsonl`. Refresh merges newly read
sessions with stored ones, retaining sessions whose source transcript disappeared.
`steer report refresh` writes snapshots; status and dashboard read them.
`internal/evidence/` gathers review rows, session snapshots and guard firings into
one `mine.EvidenceSet` shared by cadence, status and intervention commands.

## Interventions and observations

`internal/gate/mine/registry.go` routes review-findings to chain units and
transcript-friction to root-session units. `ScoreWith` dispatches to the registered
engine. Unsupported corpus/unit records remain unscored.
`cmd/intervention.go` constructs the shared command tree exposed by `steer fix`,
its `intervention` alias and the original `commit-gate mine` path.

Records bind a class regex, corpus, unit, tier, artifact and explanatory prose.
Eligibility is frozen at registration from harness, repository and changed-path
metadata, independently of matched findings. Scope predicates combine with AND;
scope-all is exclusive. Session records cannot use changed-path scope.
A legacy scope migration recomputes the baseline and starts a new after-window;
changing a frozen scope requires a new record. Baseline writers and scorers use
the same eligibility pass.

Both engines emit an observation: counts and window bounds, recurrence, guard
firing activity, coverage and identity provenance. Before/after counts are
uncontrolled observations. Standing instructions and other behavior-changing
inputs are not fully recorded, so these counts cannot isolate an intervention's
contribution. Guard fixture results establish behavior within their tested cases.

Guard firing joins differ by corpus. Session scoring joins hook id, harness and
eligible root session. Chain scoring reports firings over the window period,
without claiming they belong to the class's eligible commits. A missing audit log
is unknown activity; it is distinct from a known zero.

`mine.CoverageGap` and `MissingCoverage` share coverage interpretation among
scorers and readers. `status.ActionFor` orders actions as inspect, repair, observe:
recurrence outranks missing scope and coverage, while a merely pending row or zero
eligible after-units does not itself prescribe repair. Retirement ends tracking;
`mine.Retire` removes no guard, instruction or repository check.

## Cadence, status and controls

`internal/cadence/` coordinates evidence scoring and transcript mining into a
versioned queue. `Refresh` is the pure computation; the refresh command writes
queue, session snapshots and freshness state under the neutral evidence root.
`steer report cadence` is the fail-open SessionStart entry point: it launches a
stale refresh in the background and emits deduplicated operator nudges, including
stale-master notices. The queue carries attention records, candidate pointers,
unscored record ids and corpus sweep summaries. The tier does no semantic
grouping: a transcript cluster key is the user's own wording, so it merges
verbatim repeats only, and candidates are ranked by session spread and capped
rather than filtered on a count that key cannot earn. Naming classes is the
skill's work on both corpora.

`internal/status/` composes the read-only `SystemStatus` for terminal, JSON and
TUI. Shared problem, evidence and formatting helpers keep human summaries aligned;
wire version constants and schema tests own machine compatibility.
`vocabulary_test.go` checks display literals; dynamic stored values also need
translation at rendering boundaries.

Arming distinguishes registered configuration, command resolution from this
process, and observed corpus activity. Resolution is a proxy for the harness's
environment; activity does not identify scope or prove current trust. Readiness
reports detectable setup problems with repairs and keeps trust uncertainty
separate. Machine-local absence describes only the evidence available here.

`internal/tui/` provides Arming, Problems, Guards, Reviewer and Diagnostics panels.
Mutations call shared lifecycle/settings functions and recompose status afterwards.
Problems combines candidates and tracked records; authoring goes through the
skills. Git pre-commit installation remains CLI-only. Shared styles and geometry
tests own wrapping and terminal layout details.

`internal/statusline/` renders the Claude Code status-line row;
`internal/claude/statusline.go` owns registration. An occupied slot requires an
explicit wrap or replace choice. Wrapping preserves the child's command for
removal/restoration and isolates child failures from Steer's own row.
`steer denials` is a separate Claude Code PermissionDenied recorder: it stores
classifier denials without granting a retry or deciding whether a denial was right.
