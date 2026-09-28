---
name: steer-report
description: Audits the fixes steer already installed, then mines Claude Code and Codex transcripts and the commit-review ledger for new fix candidates backed by evidence — repository checks, guards, instruction rules, or review prompt entries. Use for steer onboarding ("what hooks should I create?"), periodic review of installed hooks and fixes, or when the user asks to mine usage history for candidates.
user-invocable: true
compatibility: Requires the steer CLI (report, commit-gate and check subcommands) on PATH or bundled under scripts/. Reads transcripts from ~/.claude/projects and $CODEX_HOME/sessions, and, when present, the commit-review ledger under ~/.steer/state/commit-review (~/.claude/state/commit-review on a machine that has not yet migrated).
allowed-tools: Bash(steer *), Read, Edit, Write
---

# Steer Report — audit installed fixes, then mine for new ones

This skill closes the friction loop. It decides two things, in this order:

1. **Is a problem the loop already answered still happening?** A class that came back gets reworked or removed.
2. **Which new patterns recur often enough to deserve a hook?** Both corpora feed this — transcript friction and commit-review findings.

The audit comes first on purpose. Proposing new hooks is the interesting half, and skipping the audit is how a log of interventions turns into a graveyard of unmeasured good intentions.

Two corpora feed this skill, and either may be empty on a given machine:

- **Transcript friction** — repeated corrections, rejected tool calls, recurring lint/test failures, and instruction-file rules (CLAUDE.md, AGENTS.md) not yet enforced. Mined by `steer report` across every harness with transcripts on this machine.
- **Commit-review findings** — defects the commit-review gate found and adjudicated. Mined by `steer commit-gate mine`. Absent when there is no commit-review ledger on this machine.

An empty corpus produces one line, never an error. An adopter with no commit-review ledger still gets the transcript half — the cold-start front door.

## Example Invocations

```
/steer-report
/steer-report --days 7
/steer-report --project-only
/steer-report --repo-only
```

## Step 0: Fresh-context gate (interactive mode only)

The analysis reads a sizable digest and a finding dump, then reasons over both. If this conversation already contains substantial prior work — more than a few turns, or a large amount of loaded code and discussion — warn before proceeding:

> "A usage report works best in a fresh session: the analysis gets the full context window, and cost is lower because each step doesn't re-pay for this conversation's history. I recommend running `/steer-report` as the first message of a new session. Proceed here anyway?"

Respect the user's choice. If the conversation is fresh, skip the warning and proceed.

## Step 1: Snapshot the loop, refresh the queue, then audit

A deterministic tier runs this loop's mechanical half — scoring, corpus sweeps, threshold checks — with no model. It may have run on a schedule already; run it again now, synchronously, so you never read a stale queue. It takes seconds and spends no tokens. Run it first, so the snapshot and the scores below read the session history it refreshed.

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" report refresh --days 30
```

Then read the whole loop's posture and full class inventory in one snapshot, and
each live record's detail beside it:

```bash
"${STEER_BIN}" status --json > "${TMPDIR:-/tmp}/steer-status.json"
"${STEER_BIN}" fix score --json > "${TMPDIR:-/tmp}/steer-score.json"
```

Read each file **once**, with the Read tool, to its end. The `interventions[]`
array of the snapshot is the **full** inventory — one summary row per live class
across every tier. This is your dedup source: when you cluster new candidates in
Step 3, drop any class already here. A class in this list is already decided, so a
new hook for it is a rework of that record. The schema is documented in "The
`steer status --json` contract" below.

The score file is an array with one row per live record, carrying its
`observation`, its coverage maps, and `reason` / `reopen` on a refused record.
Join it to `interventions[]` by exact `id`, never by position. A summary row with
no score row, or a score row with no summary row, is incomplete evidence: name the
id and draw no audit conclusion for it. Fail soft — if a command errors, say so in
one line, continue, and read `audit.md` anyway: without a snapshot, nothing shows
that there is no fix to audit.

Then Read the queue **once**: `~/.steer/state/steer-report/queue.json` (`version: 15`). It holds:

- `interventions[]` — full contract records the tier flagged as **asking for a reader**: the class recurred after the split, an absent guard audit log or missing check evidence left the fix's activity unread, the record carries no frozen scope, the fix it binds is `absent` or `unreadable`, or the fix is in place but carries `artifact.removed`. These carry a `match`; they are the audit's action list, pre-filtered. Each carries `problem` and `intervention` — the record's own explanation of what goes wrong and what it does about it, authored when the class was found. Lead your report with those rather than with the class name or the regex, and correct one that no longer describes the evidence with `steer fix describe ID`, which changes no window. Each also carries the conditions the counts were taken under: `corpus` and `unit` (`review-findings` counts by `chain`, `transcript-friction` by `session` — never compare the two rates), `signal_kind` on a session-unit record, `scope` (the frozen eligibility rule, `null` on a legacy record), `before_coverage` / `after_coverage` (`total`, `eligible`, `ineligible`, `unknown`, `unknown_scope`, `unknown_evidence`, `pending`, `ledger_missed`, `ledger_overdue`, `partial_exposure`), `observation` — the seven field blocks documented in `audit.md` — and `artifact`, where the fix the record binds stands now.
- `unscored[]` — ids of records no engine reads. Report their existence; do not read a result into them.
- `candidates[]` — **one row per corpus**, each pointing at a pile of evidence no problem covers yet. The transcript row carries `ungrouped_clusters`; the review row carries `undecided_chains` and a `dump` command. The transcript row also carries a few `examples` — the strongest cluster keys, for a reader glancing at the row, never the pile itself; the review row carries none, because its `dump` command prints the findings in full. A row has **no** `match` and no title: grouping the pile and authoring the regex are your job, below. Read the full dumps for the pile; the row only sizes it.
- `corpora[]` — one row per corpus (`review-findings`, `transcript-friction`) with `present` and `size`. Relay this sweep to the user in one sentence. When `review-findings` is absent or empty, a second sentence says what comes next. Read that from `posture.readiness`, never from a `commit_gate[]` row's `effective`, which holds before Codex trusts the hook. If `readiness.reviewing` is true, the commit gate reviews each commit from now on, and a later `/steer-report` can mine those reviews for problems. If it is false, no review history collects yet: relay `readiness.headline`, each `steps[]` entry with its `fix`, and each `unverified[]` line. A Codex line clears when the user approves the hook in Codex's `/hooks`. When no commit gate is registered, `/steer-setup` arms one.
- `checks[]` — the open steps in the repository the refresh ran in, each with `kind`, `repo`, an optional `check_id`, a `detail` and the `fix` command that clears it. `not-adopted` is a repository declaring checks that this machine never authorized to run at the end of a turn (`steer check adopt`). `never-validated` is a declared check whose cases were never replayed here, so nothing shows it fails on the problem it names (`steer check test <id>`). `integration-failure` is a passing `check.sh` that ran no declared check — the script's wiring, never a failed assertion (`steer check init` prints the managed line to add). A **stale** receipt is deliberately not here: a stale check keeps running, and `steer status --json` carries the label. These are states of a declaration, not recurrence measured over a window, so they carry no count and never enter a before/after reading.

The queue tells you what is pending. The full dumps below are the raw material you cluster over. The score file holds every live record for the complete audit — the queue lists only the ones asking for a reader, not the quiet ones worth showing too. When the score file is `[]`, this machine records no fix. Guard evidence is a separate stream from review history, so an absent ledger says nothing about what the installed guards blocked: the rule under "The audit lives in `audit.md`" decides whether the audit runs.

`steer fix` is the fix command set — `match`, `register`, `reject`, `scope`, `score`,
`list`, `describe`, `retire`. `steer intervention` is the same set under its former
name and still works, as does `steer commit-gate mine`; `findings`, which dumps the
review corpus rather than managing a fix, lives only under the latter.

Three kinds of evidence answer three different questions, and none of them establishes that a fix works:

- **Stored fixtures.** A current passing result establishes that the stored cases produced their expected allow and block outcomes, under the recorded guard, rule, fixtures and master (`steer fix mine`, `steer test <hook>`). It establishes no field reach.
- **Audited firings.** Reading a blocked action in its original task context supports a judgment about that one attempt: the action would have produced the failure the rule names, or the action was acceptable work, or the evidence does not settle it. These judgments establish no rate.
- **Field counts.** A count reports matches to the recorded problem over eligible work. Read the matches before you call one a recurrence. A count attributes neither a recurrence nor its absence to the fix.

Each record reports before/after incidence over its own unit — the fraction of reviewed commits on which the class appeared for a `review-findings` record, or the fraction of root sessions carrying it for a `transcript-friction` one — split at the install timestamp. **The comparison is uncontrolled and the record makes no efficacy claim.** An agent's standing instruction set — CLAUDE.md, AGENTS.md, imported globals, skills, hooks — can affect its behaviour, and no chain and no session records it, a person edits it by hand between sessions, and an instruction-tier fix edits it directly. So a fall between the two windows is a fact about the corpus and never a fact about the fix.

### The audit lives in `audit.md`

Decide this from the snapshot, never from what you expect to find. **When
`interventions[]` holds a row, or `telemetry.audit_firings` or
`telemetry.check_blocks` is above zero, Read `./audit.md` from this skill's
directory now, and finish its audit before Step 2.** It holds how to read each
record's fields, the contests blocked agents filed, the sampled reading of recorded
guard and check.sh blocks, the repair of a block judged wrong, and the field
reference for an `interventions[]` row.

When none holds, this machine has no fix to audit and no recorded block to read.
Say so in one line and go to Step 2:

> No fixes to audit on this machine.

This is the report's one line about absent fixes. A failed snapshot is not this
case: the fail-soft rule above reads `audit.md` anyway.

## Step 2: Gather new evidence from both corpora

### 2a. Transcript friction

Prefer the launcher bundled with the plugin (`PLUGIN_ROOT` on Codex, `CLAUDE_PLUGIN_ROOT` on Claude Code), and fall back to `steer` on PATH when the plugin root is not set. Redirect the digest to a temp file so it enters context exactly once — via Read, not via command output. Run a **single command**:

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" report --days 30 > "${TMPDIR:-/tmp}/steer-digest.json"
```

Then Read `${TMPDIR:-/tmp}/steer-digest.json` **once**. Do not cat the file and do not re-read it later — everything needed for analysis is in that single read.

All projects and every harness are scanned by default (cross-project, cross-harness habits are the strongest candidates). Pass through any flags the user supplied (`--days N`, `--project-only`, `--repo-only`, `--harness claude-code|codex`, `--max-digest-bytes N`). `--project-only` reads the working directory alone; `--repo-only` reads every session launched anywhere inside the current checkout, and the two are mutually exclusive.

The digest — not the queue — is the transcript corpus. The queue carries one row saying how many correction clusters are ungrouped; the clusters themselves are here. Most hold a single occurrence, because a cluster key is the user's own wording and merges verbatim repeats and nothing else: two complaints about one habit, phrased differently, arrive as two clusters. A cluster is therefore never a problem and its `count` is never a recurrence. Merging them into classes is your job, not the tier's.

Digest schema (`digest_version: 3`):

- `stats` — coverage: `sessions_scanned`, `threads_scanned`, `bytes_scanned`, `clusters_found`, `clusters_emitted`, `truncated`. **Always relay coverage to the user in one sentence.** If `truncated` is true, say so and suggest narrowing (`--days 7`) or raising `--max-digest-bytes`.
- `stats.harnesses[]` — per harness: `harness`, `files_scanned`, `sessions_scanned`, `threads_scanned`, `bytes_scanned`, `entries_scanned`, `entries_skipped`. A harness missing from this list was never reached; a harness present with zeros has no evidence on this machine. Say which when a harness contributes nothing.
- The session is the unit and a subagent thread is provenance under its root session. `threads_scanned` reports how many threads rode along; it is never added to `sessions_scanned`.
- `instruction_files[]` — per instruction document (CLAUDE.md or AGENTS.md), tagged with its `harness`: `steer_rules` (every line of the `## Steer Rules` section), `guard_rules` (the subset a guard in that scope enforces) and `directives` (standing instructions with their section heading, NOT yet enforced). A guard writes its rule into the section and so does an instruction rule, so a `steer_rules` line absent from `guard_rules` is an instruction rule: the loop decided on it, and nothing enforces it.
- `signals.user_corrections[]` — heuristic correction candidates: user turns matching correction language. A match is not a confirmed correction, so read the examples before you count one as evidence, and do not treat a missing cluster as proof that no correction happened. Each cluster: `pattern`, `count`, `sessions` (distinct sessions), `first_seen`, `last_seen`, `examples` (up to 3 raw snippets).
- `signals.user_rejections[]` — tool calls the user declined at the permission prompt (pattern is `Tool: target`). Only Claude Code's decline is recognized, so an absent Codex or Pi rejection is no reading.
- `signals.tool_failures[]` — recurring lint/test/compile failures from shell-tool output, whichever harness ran the shell.
- `signals.hook_blocks[]` — operations already blocked by existing hooks. These are enforcement *working*; use them only to avoid suggesting duplicates.

The digest is size-capped: the CLI evicts the lowest-scoring clusters to fit, and reports honesty in `stats`.

### The `steer status --json` contract (`status_version: 28`)

Step 1 reads this snapshot for posture and the full class inventory. The unit is
the failure class and what the corpus recorded about it, not the hook count. This
and the `interventions[]` row fields in `audit.md` are the one copy of the schema
in the bundled skills; change them with `cli/internal/status/status.go`. Fields:

**Read every count here as an observation.** The windows are uncontrolled: the
agent's standing instruction set can affect its behaviour, no chain and no
session records it, a person edits it by hand between sessions, and an
instruction-tier fix edits it directly. So the snapshot reports hits,
eligible units, window bounds, coverage, guard firings and the identities that
counted each window, and it reports no efficacy verdict. The intervention's
contribution to any change in these counts is unresolved. v11 removes `verdict`,
`p_value`, `effect`, `required_after`, `confounds`, `composition`,
`instrument_drift`, `firings_applicable`, `attribution` and `chain_rate`, and adds
`observation`. v12 adds `checks`: the repository check tier, whose registration,
authorization and observed execution are three readings and not one. v13 adds
`observation.checks`: a check-tier fix's executions in its window, counted on
this machine only. v16 adds `observation.checks.registered_host` and
`other_host`: a fix registered on another host is not missing coverage here.
v17 adds `artifact`: the fix a record binds and where it stands now. v18 adds
`rules` and `addenda`: the instruction and review entries each store holds.
v19 adds `posture.masters[].state` `detached` and `store`: an installed harness
that runs none of a scope's guards. v20 adds `artifact.removed`: a fix in place
now that its store's archive records as removed after the record's split. v21
removes `posture.denials`, `telemetry.denials_rows` and the `auto-denials` corpus.
v25 adds `observation.fixtures`: what this machine knows about the stored
cases behind a guard-tier fix.
v22 adds coverage `ledger_missed` and `ledger_overdue`: a missing ledger is
disclosed and no longer puts a record under repair. v23 does the same for an
`unknown_scope` unit and a session with no project. v24 adds `store_problems`:
a guard store whose manifest a reader refused, so an unreadable store no longer
reads as a store holding nothing. v26 adds `posture.masters[].sources` and
`store_sources`: the guard source a runner carries, read against the store's own
guard files. v27 is a summary: one row per live record without its `observation`,
match or coverage maps, which `steer fix score --json` carries by the same `id`;
`attention.interventions` holds ids; `hooks` and `presets` read both scopes;
`resolver` rows keep only their arming readings; `scope` carries `repo_root`; and
`chains` is gone. v28 adds `posture.readiness`: the answer `steer status` prints.

- `status_version` — the schema version. `--json` is a stable agent contract.
- `posture` — the arming state: `instrument` (reviewer model / runs / prompt
  digest), `reviewer`, `ledger` path, and `commit_gate[]` / `resolver[]`. A
  `commit_gate` row carries `harness`, `scope`, `registered`, `effective`,
  `timeout`, `trust`, `command`, `resolvable`, `observed` and `observed_corpus`; a
  `resolver` row carries `harness`, `scope`, `registered`, `effective`, `trust` and
  `resolvable`. `readiness` combines those rows with the reviewer CLI into one
  answer: `reviewing` (a registration enforces and the reviewer CLI resolves),
  `headline`, `steps[]` (`title` and the `fix` command that clears it), `unverified[]`
  (trust that Steer cannot read) and an optional `note`. A
  registration is three separate readings, and only the first is what the
  configuration says. `command` is the command the registration names, verbatim.
  `resolvable` is whether that command resolves — `resolvable`, `missing`,
  `unknown`, or `not-registered` when there is no command to test. Treat it as a
  PROXY, never a proof: the hook runs under the harness's environment, whose
  PATH may differ, so it can prove a command absent and never prove one runs.
  **Act on `missing`:** neither harness reports a command it cannot resolve — the
  tool call runs, the hook is skipped, and nothing says so — and
  `steer bin install` repoints every registration at `~/.steer/bin/steer`.
  `observed` is the epoch second of the newest row the channel wrote to
  `observed_corpus`, 0 when it never wrote one, and both fields are absent when
  the channel writes no corpus of its own (the resolver). Never observed is not
  proof of breakage: nobody may have attempted the action the channel fires on.
  A corpus records no scope, so both scopes of one harness report the same
  evidence. One guard store serves both harnesses but each harness holds its
  own registration, so a scope alone does not identify a row: `commit_gate` and
  `resolver` report one row per harness per scope. `trust` is `not-applicable` (the harness reviews no hook trust),
  `record-present-unverified`, `missing` or `unknown` — Steer never claims to
  have verified a harness's internal hash. The resolver is the Stop hook that
  joins ledger rows to transcripts; it arms with the commit gate on the same
  scope. `unresolved_rows` counts the rows still owing that join — a window
  cannot count them until the resolver reads them, and `steer commit-gate backfill --apply`
  reads them now.
  `masters[]` is the guard master's runtime provenance, one row per harness per
  scope: `path`, `runtime` (the digest of the Python runtime spliced into it),
  `version` (the steer that wrote it), `expected` (this binary's runtime digest)
  `state` and `observed` (the epoch second of the last block in the audit log
  beside this registered master; the log holds blocks only, so a master that
  allowed every call reads as 0). `state` is `current`, `stale` (an older runtime, so a firing
  writes fewer audit fields than the join reads), `unknown` (an unstamped
  master — every store built before the stamp existed, which never reads as
  current), `detached` or `not-registered`. `detached` is an installed harness
  that runs none of the scope's guards: the store holds guards and the harness
  registers no runner for them, or one other than the store's. A store with guards
  and no runner file never reads `detached`: its repair is a rebuild.
  Its `store` names the runner the store holds. `trust` is the harness's approval of the
  registered runner, in the same values as a `posture` row's `trust`, and is
  absent when no runner is registered. A Codex runner reading `missing` runs no
  guard, while the guard's rule still reaches the agent as text; approve the
  definitions in `/hooks`. The row reads the REGISTERED master, not the one
  the store resolves to: a migrated machine holds a master at both paths. **Act
  on `stale` or `unknown`:** run `steer hooks rebuild --scope <scope>`, which
  regenerates the master AND re-registers it in one operation. **Act on
  `detached`:** run `steer hooks sync --scope <scope>`, which registers the
  runner the store already holds and writes nothing in the store; a clone of a
  repository whose guards were committed reads `detached` until it runs.
  `sources` reads the guard source the REGISTERED runner embeds against the
  scope's store files, and is a fourth fact beside registration, runtime
  freshness and detachment: a runner carrying this binary's runtime can still run
  source the store file no longer holds. Its `state` is `complete`, `no-master`,
  `unavailable` or `not-applicable`, and only `complete` supports a reading —
  `guards[]` then names each guard `matches`, `differs`, `absent-from-master` or
  `absent-from-store`. An `unavailable` comparison is not agreement. `differs` is
  directionless: it says the runner runs something other than the file, never
  which one is wanted, so read the guard file before rebuilding. A detached row
  also carries `store_sources`, the same reading for the runner the store holds,
  because the two choose different repairs: a store runner that already covers
  the store needs `sync`, not `rebuild`.
  `rules[]` reads each installed harness's `## Steer Rules` section against the
  manifest, one row per installed harness per scope; a harness that is not
  installed gets no row. `path` is the instruction file (CLAUDE.md or AGENTS.md),
  `expected` counts the rules the manifest names in that scope, `missing` lists
  the rule ids whose text the section does not carry, and `extra` lists section
  lines the manifest does not name, which the next sync removes. `state` is
  `current`, `drifted` or `unknown` (the file could not be read). A record whose
  rule is missing from a harness's file stays live while that agent never reads
  the rule: a harness installed after the rule was written, or a hand edit to the
  file, causes it. **Act on `drifted`:** run `steer hooks sync --scope <scope>`,
  which rewrites every installed harness's section from the manifest.
- `checks` — this repository's check tier on this machine, with an empty reading
  when `steer status` ran outside a repository. `repo` is the resolved Git root,
  `manifest` says whether it declares checks, and `adopted` / `adopted_at` say
  whether THIS machine authorized its `check.sh` to run at the end of a turn.
  Registration, authorization and observed execution are three separate readings,
  and a clone carries none of the second. `runner[]` is the Stop runner's
  registration in the same shape as a `posture` row, one per harness per scope;
  its `observed` is the newest run that harness's runner recorded, and its
  `trust` is `unknown` wherever a harness reviews hook trust, because Steer reads
  no Stop trust record for that event. `checks[]` is one row per declaration:
  `id`, `kind` (`script` or `go-test`), `binding` (the script, or the package and
  test), `validation` (`current`, `stale` or `none`) with `changed` naming what
  moved since the receipt in `receipt` / `validated_at`, and `execution` /
  `executed_at` / `execution_run` from the newest execution record here. **A
  stale check keeps running:** staleness is a label on the proof, never a repair
  the loop demands, and `steer check test <id>` replays the cases when a fix
  registration needs fresh proof. An absent `execution` is unobserved and never a
  pass — an aggregate `check.sh` outcome attributes no result to one assertion.
  `latest_run` is the newest run that did any work: `run_id`, `source`,
  `harness`, `outcome`, `disposition`, `finished`, `missing[]`,
  `integration_failure` and `refused_after`. A run the repository lock turned
  away ran nothing, so it never stands in for the result of the run that held the
  repository; `refused_after` is when such a run was turned away. **Act on
  `integration_failure`:** a passing `check.sh` that ran no declared check is the
  script's wiring and never a failed assertion — add the managed line naming
  `steer check exec --all` or `steer check go-test`.
- `interventions[]` — the **full** inventory, one summary row per live class:
  `id`, `class`, `problem`, `intervention`, `tier`, `target`, `status`,
  `hook_id`, `scope`, `artifact`, `corpus`, `unit`, `signal_kind`, `recurred`
  (the record matched work after its split) and `before` / `after` (`hits`,
  `units`, `rate`). Each row's detail is the `steer fix score --json` row with the
  same `id`; both are documented in `audit.md`, which Step 1 reads whenever this
  array holds a row.
- `summary` — `total` plus `counts` by action section (`inspect`, `repair`,
  `observe`).
- `attention` — the subset asking for a reader: `interventions[]` (the ids of
  records with a recurrence, an absent audit log or check evidence, or no frozen
  scope; the queue carries their full rows), `candidates[]` (no `match` yet),
  `unscored[]` (ids of records no engine reads), and `checks[]` — the open steps
  in this repository's check tier, the same rows the queue carries.
- `telemetry` — corpus sizes: `review_chains`, `audit_firings`, `check_blocks`
  (the check.sh Stop blocks recorded here that no exclusion retires),
  `session_snapshots`, plus `harnesses[]` — one row per harness with its
  `sessions`, `firings`, `check_blocks` and `chains`, so a harness that contributed
  nothing reads differently from one that was never mined.
- `corpora[]` — one row per corpus with `present`, `size` and `path`.
- `hooks[]` — the registered managed hooks in both scopes: `name`, `scope`,
  `source`, `rule`. One name can appear once per scope; `scope` and `name`
  together identify a guard.
- `presets[]` — the catalog roster, one row per preset: `name`, `bundle`,
  `description`, and `enabled_scopes` / `drifted_scopes`, the scopes it is enabled
  in and the scopes whose installed copy drifted from the catalog.
- `rules[]` — the instruction-tier rules in both stores: `id`, `scope`, `text`.
  Whether an agent's instructions carry one is `posture.rules[].missing`.
- `addenda[]` — the review addendum entries in both stores: `id`, `scope`,
  `text` (the store on disk), `head_text` (the committed text the commit gate
  reads for a project entry) and `state`: `live`, `uncommitted` (on disk, not at
  HEAD), `uncommitted-change` (the gate reads `head_text`), `uncommitted-removal`
  (removed on disk, still read at HEAD) or `unreadable` with `detail`. A global
  entry is read from disk, so it is `live` whenever the store holds it.
- `store_problems[]` — the guard stores a reader refused, each `scope` with the
  `error`. A row says `hooks`, `presets` and `rules` are INCOMPLETE for that
  scope, not empty: the guards the store registered still run. A manifest naming
  a file outside its store is refused this way, and an operator repairs it by
  editing the named entry.
- `team` — the repo-committed team lockfile summary: `present`, `path`,
  `hooks_locked`, `catalog_version`, and `error`. v10 adds `error`: a project with
  no lockfile is `present: false` and is not a fault, while a lockfile that exists
  and cannot be read is `present: true` with `error` naming why. Before v10 both
  read as absent.

### 2b. Commit-review findings

If this machine has a commit-review ledger, dump the findings. If not, skip to Step 3 and add nothing: Step 1's sweep sentence already told the reader that review history is absent, and what comes next.

```bash
steer commit-gate mine findings --kind accepted --undecided > "${TMPDIR:-/tmp}/gate-findings.txt"
steer commit-gate mine findings --kind rejected > "${TMPDIR:-/tmp}/gate-rejected.txt"
```

Read each file **once**. Do not cat them and do not re-read.

The queue's review candidate names how many chains carry an accepted finding that no class regex — active or rejected — yet covers (`undecided_chains`). A class counts as covering one only when it is live (active or rejected, never removed or reworked), mined from this corpus, and authored against that finding's verdict — a false-positive class decides rejected findings and leaves an accepted defect undecided. A transcript-friction class names a pattern in a session transcript, so its regex meeting a finding's text here is a coincidence. A record stored before the corpus tags existed reads as review-findings. That is the pool the classes below come from; the review side has no per-class threshold the tier can compute, because a new class has no regex until you author one.

`--undecided` hands you the pool, not the corpus: it drops the chains whose every accepted finding a live class already covers, so what you cluster over is what is left to decide. A chain that carries one covered finding and one residual finding stays, with the covered one shown in place and marked `[covered-by=<class-id>]`. Quote the finding text without that marker — it is generated classification metadata, not part of the reviewer's words. The header names how many findings are covered, how many remain, and over how many chains, which is the arithmetic that tells you whether your classes are finished.

Each chain header names the repository its rows recorded (`repo=`), so the project-or-global scope of a proposal is arithmetic over the chains in the class, not recall. Two checkouts sharing a directory name are both named by their whole path. A chain whose rows carried no repository metadata reads `repo=unknown`: it is a missing fact, so leave it out of the count rather than assigning it to the repository you are standing in. Counting where a class occurred tells you where it has been seen, never where it would occur — a class seen in one repository can still be a global rule.

Output is grouped by chain — one chain per commit attempt, findings deduplicated within it. The header line gives the denominator (`N chains reviewed`), which is what every rate on the review side is over. `--undecided` does not move that denominator: it narrows what you are shown, never what a rate is over, so an incidence printed under it means the same thing it means without it.

Above that header the dump lists every class already decided on this corpus, each with its incidence **now** and, for a rejection, its reason and reopen condition. Read that block before you cluster. It is the one place a past refusal reaches you at the moment you would otherwise re-derive it.

Pass `--since 60` to limit the window, `--strict-joins` to restrict to rows whose ledger join is reliable, and drop `--undecided` to see the covered chains too. Default is the full corpus, which is right for a first pass.

## Step 3: Analyze and cluster — this is your job, not the script's

Both corpora are mechanical evidence. All semantic judgment — what is hookable, how to phrase it — happens now, in this session. Tag every candidate with the corpus it came from.

### Transcript candidates — what some tier can fix

A transcript candidate qualifies when some tier in the routing guide (Step 4) can address it.

- A concrete pattern in an edit or a command: a pre-tool guard. "don't use console.log", "always python3 not python", "never commit .env files", "no rm -rf".
- A property of the finished tree: a repository check. "tests must pass before the turn ends", "no literal test counts in scripts".
- A judgment or workflow complaint with no mechanical test: an instruction rule, and only when it recurs across sessions and no instruction already says it. "ask before refactoring", "be more concise".

Discard the rest silently — do not pad the report.

Then:

1. **CLAUDE.md gap analysis first.** Directives are the highest-precision source: the user already wrote the rule down; it just isn't enforced. Check each directive a check or guard could detect against `guard_rules`, `hook_blocks` and the `checks` reading — skip anything a guard or check already covers. A directive no check or guard can detect is already in its strongest place; skip it. A directive that matches only an instruction rule (in `steer_rules`, not in `guard_rules`) is not enforced: propose a guard for it only with a reason why the instruction rule's `--not-mechanizable` argument no longer holds, and name the instruction rule the guard would replace.
2. **Corrections and failures second.** Weight by `sessions` over `count` (friction across sessions beats one bad session). Read the `examples` — the pattern key is normalized and lossy; the examples carry the user's actual words. Drop a cluster that a `steer_rules` line (guards and instruction rules both write one) or a check in the `checks` reading already answers: a fix `/steer` installed has no record in `interventions[]`, and the friction from before its install stays in the window. Where it keeps recurring after the install, propose registering the installed fix with `steer fix register` and its `--hook-id`, `--rule-id` or `--check-id`, not a second fix.
3. **Deduplicate across sources.** A CLAUDE.md directive corroborated by correction clusters is one candidate with stronger evidence, not two.
4. **Phrase each candidate as a rule.** A guard candidate uses `detect | suggest` syntax, e.g. `never use python bare | use python3 instead`. Reuse the user's own wording where possible.

### Review candidates — class qualification

The script does no semantic grouping by design. Read the findings and name the classes yourself. A review class qualifies only when all of these hold:

- It appears on **2 or more distinct chains**, and you state the count in the proposal. Two occurrences justify showing the class to the reader; the reader decides whether to install it. Say where you drew the class boundary, because the count follows from it: one class split in half can read as 2 and 3 where the merged class reads as 5.
- It spans **more than one commit and more than one file**. A defect that only ever hit one file is that file's bug, not a class.
- It names a **concrete, detectable tell** — a pattern in the source or the commit message, not a quality ("insufficient error handling").

Do the same pass over the rejected dump. A recurring **false-positive** class is equally actionable — it goes to the review addendum as a suppression, and it answers whether the rejected fraction is a prompt problem or reviewer noise.

A rejection is the author's verdict on its own code, so a rejection that recurs can also be an author blind spot. Before you propose a false-positive class, re-read each member finding against the code at its commit, not only the author's reason. A member whose finding holds makes the class a defect candidate, never a suppression.

Everything else is the irreducible cost of an independent reader. One-off findings are the gate working as designed; do not manufacture classes to fill a report.

Cap the whole report at the **top 5–7 candidates across both corpora**. Finding fewer — or none — is a valid result; say so plainly rather than padding with weak candidates.

## Step 4: Route each candidate to the strongest tier that can catch it

The routing guide is shared with the `steer` skill. Read it from this skill's
directory with symlinks resolved, because an installed skill directory can be a
link:

```bash
cat "$(cd "<this skill's base directory>" && pwd -P)/../steer/prompts/routing.md"
```

Route each candidate with it: name the enforcement point, then take the strongest
tier that meets it. An instruction-rule candidate also needs the recurrence Step 3
asks for.

The `--tier` flag takes either vocabulary and refuses anything else. Say the type,
not the flag value.

| `--tier` | Say |
|---|---|
| `check` | repository check |
| `steer` / `guard` | pre-tool guard |
| `prose` / `instruction` | instruction rule |
| `prompt` / `review` | review prompt |

Scope a guard project vs global — a rule seen across projects or from global CLAUDE.md suggests global scope. If nothing routes, say so. It is a correct result:

> No recurring problem was strong enough to propose a fix.

A detector the guide keeps out of `check.sh` may still be suggested as a manual or CI check. Say plainly what that leaves:

> Steer does not see this check run, so it cannot tell you if the problem comes back.

## Step 5: Present, then act

### How a proposal reads

Write each proposal as connected prose that answers five questions in order. A reader
who has never met Steer decides from it without a glossary.

1. **What problem keeps happening?** Show one representative example, in the user's own
   words where the evidence carries them.
2. **What supports that?** Name the source — session history or review history — the
   amount of relevant work it was counted over, and the limit on it.
3. **What change is proposed?** Say the behavior it affects, then name the scope as
   **project** or **global** in those words: project reaches this repository alone,
   global reaches every repository on this machine. Choose it from the evidence —
   chains from one repository scope to project, chains from several scope global —
   and give that reason in a clause. Then say which coding agents and which files.
   A **repository check** is always project scope. For one, also say what yes does:
   it arms this repository, the check then runs at the end of every Claude Code and
   Codex turn here and blocks a turn that fails it, and `steer check init` creates
   `check.sh` when the repository has none. Pi has no runner, so the check blocks no
   Pi turn.
4. **What is the tradeoff?** Name one concrete legitimate case the proposal could
   obstruct, and say what in the proposed detector or wording would catch it. Each
   tier has its own form: a **repository check** rejects valid repository content; a
   **guard** blocks an acceptable tool action; an **instruction rule** discourages a
   valid approach; a **review prompt entry** makes the reviewer flag correct code or
   a deliberate design choice. Then say how the proposal preserves that case, or what
   restriction stays. Keep the answer qualitative unless the evidence carries the rate
   you claim, which needs a denominator: "no corpus records this" states a limit on
   the measurement and is not an answer. If no concrete case comes up, give the
   boundary you examined and the uncertainty that remains; never invent a harm, and
   never call a proposal cost-free.
5. **What decision is needed?** State the action and what follows it in one question.
   For a fix, what follows is that Steer tells the user if the problem comes back.
   A check's question also carries what yes creates. Never promise that the problem
   becomes less frequent; no count here can show that.

   > Apply this guard? Steer tells you if the problem comes back.

   > Apply this check? It runs at the end of every turn in this repository, creates
   > `check.sh` if the repository has none, and Steer tells you if the problem comes back.

A worked proposal:

> The agent used `python` where this project requires `python3` in three recorded
> sessions. I suggest a guard that blocks the bare command and tells the agent to use
> `python3`. It is a project fix: every recorded session was in this repository, so it
> applies here alone, to Claude Code and Codex. It could also block a deliberate use of
> `python`, because it matches the bare command anywhere in a shell call. Apply this guard? Steer tells you if the problem comes back.

Carry the same shape into the audit half. A result reads as its counts and dates,
then the limit on them, then whether anything is asked of the reader.

Two rules from `audit.md` apply here too, at the moment you write the sentence.
State no direction and no percentage: "fewer since the fix", "the fix worked" and
"a 60% reduction" are causal claims these counts cannot carry, and they are the
natural sentence to reach for in a summary. And never hand the reader a reading's
name — "the record matched again", "the window is quiet", "the accounting is
incomplete" — in place of the facts that produced it, not even as a heading.

> The problem appeared in three of ten sessions before this guard was installed and
> in one of four since, the most recent on 14 September. Four sessions is too few to
> read a trend into, and the same rule also sits in CLAUDE.md, so these counts cannot
> separate the guard from the instruction. The guard stays installed; no action is
> needed now.

### Words the reader sees

Write in the vocabulary the CLI prints: a **fix** rather than an intervention, a
**recurring problem** rather than a class, a **result** rather than a verdict,
**review history** or **session history** rather than a corpus, a **review
sequence** rather than a chain, a **block** rather than a firing, a **decision**
rather than an adjudication, **stop tracking** rather than retire.

Use plain words in every explanation, heading and question, and in the stored
descriptions (`--problem`, `--intervention`) a later report shows. Field names and
measurement terms belong in these instructions and in commands, never in the
reader's text: say what one means instead. Reproduce commands, identifiers and
quoted evidence verbatim. Write each phrase this skill hands the reader as a
blockquote, and keep a negative example inline.

A reader needs these terms, and the report defines each one the first time it uses it:

- **Fix** — what Steer installs so that a recurring problem stops.
- **The kind of fix**, named by its type and never by its flag value, and defined by
  where it acts. A **repository check** fails the agent's turn at the end. A
  **pre-tool guard** blocks a command or an edit before it runs. An **instruction
  rule** is a line in CLAUDE.md or AGENTS.md. A **review prompt** entry changes what
  the commit reviewer flags.
- **Project** or **global**, as question 3 defines them.
- **Block** — an action a guard stopped. The word says nothing about whether the
  action was wrong.
- **Decision** — the author's acceptance or rejection of a review finding.

Say plainly which action does what. **Applying a fix** makes a change. **Stopping the
tracking** of a fix ends its measurement and leaves the change in place — it removes
nothing. **Removing a guard** takes the guard out and is a separate operation from
either. A reader who conflates them removes a rule they meant to keep.

Then ask which proposals to apply (multi-select). Create nothing unasked.

### Every record explains itself

`--problem` and `--intervention` are required on `register` and on `reject`, and the
CLI refuses a blank or a value that only repeats the class name. Author both **now**,
while the evidence is on screen. You are the last reader who has it: months later the
record is read from a status page by someone with the class name, the regex and
nothing else, and neither of those says what the class is.

- **`--problem`** — what goes wrong, in one sentence, in the vocabulary of the defect.
  Write it for a reader who was not in this session, not as "within-chain re-raised
  finding":

  > The reviewer raises a concern an earlier round of the same review already settled.

- **`--intervention`** — what this record does about it, as concrete behavior, not
  "added a check":

  > check.sh refuses a printed literal count.

  On a `reject`, describe the fix you considered. If you found none worth installing,
  say exactly:

  > no suitable fix was identified

  `--reason` still explains the decision; these two explain the class.
- **`--note`** stays optional and is for implementation detail that fits neither.

To correct a description on an existing record, use `steer fix describe ID
--problem … --intervention …`. It writes the prose and nothing else — the id, the
match, the scope, the baseline, the instrument and both windows are untouched, so a
clearer sentence never restarts the measurement. Omit a flag to keep that half.

### Transcript candidates — install through the tier's own workflow

Install each accepted transcript candidate through its tier's section in the routing guide. A pre-tool guard goes through the steer skill's generation protocol with the phrased rule — specialist prompt selection, hook generation, registration via `steer register`, and mandatory self-testing all apply unchanged. Do not duplicate or shortcut that protocol here.

A candidate routed to a repository check registers with `--tier check --check-id` under the same binding and `--scope-repo-root` rules as a review candidate.

Then register the class over the transcript corpus, so the fix you just installed is measured rather than assumed:

```bash
steer fix match 'YOUR REGEX' --corpus transcript-friction --signal-kind user_corrections \
  --scope-harness claude-code,codex

steer fix register \
  --class "bare python call" \
  --match 'YOUR REGEX' \
  --corpus transcript-friction --signal-kind user_corrections \
  --tier steer --target hooks/no-bare-python.py --hook-id no-bare-python \
  --scope-harness claude-code,codex \
  --problem "the session runs bare python and the interpreter is missing" \
  --intervention "a pre-tool guard refuses a bare python invocation" \
  --note "why this tell was chosen"
```

Iterate until the regex matches the class members and nothing else, as on the review path. `match` prints every matched session with its signals; read them, not only the count. With the record's `--scope-*` flags it prints the `registration baseline` and marks each session `counted in baseline` or `outside baseline`. A false match in a counted session enters the baseline, and only a `--supersedes` record removes it.

The unit here is the **root session**, not the chain: a class either occurred in a session or it did not, and a subagent thread is provenance on its root. `--signal-kind` names the classifier kind the regex is counted over — `any`, `user_corrections`, `user_rejections`, `tool_failures` or `hook_blocks` — and is frozen with the rest of the record. `--scope-path` is refused here, because a session carries no changed-path list; scope by harness or repo instead. The corpus lives in a snapshot store that `steer report refresh` writes, so a frozen baseline outlives the transcript the harness later rotates away.

Pass `--tier steer --hook-id` for a generated guard. That is what gives the record an audit-log join, so `observation.firing` reports what the guard actually blocked over the window. A quiet window beside `firing.rows: 0` is silence the guard did not produce, and a recurrence beside `firing.rows: 0` points at the matcher. Neither reading is available from the incidence alone.

### Review candidates — author the regex, install, register

For each accepted review candidate:

1. **Author the regex and test it before writing any fix:**

   ```bash
   steer fix match 'YOUR REGEX' --kind accepted
   ```

   `match` also reports every recorded class that covers chains your candidate matches. A high share against a rejected class means you are re-proposing it: read the reason before going further.

   Add `--show-residue` to see what the regex leaves behind: the findings of that kind it does not match, and the terms recurring among them, each with its count in the residue and its count across the whole corpus. Where the two counts are equal the term is the corpus's own vocabulary and names nothing; where the residue count is well under the corpus count, the regex already reaches most of that term's chains. A residue term with a high count and few matches is the class you did not name — either widen the regex or author a second class for it. The residue is per finding, so a chain your regex matches still contributes the findings inside it that it misses. What it lists is unmatched by **this** regex, which is not the same as unclassified: another registered class may already cover it, and `mine findings --undecided` is what answers that.

   Iterate until it matches the class members and nothing else. This regex is the class's permanent definition — `score` re-counts with it months from now, so an over-broad regex will report the class as recurring forever, and a commit-specific one will report a quiet window it did not earn. Prefer the defect's vocabulary over any single commit's identifiers.

2. **Install the fix** through its tier's workflow, and verify it fires on the real case:

   Follow the tier's section in the routing guide. For a repository check, the defect case reproduces the real finding. Step 3 accepts a check-tier record only once the check is validated, the repository adopted, and its Stop runner registered; `steer check list` shows all three.

3. **Register it**, after the fix is in:

   ```bash
   steer fix register \
     --class "stale test-count claim" \
     --match 'YOUR REGEX' \
     --kind defect \
     --tier check --target check.sh \
     --check-id no-literal-count \
     --scope-repo-root "$(git rev-parse --show-toplevel)" \
     --scope-harness claude-code --scope-path '\.go$' \
     --problem "a check prints a literal test count and the number goes stale" \
     --intervention "check.sh refuses a printed literal count" \
     --note "why this tell was chosen"
   ```

   Before you register, run `steer fix match` again with the same `--scope-*` flags, and the same `--installed-ts` if you pass one. Without a scope, `match` counts the whole corpus, which is not the baseline. With one, its `registration baseline` line is the count `register` freezes, and it marks each listed unit `counted in baseline` or `outside baseline`. Read every counted unit: a false match there enters the baseline, and only a `--supersedes` record removes it.

   `register` computes the pre-install baseline and refuses a regex with no baseline, since an unmatched class can never be scored. Use `--kind false-positive` for reviewer suppressions; it scores against rejected findings instead of accepted ones.

   **A scope is required.** It names the opportunity window the class is measured over, and it is frozen at register time. Pass `--scope-all` for every chain, or any of `--scope-harness IDS` (exact harness ids — `claude-code`, `codex` — repeatable or comma-separated), `--scope-repo REGEX` (matches the repository root basename), `--scope-repo-root PATH` (the one exact repository root, which two checkouts sharing a basename do not share) and `--scope-path REGEX` (matches a repo-relative changed path); the predicates combine with AND. **A `--tier steer` record must name its harnesses with `--scope-harness`, and each one must already carry the guard** — `register` refuses the record otherwise, because a scope that admits an unguarded harness pools guarded and unguarded work and can never come back true. A Codex runner with no approval record in Codex's `config.toml` counts as unguarded: Codex runs no hook the user has not approved in `/hooks`, so its sessions would read the guard's rule as text with no guard behind it. Install the guard in every harness (`steer register` now does this by default), then register one record per harness: Codex and Claude Code run different models, so their chains carry different base rates and a single pooled rate measures the mix, not the fix. A `--tier check` record answers the same rule: it names the harnesses whose Stop runner is registered, and `--scope-repo-root` rather than `--scope-all`. `prose` and `prompt` tiers are harness-independent and keep `--scope-all`. Scope repo and path metadata only — never the finding text. Selecting chains on the failure content is the bias the scope exists to remove, so a `check`-tier fix in Go code scopes to `--scope-path '\.go$'`, not to the finding's words. The baseline `register` reports is restricted to the scope and to chains that carry an adjudication; widen the scope if it reads as zero, and check `steer commit-gate backfill --apply` first, because unresolved rows shrink the baseline the write can see.

   **`--tier check` additionally requires `--check-id`** — the id in the repository's `.steer/checks/manifest.json`. `register` freezes that check's binding kind, resolved repository root, detector digest and current validation receipt onto the record, and refuses a check that is undeclared, has no current receipt (`steer check test <id>`), whose repository is not adopted on this machine (`steer check adopt`), or whose Stop runner is registered in no harness (`steer check install --harness <id>`). Without that binding the record can only be read beside an aggregate `check.sh` outcome, which attributes no result to its assertion. Records written before the binding existed stay readable as unbound, with execution attribution unavailable.

   **`--tier steer` additionally requires `--hook-id`** — the steer-manifest key, which is also the `hook_name` in `hooks/steer-audit.jsonl`. That id makes the audit-log join deterministic. Reconstructing it later from `target` means searching the manifest for a path, and the manifest stops describing a hook the moment this loop renames or retires it. `register` also copies the rule text and `rule_hash` from the manifest, so the join survives a rename, plus `hook_version` and `hook_digest` — the identity of the implementation that ran. The rule hash names the policy text; a guard repaired without a rule change moves the digest alone, which is what says the treatment changed under a class still collecting. The lookup reads whichever store holds the id and refuses an id held by both; `--hook-scope project|global` names one.

   **`--tier prose` additionally requires `--rule-id` and `--not-mechanizable`.** The prose has to exist before a record can name it: write it with `steer rules add <id> --rule "<text>"`, which prints the scope's rules first and refuses text a second id already holds, then pass that id. `register` copies the rule text and its `rule_hash` onto the record, and refuses an id the store does not hold. `--not-mechanizable` carries the argument the routing ladder asks for — one sentence naming why a repository check and a pre-tool guard cannot detect this class. It is refused blank and refused when it only restates the class name. Today that argument is made in a session and lost there, so no later reader can check it was made at all; the flag preserves its presence, and its soundness stays the reader's.

   **`--tier prompt` additionally requires `--addendum-id` and `--kind false-positive`.** The stock review prompt is compiled into the binary and fixed; a fix on this tier is an entry the commit gate adds to it. Write it with `steer commit-gate addendum add <id> --text "<text>"`, then pass that id. `register` copies the entry text and its `addendum_hash` onto the record, refuses an id the store does not hold, and refuses an id held by both stores unless `--addendum-scope project|global` names one. A defect class is refused, because an entry that stops the reviewer raising a real defect would read as a falling count. A project entry reaches the reviewer only once the store's `steer-manifest.json` is committed: the gate reads project entries from HEAD, so `register` refuses a project entry that HEAD does not hold with the same text. Commit the manifest first; `steer commit-gate addendum list` names each entry's state at HEAD.

   The audit log is a second, independent measurement: it says whether the hook ever *fired*. A steer fix with falling incidence and zero fires did not cause the fall, and incidence alone cannot tell you that.

   `register` refuses a candidate that shares half or more of its chains with a rejected class. Two escapes, and they mean different things: `--supersedes <id>` says the refusal is overturned and marks that record `reopened`, while `--force` says the two classes only look alike. Prefer the first.

For each review candidate you examine and refuse, record the refusal:

```bash
steer fix reject \
  --class "within-chain repeat finding" \
  --match 'YOUR REGEX' \
  --scope-all \
  --problem "the reviewer raises a concern an earlier round of the same review already settled" \
  --intervention "no suitable fix was identified" \
  --reason "why no gate was installed" \
  --reopen "the observation that would change this answer"
```

Author the regex the same way as for `register`, and test it with `match` first — a rejection whose regex matches nothing cannot recognise the class when it returns, so `reject` refuses one, exactly as `register` does. A rejection also takes a frozen scope, on the same `--scope-all` / `--scope-harness` / `--scope-repo` / `--scope-path` rule as `register`: it is the window the class's growth is watched over if the refusal is ever revisited.

A legacy record written before scopes existed carries no scope. `steer fix scope <id> --scope-…` installs one: it sets the scope once, recomputes the baseline over the eligible chains, and opens a fresh after-window at the migration moment so inspecting the record's own history cannot pick a scope that flatters it. An installed scope is frozen — correct one by retiring the record and registering afresh.

`--reason` and `--reopen` are both required, and the second is the load-bearing one. "It is not mechanizable" is a verdict on today's instrument, not a permanent property of the class. Name what would have to change: a signal that starts being persisted, a rate that triples, a tier that becomes cheap. The durable version of that reasoning belongs in a durable design note; the record here is what fires at re-proposal time.

**If your reason cites a count for a subset of the class, store that subset's regex.** Pass `--reopen-match '<regex>' --reopen-at <n>` together; either alone is refused. Without them the record keeps only the broad class regex, and a reason reading "only the numeric sub-case is detectable, and it is one chain" names a threshold no later pass can evaluate — the subset has no stored pattern to re-count. With them, the findings dump scores the subset on every run and prints `REOPEN CONDITION MET` when it reaches the threshold, so a refusal that time has overturned announces itself instead of waiting to be re-derived by hand.

Record a refusal only for a candidate you actually examined against the corpus. Every one-off finding is not a rejected class, and a log padded with them buries the four or five refusals that matter.

Register only after the fix exists. The install timestamp is the split point for every future measurement, so registering early credits the fix with commits it was never present for.

### Clear the queue

After you register, reject, rework, or remove anything, run the refresh once more:

```bash
steer report refresh --days 30
```

This rewrites the queue and its timestamp from the new state, so the SessionStart nudge stops pointing at work you just finished. Skip it only when you changed nothing this pass.

## Reading the numbers honestly

- **Keep the three kinds of evidence apart.** Fixtures establish expected outcomes on stored cases under the recorded identities; an audited firing supports a judgment about one attempted action; field counts describe matched recurrence over eligible work. None of them establishes efficacy.
- **The comparison is uncontrolled, so no count here credits a fix.** The standing instruction set — CLAUDE.md, AGENTS.md, imported globals, skills, hooks — can affect the agent's behaviour, no corpus records it, and a person edits it by hand between sessions. Report what recurred and what did not.
- **The adjudication is self-reported.** The review outcomes come from the agent that received the findings, so a class the agent systematically misjudges is invisible here. Treat incidence as a strong hint, never as ground truth.
- **The instrument moves under the measurement.** Every ledger row is stamped with the reviewer that produced it (`model/runs/prompt-digest`), and `observation.provenance` lists every identity that counted each window. When the two lists differ, two instruments counted these windows — re-read the findings before weighing the counts against each other. A regex leaning on the reviewer's framing verbs ("hardcodes", "still advertises") is far more exposed than one leaning on code vocabulary, which is a reason to prefer the latter when authoring.
  - The stamp cannot catch vendor-side drift. An external model alias whose behaviour changes with no local diff makes an unchanged instrument string weaker evidence than a changed one.
- **Workload moves the split.** A `check`-tier fix scoped to Python cannot recur in a month of shell commits, and that reads as silence. Read `observation.counts` for each window's date bounds and eligible size before reading anything into a quiet after-window.
- **A fresh record has an empty after-window.** The gate reviews on the order of a few commits a week, so `zero_eligible_after` is the normal state of a new record. Re-read on a cadence rather than the day after installing, and never read a fresh quiet window as success.
- **Never inject this history into the reviewer or the authoring session.** A reviewer handed past findings re-finds them instead of searching, and a list of old defects is a gauge rather than a directive. The output of this loop is a gate, not a reminder.
