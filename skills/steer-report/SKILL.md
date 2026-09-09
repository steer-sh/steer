---
name: steer-report
description: Audits the interventions steer already installed, then mines Claude Code and Codex transcripts and the commit-review ledger for new hook candidates backed by evidence. Use for steer onboarding ("what hooks should I create?"), periodic review of installed hooks, or when the user asks to mine usage history for candidates.
user-invocable: true
digest_version: 2
compatibility: Requires the steer CLI (report and commit-gate subcommands) on PATH or bundled under scripts/. Reads transcripts from ~/.claude/projects and $CODEX_HOME/sessions, and, when present, the commit-review ledger under ~/.steer/state/commit-review (~/.claude/state/commit-review on a machine that has not yet migrated).
allowed-tools: Bash(steer *), Read, Edit, Write
---

# Steer Report — audit installed hooks, then mine for new ones

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
/steer-report --headless
```

## Step 0: Fresh-context gate (interactive mode only)

The analysis reads a sizable digest and a finding dump, then reasons over both. If this conversation already contains substantial prior work — more than a few turns, or a large amount of loaded code and discussion — warn before proceeding:

> "A usage report works best in a fresh session: the analysis gets the full context window, and cost is lower because each step doesn't re-pay for this conversation's history. I recommend running `/steer-report` as the first message of a new session. Proceed here anyway?"

Respect the user's choice. If the conversation is fresh, skip the warning and proceed. In `--headless` mode, skip this gate entirely and never ask questions.

## Step 1: Snapshot the loop, refresh the queue, then audit

First read the whole loop's posture and full class inventory in one snapshot:

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" status --json > "${TMPDIR:-/tmp}/steer-status.json"
```

Read `${TMPDIR:-/tmp}/steer-status.json` **once**. The `interventions[]` array is
the **full** inventory — every live class across every tier. This is your dedup
source: when you cluster new candidates in Step 3, drop any class already here. A
class in this list is already decided, so a new hook for it is a rework of that
record. The schema is documented in "The `steer status --json` contract" below.
Fail soft — if the command errors, say so in one line and continue; the audit
below still runs.

A deterministic tier runs this loop's mechanical half — scoring, corpus sweeps, threshold checks — with no model. It may have run on a schedule already; run it again now, synchronously, so you never read a stale queue. It takes seconds and spends no tokens.

```bash
steer report refresh --days 30
```

Then Read the queue **once**: `~/.steer/state/steer-report/queue.json` (`version: 5`). It holds:

- `interventions[]` — full contract records the tier flagged as **asking for a reader**: the class recurred after the split, a coverage cause left the window unaccounted for, or the record carries no frozen scope. These carry a `match`; they are the audit's action list, pre-filtered. Each carries `problem` and `intervention` — the record's own explanation of what goes wrong and what it does about it, authored when the class was found. Lead your report with those rather than with the class name or the regex, and correct one that no longer describes the evidence with `steer fix describe ID`, which changes no window. Each also carries the conditions the counts were taken under: `corpus` and `unit` (`review-findings` counts by `chain`, `transcript-friction` by `session` — never compare the two rates), `signal_kind` on a session-unit record, `scope` (the frozen eligibility rule, `null` on a legacy record), `before_coverage` / `after_coverage` (`total`, `eligible`, `ineligible`, `unknown`, `unknown_scope`, `unknown_evidence`, `pending`, `partial_exposure`), and `observation` — the five field blocks documented in the status contract below.
- `unscored[]` — ids of records no engine reads. Report their existence; do not read a result into them.
- `candidates[]` — pointers to new clusters, strongest first within each corpus. Transcript rows carry `pattern`/`count`/`sessions`; the review row carries `undecided_chains` and a `dump` command. These carry **no** `match` — authoring the regex is your job, below.
- `corpora[]` — one row per corpus (`review-findings`, `transcript-friction`, `auto-denials`) with `present` and `size`. Relay this sweep to the user in one sentence.

The queue tells you what is pending. The full dumps below are the raw material you cluster over. Read the review-history fixes for the complete audit — the queue lists only the ones asking for a reader, not the quiet ones worth showing too. If this machine has no commit-review ledger, `mine score` reports an empty corpus — say "no fixes to audit on this machine" in one line and go to Step 2.

```bash
steer fix score
```

`steer fix` is the fix command set — `match`, `register`, `reject`, `scope`, `score`,
`list`, `describe`, `retire`. `steer intervention` is the same set under its former
name and still works, as does `steer commit-gate mine`; `findings`, which dumps the
review corpus rather than managing a fix, lives only under the latter.

Each record reports before/after incidence over its own unit — the fraction of reviewed commits on which the class appeared for a `review-findings` record, or the fraction of root sessions carrying it for a `transcript-friction` one — split at the install timestamp. **The comparison is uncontrolled and the record makes no efficacy claim.** The dominant input to an agent's behaviour is its standing instruction set — CLAUDE.md, AGENTS.md, imported globals, skills, hooks — which no chain and no session records, which a person edits by hand between sessions, and which an instruction-tier fix edits directly. So a fall between the two windows is a fact about the corpus and never a fact about the fix.

Behaviour is established at install instead: a guard's stored fixtures replay real cases deterministically (`steer fix mine`, `steer test <hook>`). Field counts are maintenance evidence — they say whether a class is still happening, never whether a fix is what stopped it.

### Read the fields, speak the reading

Each record carries an `observation` with five blocks. Read those; say what they
show, in the same vocabulary the CLI prints: a **fix** rather than an
intervention, a **recurring problem** rather than a class, a **result** rather
than a verdict, **review history** or **session history** rather than a corpus, a
**review sequence** rather than a chain, **stop tracking** rather than retire.

| Block | What it holds | What you may say from it |
|---|---|---|
| `counts` | `split_ts`, `observed_ts`, `unit`, and per window `{hits, eligible, first_activity_ts, last_activity_ts}` | The two windows' sizes and dates. Never a rate comparison. |
| `recurrence` | `occurred`, `matches`, `example_kind`, `example`, `example_ts` | Whether the problem came back, and the newest example. |
| `firing` | `applicable`, `log_present`, `hook_id`, `scope`, `class_scoped`, `rows`, `units` | What the guard's audit stream recorded, and over what. |
| `coverage` | the two coverage maps plus `zero_eligible_after` | Which units the window could not account for. |
| `provenance` | `kind` plus the identities per window | Whether one instrument or two counted these windows. |

There are three readings and no fourth:

- **The problem came back.** `recurrence.occurred` is true. **Act on this first.** Open `recurrence.example` and decide whether the record still describes the problem. `example_kind` says what the example is: `adjudicated_finding` is a reviewer's confirmed concern; `cluster_key` is the text a classifier grouped a session's friction by, and is NOT a confirmed escaped failure. A recurrence beside `firing.rows: 0` on a known log points at the rule, its coverage or its registration — the guard recorded no block over work the class did match. On a record with `declined: true`, nothing was installed: the recurrence is a reason to reconsider the refusal, never a fix that failed.
- **The accounting is incomplete.** A coverage map counts `unknown_scope`, `unknown_evidence`, `pending` or `unknown` units, or `firing.applicable` is true while `log_present` is false. The window cannot be read at all. **Act on this:** only one cause has a command — a ledger row the resolver closed as gone clears with `steer commit-gate backfill --apply`. Review sequences carrying no repository or changed-path record, sessions carrying no project, and an absent guard audit log have none; name the cause rather than handing the user a command that changes nothing. A `null` `scope` is its own case: the record has no frozen eligibility rule, so its two windows count different opportunity sets. Scope it with `steer fix scope <id>`, which restarts its after-window.
- **The window is quiet.** Eligible work arrived and the class did not. Say that, and stop there. `zero_eligible_after` is the separate case: no eligible work has arrived at all, which is not a quiet window and is not a fault — every freshly installed record reads that way.

State no direction and no percentage. "Fewer since the fix", "the fix worked", "a 60% reduction" all state a causal claim these counts cannot carry. Say what was counted: "this problem hit 4 of 37 review sequences before the fix and recurred 0 times in the 40 after."

Read `provenance` before you weigh the two windows against each other. When the `before` and `after` lists differ, two instruments counted them — a different reviewer, or a different transcript parser — and the counts describe two measurements rather than one.

For every record whose problem came back, open the target file and decide between:

- **Rework** — the fix was aimed at the wrong tell. Fix it, then `register --supersedes <id>` so the old record is retired with a forward pointer and the new one gets a fresh baseline.
- **Remove** — the class is not mechanizable after all, or the defect stopped mattering. Delete the check and `retire <id> --reason "..."`.

A gate that never fires is not free. It costs oracle runtime on every commit and, worse, it reads as coverage that does not exist.

## Step 2: Gather new evidence from both corpora

### 2a. Transcript friction

Use `steer` from PATH if available, otherwise fall back to the plugin root (`PLUGIN_ROOT` on Codex, `CLAUDE_PLUGIN_ROOT` on Claude Code). Redirect the digest to a temp file so it enters context exactly once — via Read, not via command output. Run a **single command**:

```bash
STEER_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
STEER_BIN="${STEER_ROOT:+${STEER_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" report --days 30 > "${TMPDIR:-/tmp}/steer-digest.json"
```

Then Read `${TMPDIR:-/tmp}/steer-digest.json` **once**. Do not cat the file and do not re-read it later — everything needed for analysis is in that single read.

All projects and every harness are scanned by default (cross-project, cross-harness habits are the strongest candidates). Pass through any flags the user supplied (`--days N`, `--project-only`, `--harness claude-code|codex`, `--max-digest-bytes N`). Strip `--headless` — it is a skill flag, not a CLI flag.

The queue's transcript candidates list the strongest correction clusters, most of them holding a single occurrence. A cluster key is the user's own wording, so it merges verbatim repeats and nothing else — two complaints about one habit, phrased differently, arrive as two rows. Grouping them into classes is your job, not the tier's. Read the full digest to merge, split, or discard those rows and to catch anything the queue's cap left out.

Digest schema (`digest_version: 2`):

- `stats` — coverage: `sessions_scanned`, `threads_scanned`, `bytes_scanned`, `clusters_found`, `clusters_emitted`, `truncated`. **Always relay coverage to the user in one sentence.** If `truncated` is true, say so and suggest narrowing (`--days 7`) or raising `--max-digest-bytes`.
- `stats.harnesses[]` — per harness: `harness`, `files_scanned`, `sessions_scanned`, `threads_scanned`, `bytes_scanned`, `entries_scanned`, `entries_skipped`. A harness missing from this list was never reached; a harness present with zeros has no evidence on this machine. Say which when a harness contributes nothing.
- The session is the unit and a subagent thread is provenance under its root session. `threads_scanned` reports how many threads rode along; it is never added to `sessions_scanned`.
- `instruction_files[]` — per instruction document (CLAUDE.md or AGENTS.md), tagged with its `harness`: `steer_rules` (already enforced by hooks) and `directives` (standing instructions with their section heading, NOT yet enforced).
- `signals.user_corrections[]` — user turns matching correction language. Each cluster: `pattern`, `count`, `sessions` (distinct sessions), `first_seen`, `last_seen`, `examples` (up to 3 raw snippets).
- `signals.user_rejections[]` — tool calls the user declined at the permission prompt (pattern is `Tool: target`).
- `signals.tool_failures[]` — recurring lint/test/compile failures from shell-tool output, whichever harness ran the shell.
- `signals.hook_blocks[]` — operations already blocked by existing hooks. These are enforcement *working*; use them only to avoid suggesting duplicates.

The digest is size-capped: the CLI evicts the lowest-scoring clusters to fit, and reports honesty in `stats`.

### The `steer status --json` contract (`status_version: 11`)

Step 1 reads this snapshot for posture and the full class inventory. The unit is
the failure class and what the corpus recorded about it, not the hook count. This
schema is byte-shared with the root `SKILL.md`; change both together (see
CLAUDE.md Key Contracts). Fields:

**Read every count here as an observation.** The windows are uncontrolled: the
dominant input to an agent's behaviour is its standing instruction set, which no
chain and no session records, which a person edits by hand between sessions, and
which an instruction-tier fix edits directly. So the snapshot reports hits,
eligible units, window bounds, coverage, guard firings and the identities that
counted each window, and it reports no efficacy verdict. The intervention's
contribution to any change in these counts is unresolved. v11 removes `verdict`,
`p_value`, `effect`, `required_after`, `confounds`, `composition`,
`instrument_drift`, `firings_applicable`, `attribution` and `chain_rate`, and adds
`observation`.

- `status_version` — the schema version. `--json` is a stable agent contract.
- `posture` — the arming state: `instrument` (reviewer model / runs / prompt
  digest), `reviewer`, `ledger` path, and `commit_gate[]` / `denials[]` /
  `resolver[]`. Each row carries `harness`, `scope`, `registered`, `effective`,
  `trust`, `command`, `resolvable`, `observed` and `observed_corpus`. A
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
  `resolver` report one row per harness per scope, and `denials` is Claude
  Code's alone. `trust` is `not-applicable` (the harness reviews no hook trust),
  `record-present-unverified`, `missing` or `unknown` — Steer never claims to
  have verified a harness's internal hash. The resolver is the Stop hook that
  joins ledger rows to transcripts; it arms with the commit gate on the same
  scope. `unresolved_rows` counts the rows still owing that join — a window
  cannot account for them, and `steer commit-gate backfill --apply` clears them.
  `masters[]` is the guard master's runtime provenance, one row per harness per
  scope: `path`, `runtime` (the digest of the Python runtime spliced into it),
  `version` (the steer that wrote it), `expected` (this binary's runtime digest)
  `state` and `observed` (the epoch second of the last block in the audit log
  beside this registered master; the log holds blocks only, so a master that
  allowed every call reads as 0). `state` is `current`, `stale` (an older runtime, so a firing
  writes fewer audit fields than the join reads), `unknown` (an unstamped
  master — every store built before the stamp existed, which never reads as
  current) or `not-registered`. The row reads the REGISTERED master, not the one
  the store resolves to: a migrated machine holds a master at both paths. **Act
  on `stale` or `unknown`:** run `steer hooks rebuild --scope <scope>`, which
  regenerates the master AND re-registers it in one operation.
- `interventions[]` — the **full** inventory. It carries every live class. Each
  row: `id`, `class`, `problem`, `intervention`, `legacy_note`, `match` (the regex
  that defines the class), `tier`, `target`, `status` (`active` or `rejected`),
  `before` / `after` incidence (`hits`, `units`, `rate`), and for
  a refused class `reason`, `reopen` and `rejected_ts`. `problem` and `intervention` are the record's own explanation, authored when the
  evidence was in hand: what goes wrong, and what this record does about it. Lead
  with them — a `class` is a label and a `match` is a regex, and neither says what
  the class is. On a `rejected` row the `intervention` is the one that was
  considered, or the sentence `no suitable intervention was identified`.
  `legacy_note` is present only on a record written before those fields existed;
  it holds implementation detail, so never read it as either. This is the dedup source
  — a class here is decided. `corpus` and `unit` name the evidence the class is
  counted over and what one denominator entry is: `review-findings` counts by
  `chain`, `transcript-friction` by `session`. Never compare a chain rate to a
  session rate. A session-unit row also carries `signal_kind` — the classifier
  kind its regex was authored against (`any`, `user_corrections`,
  `user_rejections`, `tool_failures` or `hook_blocks`). Each row also carries
  its measurement conditions:
  - `scope` — the frozen eligibility rule: `{version, all}`, or `{version, repo,
    path}` at version 1, plus a `harness` id list at version 2. The predicates
    combine with AND. `null` is a legacy record: its two windows describe
    different opportunity sets, and no scope is ever inferred for it. Run
    `steer fix scope <id>`.
  - `before_coverage` / `after_coverage` — each window's unit accounting:
    `total`, `eligible` (the denominator), `ineligible`, `unknown`,
    `unknown_scope`, `unknown_evidence`, `pending`, `partial_exposure`. An
    unknown unit is not counted; it never shrinks the denominator.
    `partial_exposure` is the session corpus's own category, for a session that
    straddled the install and so ran under both regimes: `unknown` means the
    scope could not decide, `partial_exposure` means the window could not.
  - `observation` — what the corpus recorded. Five blocks:
    - `counts` — `split_ts` (the boundary: the install, the refusal, or the
      scope migration), `observed_ts` (the read's clock), `unit`, and `before` /
      `after`, each `{hits, eligible, first_activity_ts, last_activity_ts}`. The
      two timestamp pairs are different things: the period bounds express an
      empty window and say how long one has been quiet, and the activity bounds
      name the eligible work actually seen, which an empty window cannot express.
    - `recurrence` — `{occurred, matches, example_kind, example, example_ts}`.
      `example_kind` is `adjudicated_finding` (a reviewer's confirmed concern) or
      `cluster_key` (the text a classifier grouped a session's friction by, which
      is NOT a confirmed escaped failure). Read the example before acting.
    - `firing` — the guard audit stream: `{applicable, log_present, hook_id,
      scope, class_scoped, rows, units, newest_ts}`. `applicable` is false for a
      tier that writes no audit rows and for a record naming no hook.
      `log_present: false` is unknown activity, never a known zero. `scope` is
      `eligible_units` (rows joined to this class's own eligible units, and
      `class_scoped` is then true) or `window_period` (every row this hook wrote
      inside the window's bounds, whatever work it landed on). A `window_period`
      count cannot establish that the guard fired on this class's work.
    - `coverage` — `before` / `after`, the same maps as above, plus
      `zero_eligible_after`. That flag is a statement, not a fault: every freshly
      installed record has zero eligible after-units, and absent opportunity is
      not an observed quiet window.
    - `provenance` — `{kind, before, after}`. `kind` is `reviewer` (the reviewer
      identities that counted a chain window) or `parser` (the parser-and-adapter
      identities that produced a session window). Labels only: no rule here
      compares them. A window whose two lists differ was counted by two
      instruments, which is a reason to read the examples rather than the counts.
    - `declined` — true on a refused record. Its after-window is the period since
      the refusal, so a recurrence there implies no fix that failed.
- `summary` — `total` plus `counts` by action section (`inspect`, `repair`,
  `observe`).
- `chains[]` — recent review chains, findings split `accepted` / `rejected`.
- `attention` — the subset asking for a reader: `interventions[]` (a recurrence,
  a missing coverage cause, or no frozen scope), `candidates[]` (no `match` yet),
  and `unscored[]` (ids of records no engine reads).
- `telemetry` — corpus sizes: `review_chains`, `denials_rows`, `audit_firings`,
  `session_snapshots`, plus `harnesses[]` — one row per harness with its
  `sessions`, `firings` and `chains`, so a harness that contributed nothing reads
  differently from one that was never mined.
- `corpora[]` — one row per corpus with `present`, `size` and `path`.
- `hooks[]` — the registered managed hooks in the active scope: `name`, `scope`,
  `source`, `rule`.
- `presets[]` — the catalog roster: `name`, `bundle`, `description`, `enabled`,
  `drifted`.
- `team` — the repo-committed team lockfile summary: `present`, `path`,
  `hooks_locked`, `catalog_version`, and `error`. v10 adds `error`: a project with
  no lockfile is `present: false` and is not a fault, while a lockfile that exists
  and cannot be read is `present: true` with `error` naming why. Before v10 both
  read as absent.

### 2b. Commit-review findings

If this machine has a commit-review ledger, dump the findings. If not, say "no commit-review corpus on this machine" in one line and skip to Step 3.

```bash
steer commit-gate mine findings --kind accepted > "${TMPDIR:-/tmp}/gate-findings.txt"
steer commit-gate mine findings --kind rejected > "${TMPDIR:-/tmp}/gate-rejected.txt"
```

Read each file **once**. Do not cat them and do not re-read.

The queue's review candidate names how many chains carry an accepted finding that no class regex — active or rejected — yet covers (`undecided_chains`). That is the pool the classes below come from; the review side has no per-class threshold the tier can compute, because a new class has no regex until you author one.

Output is grouped by chain — one chain per commit attempt, findings deduplicated within it. The header line gives the denominator (`N chains reviewed`), which is what every rate on the review side is over.

Above that header the dump lists every class already decided on this corpus, each with its incidence **now** and, for a rejection, its reason and reopen condition. Read that block before you cluster. It is the one place a past refusal reaches you at the moment you would otherwise re-derive it.

Pass `--since 60` to limit the window, `--strict-joins` to restrict to rows whose ledger join is reliable. Default is the full corpus, which is right for a first pass.

## Step 3: Analyze and cluster — this is your job, not the script's

Both corpora are mechanical evidence. All semantic judgment — what is hookable, how to phrase it — happens now, in this session. Tag every candidate with the corpus it came from.

### Transcript candidates — the hookability filter

A steer hook is a PreToolUse Python script doing regex/AST detection on file edits or Bash commands. A transcript candidate is hookable only if a concrete textual pattern can detect the violation.

- Hookable: "don't use console.log", "always python3 not python", "never commit .env files", "no rm -rf".
- Not hookable: "be more concise", "ask before refactoring", tone/judgment/workflow complaints. Discard these silently — do not pad the report.

Then:

1. **CLAUDE.md gap analysis first.** Directives are the highest-precision source: the user already wrote the rule down; it just isn't enforced. Check each hookable directive against `steer_rules` and `hook_blocks` — skip anything already covered.
2. **Corrections and failures second.** Weight by `sessions` over `count` (friction across sessions beats one bad session). Read the `examples` — the pattern key is normalized and lossy; the examples carry the user's actual words.
3. **Deduplicate across sources.** A CLAUDE.md directive corroborated by correction clusters is one candidate with stronger evidence, not two.
4. **Phrase each candidate as a steer rule** in `detect | suggest` syntax, e.g. `never use python bare | use python3 instead`. Reuse the user's own wording where possible.

### Review candidates — class qualification

The script does no semantic grouping by design. Read the findings and name the classes yourself. A review class qualifies only when all of these hold:

- It appears on **2 or more distinct chains**, and you state the count in the proposal. Two occurrences justify showing the class to the reader; the reader decides whether to install it. Say where you drew the class boundary, because the count follows from it: one class split in half can read as 2 and 3 where the merged class reads as 5.
- It spans **more than one commit and more than one file**. A defect that only ever hit one file is that file's bug, not a class.
- It names a **concrete, detectable tell** — a pattern in the source or the commit message, not a quality ("insufficient error handling").

Do the same pass over the rejected dump. A recurring **false-positive** class is equally actionable — it goes to the reviewer prompt as a suppression, and it answers whether the rejected fraction is a prompt problem or reviewer noise.

Everything else is the irreducible cost of an independent reader. One-off findings are the gate working as designed; do not manufacture classes to fill a report.

Cap the whole report at the **top 5–7 candidates across both corpora**. Finding fewer — or none — is a valid result; say so plainly rather than padding with weak candidates.

## Step 4: Route each candidate to the strongest tier that can catch it

Take the first tier on this ladder that can actually detect the class. The ladder is ordered strongest oracle first: output quality converges to the oracles you can reach, and a prose rule only shifts a probability while an exit code does not.

The `--tier` flag takes either vocabulary — `check|steer|prose|prompt` or
`check|guard|instruction|review` — and refuses anything else. Say the type, not the
flag value.

| `--tier` | Say | Use when | Where it lands |
|---|---|---|---|
| `check` | repository check | A repo-local script can detect it deterministically | that repo's `check.sh`, via the `check-oracle` skill |
| `steer` / `guard` | pre-tool guard | It is detectable at edit time by regex/AST on a file or command | a PreToolUse hook, via the `steer` skill |
| `prompt` / `review` | review-prompt change | It is a recurring reviewer false positive | the reviewer prompt (`cli/internal/gate/prompts/review.md`) |
| `prose` / `instruction` | instruction rule | Genuinely not mechanizable but frequent enough to earn a line | CLAUDE.md |

An instruction rule is the weakest tier and the last one to reach for. Use it only after arguing why the class cannot be detected by a repository check or a pre-tool guard, and expect the class to keep recurring more often than not — that outcome is information, not failure.

A review-prompt change is not a rung you pass through on the way to prose. It applies to one candidate type only — a recurring reviewer false positive — and a defect class never routes there.

Transcript candidates almost always route to `steer` (edit-time regex) or `check` (a repo script). Scope each one project vs global — a rule seen across projects or from global CLAUDE.md suggests global scope. If nothing routes, say so. "No candidate qualified this pass" is a correct result.

## Step 5: Present, then act

### How a proposal reads

Write each proposal as connected prose that answers five questions in order. A reader
who has never met Steer decides from it without a glossary.

1. **What problem keeps happening?** Show one representative example, in the user's own
   words where the evidence carries them.
2. **What supports that?** Name the source — session history or review history — the
   amount of relevant work it was counted over, and the limit on it.
3. **What change is proposed?** Say the behavior it affects and where it applies: which
   projects, which coding agents, which files.
4. **What is the tradeoff?** Name the legitimate work it could block. Every proposal has
   one; a proposal claiming none has not been thought through.
5. **What decision is needed?** State the action and its consequence in one question.

A worked proposal:

> The agent used `python` where this project requires `python3` in three recorded
> sessions. I suggest a guard that blocks the bare command and tells the agent to use
> `python3`. It applies in this project, to Claude Code and Codex. It could also block
> an intentional use of `python`. Apply this guard and track whether the problem
> becomes less frequent?

Carry the same shape into the audit half. A result reads as a state, then its evidence,
then whether anything is asked of the reader:

> Too early to tell whether this guard helps. The problem appeared in three of ten
> sessions before installation and one of four since. More comparable sessions are
> needed. The guard stays installed; no action is needed now.

Name the tier by its type, never by its flag value: a **repository check**, a **pre-tool
guard**, an **instruction rule** or a **review-prompt change**.

Say plainly which action does what. **Applying a fix** makes a change. **Stopping the
tracking** of a fix ends its measurement and leaves the change in place — it removes
nothing. **Removing a guard** takes the guard out and is a separate operation from
either. A reader who conflates them removes a rule they meant to keep.

Then ask which candidates to act on (multi-select). Create nothing unasked.

### Every record explains itself

`--problem` and `--intervention` are required on `register` and on `reject`, and the
CLI refuses a blank or a value that only repeats the class name. Author both **now**,
while the evidence is on screen. You are the last reader who has it: months later the
record is read from a status page by someone with the class name, the regex and
nothing else, and neither of those says what the class is.

- **`--problem`** — what goes wrong, in one sentence, in the vocabulary of the defect.
  Write it for a reader who was not in this session. "The reviewer raises a concern an
  earlier round of the same review already settled", not "within-chain re-raised finding".
- **`--intervention`** — what this record does about it, as concrete behavior. "check.sh
  refuses a printed literal count", not "added a check". On a `reject`, describe the
  intervention you considered; if you found none worth installing, say exactly
  `no suitable intervention was identified`. `--reason` still explains the decision;
  these two explain the class.
- **`--note`** stays optional and is for implementation detail that fits neither.

To correct a description on an existing record, use `steer fix describe ID
--problem … --intervention …`. It writes the prose and nothing else — the id, the
match, the scope, the baseline, the instrument and both windows are untouched, so a
clearer sentence never restarts the measurement. Omit a flag to keep that half.

### Transcript candidates — hand off to steer

For each accepted transcript candidate, follow the steer skill's generation protocol (the root `SKILL.md` of this plugin) with the phrased rule — specialist prompt selection, hook generation, registration via `steer register`, and mandatory self-testing all apply unchanged. Do not duplicate or shortcut that protocol here.

Then register the class over the transcript corpus, so the guard you just installed is measured rather than assumed:

```bash
steer fix match 'YOUR REGEX' --corpus transcript-friction --signal-kind user_corrections

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

The unit here is the **root session**, not the chain: a class either occurred in a session or it did not, and a subagent thread is provenance on its root. `--signal-kind` names the classifier kind the regex is counted over — `any`, `user_corrections`, `user_rejections`, `tool_failures` or `hook_blocks` — and is frozen with the rest of the record. `--scope-path` is refused here, because a session carries no changed-path list; scope by harness or repo instead. The corpus lives in a snapshot store that `steer report refresh` writes, so a frozen baseline outlives the transcript the harness later rotates away.

Pass `--tier steer --hook-id` for a generated guard. That is what gives the record an audit-log join, so `observation.firing` reports what the guard actually blocked over the window. A quiet window beside `firing.rows: 0` is silence the guard did not produce, and a recurrence beside `firing.rows: 0` points at the matcher. Neither reading is available from the incidence alone.

### Review candidates — author the regex, install, register

For each accepted review candidate:

1. **Author the regex and test it before writing any fix:**

   ```bash
   steer fix match 'YOUR REGEX' --kind accepted
   ```

   `match` also reports every recorded class that covers chains your candidate matches. A high share against a rejected class means you are re-proposing it: read the reason before going further.

   Iterate until it matches the class members and nothing else. This regex is the class's permanent definition — `score` re-counts with it months from now, so an over-broad regex will report the class as recurring forever, and a commit-specific one will report a quiet window it did not earn. Prefer the defect's vocabulary over any single commit's identifiers.

2. **Install the fix** via the owning skill (`check-oracle` or `steer`), or by editing the reviewer prompt. Verify it fires on the real case.

3. **Register it**, after the fix is in:

   ```bash
   steer fix register \
     --class "stale test-count claim" \
     --match 'YOUR REGEX' \
     --kind defect \
     --tier check --target check.sh \
     --scope-repo 'steer' --scope-path '\.go$' \
     --problem "a check prints a literal test count and the number goes stale" \
     --intervention "check.sh refuses a printed literal count" \
     --note "why this tell was chosen"
   ```

   `register` computes the pre-install baseline and refuses a regex with no baseline, since an unmatched class can never be scored. Use `--kind false-positive` for reviewer suppressions; it scores against rejected findings instead of accepted ones.

   **A scope is required.** It names the opportunity window the class is measured over, and it is frozen at register time. Pass `--scope-all` for every chain, or any of `--scope-harness IDS` (exact harness ids — `claude-code`, `codex` — repeatable or comma-separated), `--scope-repo REGEX` (matches the repository root basename) and `--scope-path REGEX` (matches a repo-relative changed path); the predicates combine with AND. **A `--tier steer` record must name its harnesses with `--scope-harness`, and each one must already carry the guard** — `register` refuses the record otherwise, because a scope that admits an unguarded harness pools guarded and unguarded work and can never come back true. Install the guard in every harness (`steer register` now does this by default), then register one record per harness: Codex and Claude Code run different models, so their chains carry different base rates and a single pooled rate measures the mix, not the fix. `check`, `prose` and `prompt` tiers are harness-independent and keep `--scope-all`. Scope repo and path metadata only — never the finding text. Selecting chains on the failure content is the bias the scope exists to remove, so a `check`-tier fix in Go code scopes to `--scope-path '\.go$'`, not to the finding's words. The baseline `register` reports is restricted to the scope and to chains that carry an adjudication; widen the scope if it reads as zero, and check `steer commit-gate backfill --apply` first, because unresolved rows shrink the baseline the write can see.

   **`--tier steer` additionally requires `--hook-id`** — the steer-manifest key, which is also the `hook_name` in `hooks/steer-audit.jsonl`. That id makes the audit-log join deterministic. Reconstructing it later from `target` means searching the manifest for a path, and the manifest stops describing a hook the moment this loop renames or retires it. `register` also copies the rule text and `rule_hash` from the manifest, so the join survives a rename, plus `hook_version` and `hook_digest` — the identity of the implementation that ran. The rule hash names the policy text; a guard repaired without a rule change moves the digest alone, which is what says the treatment changed under a class still collecting. The lookup reads whichever store holds the id and refuses an id held by both; `--hook-scope project|global` names one.

   **`--tier prose` additionally requires `--rule-id` and `--not-mechanizable`.** The prose has to exist before a record can name it: write it with `steer rules add <id> --rule "<text>"`, which prints the scope's rules first and refuses text a second id already holds, then pass that id. `register` copies the rule text and its `rule_hash` onto the record, and refuses an id the store does not hold. `--not-mechanizable` carries the argument the routing ladder asks for — one sentence naming why a repository check and a pre-tool guard cannot detect this class. It is refused blank and refused when it only restates the class name. Today that argument is made in a session and lost there, so no later reader can check it was made at all; the flag preserves its presence, and its soundness stays the reader's.

   The audit log is a second, independent measurement: it says whether the hook ever *fired*. A steer fix with falling incidence and zero fires did not cause the fall, and incidence alone cannot tell you that.

   `register` refuses a candidate that shares half or more of its chains with a rejected class. Two escapes, and they mean different things: `--supersedes <id>` says the refusal is overturned and marks that record `reopened`, while `--force` says the two classes only look alike. Prefer the first.

For each review candidate you examine and refuse, record the refusal:

```bash
steer fix reject \
  --class "within-chain repeat finding" \
  --match 'YOUR REGEX' \
  --scope-all \
  --problem "the reviewer raises a concern an earlier round of the same review already settled" \
  --intervention "no suitable intervention was identified" \
  --reason "why no gate was installed" \
  --reopen "the observation that would change this answer"
```

Author the regex the same way as for `register`, and test it with `match` first — a rejection whose regex matches nothing cannot recognise the class when it returns, so `reject` refuses one, exactly as `register` does. A rejection also takes a frozen scope, on the same `--scope-all` / `--scope-harness` / `--scope-repo` / `--scope-path` rule as `register`: it is the window the class's growth is watched over if the refusal is ever revisited.

A legacy record written before scopes existed carries no scope. `steer fix scope <id> --scope-…` installs one: it sets the scope once, recomputes the baseline over the eligible chains, and opens a fresh after-window at the migration moment so inspecting the record's own history cannot pick a scope that flatters it. An installed scope is frozen — correct one by retiring the record and registering afresh.

`--reason` and `--reopen` are both required, and the second is the load-bearing one. "It is not mechanizable" is a verdict on today's instrument, not a permanent property of the class. Name what would have to change: a signal that starts being persisted, a rate that triples, a tier that becomes cheap. The durable version of that reasoning belongs in a durable design note; the record here is what fires at re-proposal time.

Record a refusal only for a candidate you actually examined against the corpus. Every one-off finding is not a rejected class, and a log padded with them buries the four or five refusals that matter.

Register only after the fix exists. The install timestamp is the split point for every future measurement, so registering early credits the fix with commits it was never present for.

### Clear the queue

After you register, reject, rework, or remove anything, run the refresh once more:

```bash
steer report refresh --days 30
```

This rewrites the queue and its timestamp from the new state, so the SessionStart nudge stops pointing at work you just finished. Skip it only when you changed nothing this pass.

## Reading the numbers honestly

- **The comparison is uncontrolled, so no count here credits a fix.** The standing instruction set — CLAUDE.md, AGENTS.md, imported globals, skills, hooks — is the dominant input to the agent's behaviour, no corpus records it, and a person edits it by hand between sessions. Report what recurred and what did not. Behaviour is established at install, by a guard's stored fixtures.
- **The adjudication is self-reported.** The review outcomes come from the agent that received the findings, so a class the agent systematically misjudges is invisible here. Treat incidence as a strong hint, never as ground truth.
- **The instrument moves under the measurement.** Every ledger row is stamped with the reviewer that produced it (`model/runs/prompt-digest`), and `observation.provenance` lists every identity that counted each window. When the two lists differ, two instruments counted these windows — re-read the findings before weighing the counts against each other. A regex leaning on the reviewer's framing verbs ("hardcodes", "still advertises") is far more exposed than one leaning on code vocabulary, which is a reason to prefer the latter when authoring.
  - The stamp cannot catch vendor-side drift. An external model alias whose behaviour changes with no local diff makes an unchanged instrument string weaker evidence than a changed one.
- **Workload moves the split.** A `check`-tier fix scoped to Python cannot recur in a month of shell commits, and that reads as silence. Read `observation.counts` for each window's date bounds and eligible size before reading anything into a quiet after-window.
- **A fresh record has an empty after-window.** The gate reviews on the order of a few commits a week, so `zero_eligible_after` is the normal state of a new record. Re-read on a cadence rather than the day after installing, and never read a fresh quiet window as success.
- **Never inject this history into the reviewer or the authoring session.** A reviewer handed past findings re-finds them instead of searching, and a list of old defects is a gauge rather than a directive. The output of this loop is a gate, not a reminder.

## Headless mode

When the invocation contains `--headless`: skip the gate, ask nothing, install nothing, and emit only JSON as the final message — the scored interventions and the proposed candidates from both corpora:

```json
{
  "digest_version": 2,
  "stats": { "sessions_scanned": 0, "threads_scanned": 0, "truncated": false },
  "interventions": [
    {
      "class": "stale test-count claim",
      "recurred": true,
      "before": "4/82",
      "after": "1/29"
    }
  ],
  "candidates": [
    {
      "corpus": "transcript-friction",
      "rule": "never use console.log | use logger instead",
      "scope": "project",
      "tier": "steer",
      "sources": ["instruction_files", "user_corrections"],
      "evidence": "corrected in 4 sessions; directive in CLAUDE.md §Conventions"
    },
    {
      "corpus": "review-findings",
      "class": "unchecked error return",
      "tier": "check",
      "target": "check.sh",
      "evidence": "flagged on 5 of 40 reviewed commits"
    }
  ]
}
```

Report an empty corpus as an empty array, never an error. Headless mode reports candidates only — it never creates hooks.
