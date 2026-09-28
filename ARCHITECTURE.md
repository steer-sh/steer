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
`skills/steer/SKILL.md` routes a rule to a tier and handles guard generation and removal,
and `skills/steer-report/SKILL.md` reads the queue and evidence to propose interventions.
Both route through `skills/steer/prompts/routing.md`; `/steer-report` reads it as a
sibling, so the two skills ship in one bundle.

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

Codex keys each hook approval by event, entry index and handler index, and hashes
the handler definition, not the script it runs. A Codex registration therefore
rewrites a Steer handler at its existing entry (`codex.upsertEntry`) and appends
only when none is there: a handler that moves voids its own approval and that of
the handler taking its place, and Codex then skips both until `/hooks`.

`internal/steerpath/` is the common path entry point. Two stores have different
migration ownership:

- `internal/statestore/`: evidence under `~/.steer/state/<component>`.
  Reads may fall back to a populated legacy `~/.claude/state/<component>`.
  `Prepare` copies the component before writing the `.steer-state` marker last;
  `PrepareAll` prepares all components. Writing entry points call `prepareState`
  before resolving paths. Reads leave migration to writers.
- `internal/hookstore/`: project/global guard stores under `.steer/hooks` and
  `~/.steer/hooks`. A project store belongs to a repository: `steerpath.ProjectRoot`
  anchors it at the repository containing the working directory, so a command run
  from a subdirectory reads and writes the same store as one run from the root.
  Three forms of that path are distinct. `HooksDir` is the filesystem path.
  `hookstore.MasterRegistration` is what a registration names: relative to the
  directory holding the harness config, because a harness resolves a project
  registration against that directory. A comparison against a registered value uses
  it on both sides. Each resolver has an `…In`/`…Of` form taking that directory, so
  a registrar handed another checkout resolves that checkout's store rather than
  the process's.
  `steerpath.ProjectHooksRel` is the repository-relative path a git object read or
  another checkout's store is addressed by; it never depends on the working
  directory. Legacy `.claude/hooks` is eligible while it has the manifest
  and the neutral store does not. Migration copies manifest-owned guards, the
  master and audit log, landing the manifest last. This migration does not copy
  `fixtures/`; when migrating a legacy store, check fixture availability separately.
  `Prepare` also keeps a store `.gitignore` that names both evidence paths,
  `steer-audit.jsonl` and `steer-audit-payloads/`, adding whichever a store is
  missing: a project store is committed, and its firing evidence is machine-local.

Both migrations retain legacy files and preserve files already in the destination.
A retained legacy master can still be registered, so diagnostics read
`Registrar.RegisteredMasterPath` and the audit log beside that master.
Store location alone cannot identify what a harness executes.

A third migration crosses harnesses instead of stores. `steer check migrate`
imports a personal check gate: the approval list at
`~/.claude/check-gate-approved.txt` becomes adoption, one `checks.Adopt` per
approved root, so every import validates and runs the oracle in isolation. That
gate's Stop registrations, found by `checks.IsPersonalGateCommand` through each
adapter's `StopHookMatching`, are replaced by the Steer runner only once no
approved repository with a `check.sh` is still unadopted; until then both would
run `check.sh` at every Stop, and the personal hook is the one that must stay.
Nothing changes without `--apply`, and neither the approval list nor the hook
script is deleted.

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

A project manifest is a file a repository carries, so the guard names in it are
untrusted input to every reader that joins one to the store directory.
`hookstore.EntryFileName` is the one validator: a key or a `file` value that is
not a single path element is refused, never repaired, and `LoadManifest` refuses
the whole file rather than dropping the entry. The readers that decode a manifest
or an archived entry without `LoadManifest` — `internal/hookstore`'s migration,
`gate/mine`, `claude.restoreGuard` — call it directly. Reads that copy a guard
into the archive go through the store root, because a plain name can still be a
link out of the store. Status reports a refused store in `store_problems` instead
of an empty inventory.

The manifest has `hooks`, prose-only `rules` and `review_addendum` entries
(`internal/claude/addendum.go`). Ids are mutually exclusive across the three at
every installation boundary. Rule and addendum text is stored so its normalized
digest can be recomputed; duplicate normalized text under another id of the same
kind is refused. Addendum entries derive no harness document; the commit gate
reads them.
A prose edit does not consolidate the master or invalidate unrelated guard tests.
Removing a shared artifact removes its derived rule for every target, including
Pi and Codex's shared project document.

A removal archives the fix before it deletes it. `internal/archive/` writes an
entry to `archive/<id>/<entry>/` in the store that owns the fix, with an
`archive.json` that holds the tier, the manifest entry, the removal time, the
reason and the parts it did not find. `RemoveManagedHook` also stores the guard
file and its fixtures; `RemoveManagedRule` and `RemoveAddendum` store only the
manifest entry, which holds the text. One archive id can hold entries of
different tiers over time, and the entry's tier tells them apart. The entry is written to a
temporary directory and renamed into place, and a failed write stops the removal.
Store readers skip subdirectories, so an archived guard never reaches the master.
Only a caller that proves a catalog copy removes without an archive
(`RemoveOptions.CatalogCopy`, which `presets.DisablePreset` sets for its own
factory entry); a pruned orphan and a team-lockfile extra are archived. A project
archive is committed with the store.

`steer archive list` reads the project hook store, the global hook store and the
project check store. `steer archive restore` selects one entry with
`archive.Select`: `--tier` settles an id that holds more than one tier, and
`--entry` settles a tier that holds more than one entry. `--scope` picks the hook
store, and a project restore also reads the check store. `claude.RestoreArchived`
sends a guard through `InstallManagedHook` with its file, manifest metadata and
fixtures, so the fixtures run again. It sends a rule through `InstallManagedRule`
and a review entry through `InstallAddendum`. `checks.Restore` sends a check
through `checks.Add`, so a check that fails validation stays undeclared. Restore
refuses an id its own tier holds now, because those installers overwrite, and a
guard file that no manifest entry names. The installers refuse an id that another
tier holds. The archive entry stays after a restore, so the artifact reading
still shows the removal.

Target drift includes master registration and instruction text, as well as the
store. Explicit harness flags select targets; without a flag, mutations target
installed hook-registering harnesses, with a Claude Code fallback when none are
found. `cmd.currentRegistrars` supplies the target list to commands and dashboard.

A project store is committed with its master, and a clone carries the harness
registrations only where the author's harness configuration was committed too.
`claude.SyncStore` (`steer hooks sync`) registers the master a store already
holds and rewrites drifted rules sections, writing only the targets that differ
and never the store; it refuses a master whose embedded guard sources differ from
the store's files. `status` reads an installed harness that runs none of a
scope's guards as `detached`, and the SessionStart nudge names it.
The guard master and commit gate have independent registrations. A harness
installed after a mutation receives neither master nor rules until a rebuild, and
a hand edit can remove a rule from a document that received it. A record binds the
manifest text, never the document, so the status snapshot reads each installed
harness's section against the manifest (`claude.TargetRulesPresence`): an
instruction-tier record stays live while its rule is absent from the document the
agent loads.

`internal/tier/` owns the current and legacy tier spellings.
`internal/guardscope/` validates the harness scope of guard-tier records: evidence
eligibility is distinct from installation reach, and a record must name harnesses
with a registered master unless forced. A Codex master whose approval record is
known to be absent counts as unregistered (`guardscope.RunnerUnapproved`): Codex
runs no unapproved hook, while the guard's rule still reaches the agent as text. Instruction-tier registration binds a
`rule_id` and requires `not_mechanizable`, explaining why a check or guard cannot
detect the class. `mine.ValidateArgument` validates the argument's presence;
it cannot establish its soundness. Review-tier registration binds an
`addendum_id` and requires a false-positive class: an entry that suppressed a
defect class would read as success while hiding the defects. A project entry must
be at HEAD with the text the record freezes, because the gate reads project
entries there (`mine.RequireAddendumAtHead`).

## Guard runtime and fixtures

`internal/guard/runtime.py` is embedded by `claude.generateMasterHook`.
Guard sources are compiled into separate module namespaces, preserving their
source and imports. A final bare `main()` statement is dropped before module
initialization, so loading never calls the guard; initialization runs with empty
stdin and discarded output, then the runtime calls each guard with a normalized
payload. A guard that fails to load is skipped and named on stderr, which the Pi
bridge discards. `guard.RequiredImports` supplies the generated runtime's import
contract.

Virtual edits turn MultiEdit hunks and Codex patch file sections into Claude-shaped
Edit/Write calls. A virtual Edit carries removed text as `old_string`; a MultiEdit
hunk also keeps its `replace_all` as sent. A patch section's `old_string` and
`new_string` join its `-` and `+` lines in order across hunks: they are ordered
fragments that may not be contiguous, never search-and-replace operands. A patch
file deletion is an Edit marked `steer_operation: "delete"` with no `old_string`;
the runtime never reads the deleted file. Patch syntax is never presented as a
shell command. Guards run in fixed order
over the virtual edits, and the first objection wins. Reply rendering is specific
to Claude Code, Codex or Pi; guards continue using the shared Claude-shaped input
and block contract. Audit rows add harness, root session, thread and repository
provenance; the repository is the nearest directory holding a `.git` directory
or file, so a worktree names its own root. Runtime tests under `internal/guard/` pin this translation.

A row also identifies what ran and what was attempted. `hook_digest` and
`rule_hash` come from `STEER_HOOK_META`, which consolidation stamps from the
embedded guard source and the manifest entry; `master_build_digest` is the
generated master's own constant. `locators` copies the call identifiers the
payload supplied, by key presence, so an absent identifier is distinguishable
from an empty one; the Pi bridge forwards `toolCallId` and keeps the native call
in `steer_original_call` before it reduces the call to the Claude shape. The
evidence bundle is the payload as it arrived and the virtual edit the guard
objected to. It sits inline under `STEER_EVIDENCE_INLINE_LIMIT` serialized bytes
and otherwise in `steer-audit-payloads/<firing_id>.json`, named by
`evidence_ref`; `evidence_sha256`, `evidence_bytes` and `evidence_complete`
describe either form. Evidence that cannot be retained marks the row incomplete
rather than dropping the firing. Rows an older master wrote carry none of these
fields, so a reader tests presence; coverage starts at the scope's rebuild.

An installed master embeds the runtime it was built with. Its runtime digest stamp
lets readers distinguish current, stale and unknown builds. The master also
carries its own build digest, taken over the generated text with a placeholder in
that constant's place, so a firing names its build without reopening a path a
rebuild can replace underneath it. Consolidation publishes the master through a
temporary file and a rename for the same reason.
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
each Edit carrying the hunk's removed lines as `old_string` beside its added lines,
and checks whether required diff content survived truncation. A deleted file becomes
the runtime's path-only delete Edit: its stored content never enters the payload. `gate.ParseFindings` narrows candidates by finding file. Selection and
allow/block labeling remain the author's work.

## Repository checks

`internal/checks/` owns the check tier. A repository declares its checks in
`.steer/checks/manifest.json` and their replayable cases under
`.steer/checks/cases/`; a clone carries the declarations and never the
authorization to run them. A `script` binding names a repository-relative script.
A `go-test` binding names a package directory and one top-level test that stays in
the ordinary suite. The embedded `guide.md`, printed by `steer check guide`, is
the authoring contract the bundled skills defer to.
`checks.Remove` archives the declaration under `.steer/checks/archive/` and drops
it from the manifest; the detector, the cases and the validation history stay
where they are, so the archive keeps only the declaration.

Validation replays a check's cases in temporary trees and appends the result to
`validations.jsonl` under the machine-local evidence root; a validated run is a
receipt. A receipt binds the detector digest, the cases digest and
`ExecutorVersion`, a replay-semantics constant rather than the build version, so
an upgrade does not stale every receipt. A Go detector is the named test plus the
test-side declarations it statically reaches, written into one generated test
file, so the code replayed is the code digested. Declared `inputs` cover files
that analysis cannot track; production code is outside the detector. A snapshot
case restores a commit pinned under `refs/steer/snapshots/`, overlays the current
detector and applies a defect patch. An incompatible case, an unavailable
environment or a missing snapshot is an invalid case, never an expected outcome,
and the previous receipt stays as history. `FreshnessOf` reads a receipt as
current or stale; stale is a label, and a stale check keeps running.

Execution happens inside `check.sh`. `steer check exec --all` runs the script
bindings in sequence; `steer check go-test` runs the Go suite once with `-json`,
keeps its exit status and reads each bound test from that run. Both append
per-check records to `executions.jsonl`, identified by the parent's
`STEER_CHECK_*` environment (`execution.go`). `checks.RunOracle` is that parent
for `check run`, adoption and Stop. It takes a non-waiting per-repository OS
lock, runs `bash check.sh` from the root under one deadline and appends one
aggregate record to `runs.jsonl`. A declared check with no record under the run
ID gets a `not-executed` record, and a passing `check.sh` that omitted one is an
integration failure rather than a failed assertion. A run the lock refused
records `concurrent_with` and executes nothing. `STEER_CHECK_EVIDENCE_DIR`
isolates the execution and run records of adoption; receipts ignore it.

Adoption is an append-only `adoptions.jsonl`, keyed by the exact resolved root,
where the latest event decides. A linked worktree with no event of its own
inherits its main checkout's adoption (`checks.MainCheckout`), and its
`checks.History` includes the main checkout's validation runs; an unadopt in
the worktree withdraws it alone. Every other store stays keyed by the
worktree's own root. `checks.Adopt` needs a current or freshly
replayed receipt for every declared check and an isolated oracle run that passes
with nothing missing. `checks.Stop` is the harness-neutral Stop runner and
`checks.StopHook` its one adapter, because Claude Code and Codex share the payload
fields and reply shape. It acts on a root Stop in an adopted repository only,
blocks a failure once, and yields when the tree, outcome and per-check results
repeat in the same session. Each run records its disposition and the
working-tree signature it judged. A Stop run also records the payload's
transcript locators by key presence (`transcript_path`, and the turn as Claude
Code's `prompt_id` or Codex's `turn_id`) and `stop_hook_active`; a block records
the reason it sent, whose last line names the run. Neither harness names the
transcript entry a block creates, so that line is the join.
`checks.BlockFollowups` (`followup.go`, printed by `steer check blocks`) reads
those records after each block: the session's next Stop and whether its tree
changed, the next executed run, and for each check that did not pass its next
result and whether its detector changed before the recorded pass. It describes
what followed a block, never whether the block was correct. `steer check
install` registers the runner as its own Stop entry with
`checks.RegisteredHookTimeout()`; commit-gate writers and runner writers each keep
the other's registration. Pi has no Stop runner.

## Commit gate and resolver

`internal/gate/` is the review engine; `cc`, `codex`, `pi` and `gitgate` adapt it
to native hooks and git pre-commit. Reviewer processes live under
`internal/gate/reviewers/`; prompts and their pinned digests live under `prompts/`.
The review prompt digest is part of reviewer identity. Ledger rows record both a
legacy instrument string and structured identity dimensions.
`steer commit-gate test-reviewer` (`gate.TrialReview`) runs one reviewer over a fixed
sample diff through the same prompt and outcome classification, and writes no ledger
row or chain state. Setup runs it because a resolvable executable does not prove that
the reviewer answers; it proves nothing about hook delivery. The Claude Code and Codex
reviewers run with `--no-session-persistence` and `--ephemeral`, so a review adds no
session to the user's history; agy has no such flag.

The stock review prompt is embedded and fixed; the review addendum is the
operator-writable addition to it (`internal/gate/addendum.go`). Each request
loads global entries from disk and project entries from the repository's HEAD —
never the index or working tree, so a commit cannot change its own review. The
entries render into the template's context slot, leaving the prompt digest
unchanged. The addendum's digest joins the instrument string as
`<prompt>+<addendum>` inside the digest segment, because a fifth segment is read
as effort, and rides `instrument_fields` as `addendum_digest`. An absent addendum
reads as known and empty. A settings preview has no repository, so `ImpactOf`
gives it each record's own addendum.

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

The native adapters share one pre-review refusal, `cc.PreflightDeny`, called before
the engine so a refusal starts no reviewer, consumes no firing and leaves a pending
chain intact. It refuses a command whose shell form is ambiguous; a commit that
selects its own paths (pathspecs, `--only`, `--include`), whose landed tree is
neither the index nor the working tree the gate reads; and a bundled
`git add ... && git commit` while a file holds both staged and unstaged edits: that
form is reviewed from the working tree (`cc.DiffSpecFor`), which would show the
reviewers edits the commit does not carry.

Every decision that does not block carries a receipt to both the agent's context
and the operator (`internal/gate/receipt.go`); no path lets a commit through
silently, because absent agent-facing text is indistinguishable from an absent
gate. Claude Code carries it beside an `allow`, so a clean review keeps its
auto-approval; the other adapters carry it without a decision. Both copies carry
one sentence. The agent's copy identifies the commit by repository, subject,
firing and diff digest — the hook runs before a commit hash exists; the
operator's names the repository and subject. A skipped commit (prose only, or a raw diff under
`MinDiffChars`) gets a receipt that names the reason and rules nothing; only a
repository git cannot read stays silent.

`internal/gate/resolve/` joins adjudication rows to harness-neutral `LedgerMsg`
values supplied by each adapter's harvest. The join uses thread id when present,
otherwise session id. The gate gives each chain an ID (`ChainState.ChainID`),
stamps it as `chain_id` on the chain's rows and renders it into the ledger header;
a header ID joins only the chain carrying it, across sessions and past the chain
bound. A chain started before IDs keeps none until it ends. Without an ID, a chain
ledger landing after the next chain started is claimed only by the subject in its
header, and only when exactly one chain of that
thread that was handed findings carries the subject on any of its rows; a row
with no thread never takes that path. A chain whose rows carry only an adapter's
placeholder subject (`gate.PlaceholderSubject`) has no subject to contradict: for
a row with a thread, a header naming no other chain handed findings takes the
chain bound. A miss records why in `ledger_miss` (the
`Miss*` constants in `resolve.go`); a recheck refreshes it and a link removes it.
`steer commit-gate backfill` previews; `--apply` rewrites the
local host shard with backup (the ten newest are kept) and concurrent-append
preservation. `--rejoin` re-judges the links the resolver made (a row with
`ledger_ts`; a hook-time capture has none) under the current rules, and keeps a
link with no ledger left to read. Stop hooks run
the resolver; Codex also registers SubagentStop, and Pi uses turn_end.
The git pre-commit channel has no session resolver. Pending rows have a grace
period before they count as missing evidence. The hooks' `--if-due` form harvests
again only when a row was written or a miss deadline passed since the last applied
scan (recorded in `backfill.scanned`), or after `rescanAfter`; a run without it,
including the dashboard's, always harvests.

Each row's transcript prefix is stored once per session under `prefixes/`: the
session file grows by appended gzip members, and a row names its slice with
`prefix_lines` and `prefix_sha`. Read it through `gate.ReadPrefix`, which also
resolves rows written before the merge; `scripts/merge-prefixes.py` converts a
machine's older per-firing files.

## Transcripts and session evidence

`internal/transcript/` defines Session, Event and Adapter. A root session is one
unit; subagent threads carry provenance. Consumers read normalized events and the
`Shell` flag rather than interpreting harness tool names. Adapters own discovery,
streaming, loss counts and the fields the classifier sees.

Codex joins subagent rollouts to their root and drops replayed parent history.
Tagged user messages are primary human turns, with legacy event-message fallback.
A headless run's user turns (Claude Code `entrypoint: sdk-cli`, a `codex exec`
rollout) are `Meta`: a program wrote them, such as a reviewer prompt or an agent
consulting another agent.
Pi session files are trees: the adapter emits only the active leaf's ancestry,
retains history across model-context compaction, and excludes user-run shell
commands. Forked history is deduplicated against the parent when it is available.
Adapter behavior tests cover these rules; fixture replay separately pins each
adapter's declared `Instrument` digest.

`internal/report/scope.go` owns transcript selection for every scan: all projects,
the working directory, or one checkout. `Scope.Filter` builds the `transcript.Filter`
and `Filter.Selector` applies it inside each adapter's `Discover`, so a report and the
session corpus never select different sessions. A repository scope resolves a
session's launch directory through `checks.RepoRootOf` then `checks.RepoIdentity` and
compares canonical roots exactly, which excludes a sibling worktree, a nested
repository and a submodule under the selected checkout. An adapter reads the launch
directory at discovery, before the time window applies: Codex and Pi take it from the
header, and Claude Code reads the first entry of the file because its directory name
is munged.

`internal/report/scan.go` owns the shared friction classifier. `command.go`
distinguishes executed programs from source-printing commands and extracts causal
failure keys, including traceback exception lines. The correction matcher removes
`<pasted_content>` blocks first, because pasted text is not what the person said.
Parser identity includes these rules. Session snapshots stamp parser plus adapter identity, so readers can show
which pipeline produced their evidence.

`steer report` stores the whole report, every occurrence of every cluster, as
`report.json` in the steer-report state directory under a content-derived
`report_id`, and prints the first page of an index: coverage, a summary per signal
and per instruction document, and one row per correction and rejection cluster.
`steer report show` reads the stored report and never scans: later index pages, one
signal's clusters filtered by `--match`, or the occurrences and directives of named
items. Each output is one page within a byte bound and holds at least one row, so
paging drops no evidence; `show` refuses a `--report` id that a later scan replaced.
The refresh queue reads `report.Generate` in memory, never the stored report.

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

Repository scope has two predicates. The basename regex keeps its original
semantics for existing records; the exact repository root introduced with scope
version 3 separates checkouts that share a basename. Chains preserve the whole
root their rows recorded beside the basename; a session carries the harness's
munged project key, and the two compare through that key. A unit whose evidence
names no repository stays unknown coverage.

A check-tier record binds one check: check id, binding kind, resolved
repository, detector digest, current validation receipt, adoption and registered
harnesses. `internal/checkscope/` freezes those facts, because
`internal/gate/mine` may import no adapter; `mine` owns the contract they must
satisfy and the repository-root scope rule. A record written before the binding
existed stays readable as unbound with unavailable execution attribution: its
target names a script, and identity is never inferred from it.

Both engines emit an observation: counts and window bounds, recurrence, guard
firing activity, coverage and identity provenance. Before/after counts are
uncontrolled observations. Standing instructions and other behavior-changing
inputs are not fully recorded, so these counts cannot isolate an intervention's
contribution. Guard fixture results establish behavior within their tested cases.

Guard firing joins differ by corpus. Session scoring joins hook id, harness and
eligible root session. Chain scoring reports firings over the window period,
without claiming they belong to the class's eligible commits. A missing audit log
is unknown activity; it is distinct from a known zero.

A check-tier record's observation carries its check's executions the same two
ways: `evidence.Gather` reads every repository's live check streams through
`checks.ReadLiveEvidence`, and `mine` joins them on check id and exact root.
Session scoring joins a Stop run's session and harness; chain scoring counts the
window period. Each count is by outcome, with the failures whose run blocked a
turn and the runs of a detector other than the registered one. The streams are
this machine's only, while review history can hold other machines' commits. A
bound record whose repository has no check evidence here is unknown activity and
missing coverage; an unbound record's attribution is unavailable. The binding's
`check_host` is the `gate.HostID` that registered the fix: when it names another
host and this machine has no evidence, the activity is unknown but no coverage is
missing here. A binding without it keeps the missing-coverage reading.

`mine.MissingCoverage` and `UnitsExcluded` share coverage interpretation among
scorers and readers. `status.ActionFor` orders actions as inspect, repair, observe:
recurrence outranks missing scope and coverage, while zero eligible after-units or a
unit the window could not count does not itself prescribe repair. An unknown scope,
a session with no project, and a ledger cause (`pending`, `ledger_missed`,
`ledger_overdue`) leave the unit out of the counts and are disclosed; the repair for
overdue local rows is the status entry for `Posture.Unresolved`, not the record. Retirement ends tracking;
`mine.Retire` removes no guard, instruction or repository check.

`cadence.ArtifactOf` reads where the fix a record binds stands now, against the
facts registration froze: a guard's or rule's text digest in its store, a review
entry where the commit gate reads it (the global store, the project store at
HEAD), and a check's declaration and detector digest in its bound repository. It
sits beside the observation and never enters it. The rules presence reading
compares a document with the current manifest, so a removed rule synced out of
every document leaves no drift; only this reading reports it absent. A project
store resolves from the working directory, so registration stamps the entry's
repository (`<prefix>_repo`) and the reader never matches a project entry against
another checkout. `status` and `cadence` run no git command: the caller loads the
gate's addendum and passes it in.

A fix that reads `present` or `changed` also carries `removed` when its owning
store's archive holds an entry of the same tier and id with a removal after the
record's split, compared in whole seconds. The state keeps its own meaning, so a
removal and a text change can both show. A restore turns an `absent` reading back
into `present`; `removed` keeps the gap visible, and the queue carries the record
as it carries an absent fix. A project review entry's archive is read on disk, not
at HEAD, so a removal that never reached HEAD can still show.

## Firing audits

`internal/firingaudit/` reads the guard audit stream for judging, where
`mine.LoadFirings` reads it for counting: every field the master wrote, the
locators by key presence, and the bundle held inline or in a sidecar. Both
deduplicate on the same key. `Load` stamps each row with the store it came from,
which resolves the row's sidecar and names the master that recorded it.

`Reach` decides what join a row's own locators allow before a transcript is
opened. Claude Code reaches the blocked call: `cc.Continuation` finds the
`tool_result` carrying `tool_use_id` and walks `parentUuid` forward over every
descendant in file order, so parallel tool results and attachments stay on it. A
second assistant or human child of one entry is a rewind branch, which it counts
and does not follow. Codex and Pi have no lookup, and
each says so in its own terms rather than reading as missing evidence.

`internal/adjudication/` holds one append-only receipt per judged block. A
receipt records the join granularity, its transcript anchors and its extent
apart from whether the continuation was read, because block correctness and
continuation are separate axes and a missing continuation settles neither.
`Append` refuses a receipt reporting an inspected continuation at a join that
reached none. A receipt states its subject: a guard firing, named by `firing_id`
with one judgment, or a blocked check run, named by `run_id` with one reading per
failure the run reported (a failing check, `check.sh` itself, or an integration
failure), each with its own validity, attribution and continuation outcome. A line
without a subject predates check receipts and reads as a guard. `Latest` takes the
subject, because a run identifier can fall back to decimal and no reader can tell
the two apart by form. `firingaudit.Record` resolves the row and the continuation itself
and copies the implementation stamps, so a caller authors no identity.

A check case is one Stop block: `firingaudit.CheckCases` assembles it from every
repository's live runs and executions and `checks.LiveBlockFollowups`, which
follows each repository apart. Its failures are each check whose execution
failed, errored, timed out or produced no result, `check.sh` itself, and an
integration failure; a check the run never reached asserted nothing and is not
one. `check.sh`'s own failure is required reading only when no declared check
accounts for it. `steer check blocks` stays the facts view of one repository.

A check case is read at `JoinEntry`: no Stop payload names the entry its block
creates, so `cc.StopBlock` and `codex.StopBlock` find it by
`checks.RunIDLine`, the reason's last line, matched as a whole line and narrowed
by the payload's turn key. Each returns the turn before the block, back to its
prompt and bounded by `ContextSteps`, and the continuation after it; a later
block in the same turn is a `hook_feedback` step, and `Matches` counts repeated
entries. Both harnesses' readers, guard and Stop alike, end a reading
`interrupted` where the harness recorded that the person stopped the turn (Codex
`turn_aborted`, Claude Code's `[Request interrupted by user` text).
`CheckCase.Reach` reports why a case reaches nothing (a run without the
line, no transcript path, a transcript gone, a harness with no Stop runner).

A check failure's reading applies per detector: `adjudication.DetectorApplies`
compares the execution's `detector_digest` with `checks.InstalledDetectors`, read
once per repository. Beside current, historic and unknown, a failure with no
detector of its own (`check.sh`, wiring) is `unmanaged`, and one whose repository
or detector does not read now is `unavailable`; a check no longer declared is
historic.

`Queue` offers unread rows of the installed guard, plus a judged row whose
continuation nobody read while that transcript is still on this machine. The
judgment's value queues nothing. Check blocks share the queue under the same
revisit rule, in one time order (`firingaudit.Earlier`), whatever their
failures' applicability, which each item carries; `--subject` narrows it and
`--hook` lists guards only. `show` and `record` take a firing or a run
identifier (`--subject` settles one both carry); `ShowCheck` adds the records'
follow-up and `FitCheck` withholds prior readings, then continuation from the
tail, then context from the oldest end, never the run or its failures.
`RecordCheck` refuses a failure the run did not report and an unread required
one, and copies each failure's outcome and detector digest from its execution. `cmd/audit.go` groups rows per store and hook
name, because two stores can hold a guard of the same name and their rows are
not interchangeable; `claude.ReadMasterIdentity` reads each store's registered
master for the installed stamps.

A row that records no real work, such as a block a test run caused under the
operator's guard store, is retired by an exclusion rather than removed: the audit
stream stays append-only. `adjudication/exclusion.go` holds the stream beside the
receipts, keyed by `mine.RowDigest`, the digest of the row's own line, because a
legacy row carries no other identity. Both loaders stamp that digest. The queue,
`Cover` and `evidence.Gather` read the same `Excluded` set; `gate/mine` reads
none. An exclusion states its subject: a check.sh Stop block is retired by run
(`adjudication.ExcludedRuns`, `firingaudit.CorrectCheck`), and the queue, check
coverage, contest counts and `evidence.CheckRuns` leave it out while
`runs.jsonl`, `executions.jsonl` and `steer check blocks` keep it. `status.newestFiring` keeps excluded rows, because a test block still shows
that the master executed.

A blocked agent contests a block it holds wrong for its task with `steer contest`.
The runtime allocates one firing identifier per block and puts it in both the row
and the footer, so the contest names a row the agent cannot otherwise see. The
footer offers the contest first and names removal as the user's decision, with
the scope that holds the guard; its `steer hook: NAME · ` prefix is the key
`report.hookBlockSnippet` cuts at. `adjudication/contest.go` holds the stream beside
the receipts. A contest is a complaint, never a receipt: no coverage or queue
reader counts it, it changes no enforcement, and `firingaudit.Contest` copies the
row's identity, or records the contest unmatched when no store read holds the row.
A row's `contest_protocol` says which footer the agent saw; a row without it
predates the contest.
`firingaudit.Contests` is the one reader, behind `steer audit contests`. It joins
the stream to the rows on disk now, per guard and store, and counts distinct
firing identifiers: contested, unread and not offered are separate counts, and a
contest of an excluded row or of no row read here is listed apart from them.

A check.sh Stop block offers the same contest by run: the reason's line above
`checks.RunIDLine` names `steer contest --run RUN_ID`, and the run records
`contest_protocol` as a row does. A contest states its subject as a receipt does
(a legacy line is a guard contest), `adjudication.Contested` takes the subject,
and `firingaudit.Contests` reports check blocks per repository with the same
distinct counts.

## Cadence, status and controls

`internal/cadence/` coordinates evidence scoring and transcript mining into a
versioned queue. `Refresh` is the pure computation; the refresh command writes
queue, session snapshots and freshness state under the neutral evidence root.
`steer report cadence` is the fail-open SessionStart entry point: it launches a
stale refresh in the background and emits deduplicated operator nudges, including
stale-master and drifted-rules notices, each once per distinct condition. The queue carries attention records, candidate pointers,
unscored record ids and corpus sweep summaries. The tier does no semantic
grouping: a transcript cluster key is the user's own wording, so it merges
verbatim repeats only, and a count over such a key measures phrasings rather than
recurrences of one problem. A corpus therefore contributes one candidate pointer
sizing its ungrouped pile, never one row per item, and carries a few
strength-ranked examples for a reader. Naming classes is the skill's work on both
corpora, over the full dumps.

The queue also carries the open steps in the repository the refresh ran in. The
deterministic tier reads the check stores only, so no harness adapter enters it;
the runner's registration reaches a reader through the status snapshot. A stale
receipt is not an open step: a stale check keeps running.

`internal/status/` composes the read-only `SystemStatus` for the TUI; the terminal
renders only its readiness answer (`status.ReadinessOf`) and a guard store it could
not read.
`--json` is a projection of it (`status/json.go`): one summary row per record, whose
observation `steer fix score --json` carries under the same id, guards and
presets from both scopes, and the readiness answer as `posture.readiness`. The TUI lists one scope because it mutates that scope.
Shared problem, evidence and formatting helpers keep human summaries aligned;
wire version constants and schema tests own machine compatibility.
`vocabulary_test.go` checks display literals; dynamic stored values also need
translation at rendering boundaries.

The check tier is one section of that snapshot, read through
`checks.StateOf`, the single reader of a repository's check state that `steer
check list` and the queue share. A repository's declaration, this machine's
adoption, the runner's registration and an observed execution are four separate
readings, and none of them substitutes for another: an aggregate `check.sh`
outcome attributes no result to a particular assertion, and a run the repository
lock turned away never stands in for the result of the run that held it.

Arming distinguishes registered configuration, command resolution from this
process, and observed corpus activity. Resolution is a proxy for the harness's
environment; activity does not identify scope or prove current trust. Readiness
reports detectable setup problems with repairs and keeps trust uncertainty
separate. A resolver problem is a step beside the reviewing answer, never a reason
to withhold it; one working resolver reads every harness's ledger. Machine-local absence describes only the evidence available here.

`internal/tui/` provides Arming, Problems, Fixes, Reviewer and Diagnostics panels.
Mutations call shared lifecycle/settings functions and recompose status afterwards.
A fact appears on the panel that acts on it: waiting review outcomes on Arming,
the repository's checks and their last run on Fixes. Diagnostics holds the
issues that ask for a step, the history counts and the technical block.
Problems lists tracked records first, then the candidate pointers: a tracked row
offers a key that acts on it and a pointer row offers only the handoff. Authoring
goes through the skills. Fixes lists guards, rules, review prompt entries and the repository's
checks, each with its own state, after any maintenance a detected issue asks for;
the built-in guard catalog and the Archived section open from a row that counts
them. `fixRows` returns rows in render order, because the cursor walks that list. A project addendum entry is read on disk and at
HEAD through `status.AddendumInventory`, which `steer commit-gate addendum list`
shares, so an entry removed on disk but still committed stays listed. Every fix
except a built-in guard archives through its tier's remove function; a built-in
guard turns off, which writes no archive. The Archived section shows the newest
entry of each tier and id per store and restores it through the same code as
`steer archive restore`. Check adoption, replay and restore run as background
commands; the check runner's registration belongs to Arming. Git pre-commit installation remains CLI-only. Shared styles and geometry
tests own wrapping and terminal layout details.

`internal/statusline/` renders the Claude Code status-line row;
`internal/claude/statusline.go` owns registration. An occupied slot requires an
explicit wrap or replace choice. Wrapping preserves the child's command for
removal/restoration and isolates child failures from Steer's own row.
