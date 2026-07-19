---
name: steer-report
description: Scans Claude Code transcripts and CLAUDE.md for recurring friction, then suggests steer hooks backed by evidence. Use for steer onboarding ("what hooks should I create?"), periodic review of repeated corrections, or when the user asks to mine their usage history for hook candidates.
user-invocable: true
digest_version: 1
compatibility: Requires the steer CLI (report subcommand) on PATH or bundled under scripts/. Reads transcripts from ~/.claude/projects.
allowed-tools: Bash(steer *), Read
---

# Steer Report - Hook Suggestions from Usage History

Scan the user's recent Claude Code sessions for recurring friction — repeated corrections, rejected tool calls, recurring lint/test failures, and CLAUDE.md rules that aren't yet enforced — then propose steer hooks backed by evidence.

## Example Invocations

```
/steer-report
/steer-report --days 7
/steer-report --project-only
/steer-report --headless
```

## Step 0: Fresh-Context Gate (interactive mode only)

The analysis reads a sizable digest and reasons over it. If this conversation already contains substantial prior work — more than a few turns, or a large amount of loaded code and discussion — warn before proceeding:

> "A usage report works best in a fresh session: the analysis gets the full context window, and cost is lower because each step doesn't re-pay for this conversation's history. I recommend running `/steer-report` as the first message of a new session. Proceed here anyway?"

Respect the user's choice. If the conversation is fresh (this is among the first messages), skip the warning and proceed.

In `--headless` mode, skip this gate entirely and never ask questions.

## Step 1: Run the Scanner

Use `steer` from PATH if available, otherwise fall back to `${CLAUDE_PLUGIN_ROOT}/scripts/steer`. Redirect the digest to a temp file so it enters context exactly once — via Read, not via command output. Run a **single command**:

```bash
STEER_BIN="${CLAUDE_PLUGIN_ROOT:+${CLAUDE_PLUGIN_ROOT}/scripts/steer}"
STEER_BIN="${STEER_BIN:-steer}"
"${STEER_BIN}" report --days 30 > "${TMPDIR:-/tmp}/steer-digest.json"
```

Then Read `${TMPDIR:-/tmp}/steer-digest.json` **once**. Do not cat the file and do not re-read it later — everything needed for analysis is in that single read.

All projects are scanned by default (cross-project habits are the strongest candidates). Pass through any flags the user supplied (`--days N`, `--project-only`, `--max-digest-bytes N`). Strip `--headless` — it is a skill flag, not a CLI flag.

The digest is pure JSON (schema below); progress and warnings go to stderr. It is size-capped: the CLI evicts the lowest-scoring clusters to fit, and reports honesty in `stats`.

## Step 2: Read the Digest

Digest schema (`digest_version: 1`):

- `stats` — coverage: `sessions_scanned`, `bytes_scanned`, `clusters_found`, `clusters_emitted`, `truncated`. **Always relay coverage to the user in one sentence.** If `truncated` is true, say so and suggest narrowing (`--days 7`) or raising `--max-digest-bytes`.
- `claude_md[]` — per CLAUDE.md file: `steer_rules` (already enforced by hooks) and `directives` (standing instructions with their section heading, NOT yet enforced).
- `signals.user_corrections[]` — user turns matching correction language. Each cluster: `pattern`, `count`, `sessions` (distinct sessions), `first_seen`, `last_seen`, `examples` (up to 3 raw snippets).
- `signals.user_rejections[]` — tool calls the user declined at the permission prompt (pattern is `Tool: target`).
- `signals.tool_failures[]` — recurring lint/test/compile failures from Bash output.
- `signals.hook_blocks[]` — operations already blocked by existing hooks. These are enforcement *working*; use them only to avoid suggesting duplicates.

The digest is mechanical evidence. All semantic judgment — what is hookable, how to phrase it — happens now, in this session.

## Step 3: Analyze

Evaluate every cluster and directive against the **hookability filter**: a steer hook is a PreToolUse Python script doing regex/AST detection on file edits or Bash commands. A candidate is hookable only if a concrete textual pattern can detect the violation.

- Hookable: "don't use console.log", "always python3 not python", "never commit .env files", "no rm -rf".
- Not hookable: "be more concise", "ask before refactoring", tone/judgment/workflow complaints. Discard these silently — do not pad the report with unhookable items.

Then:

1. **CLAUDE.md gap analysis first.** Directives are the highest-precision source: the user already wrote the rule down; it just isn't enforced. Check each hookable directive against `steer_rules` and `hook_blocks` — skip anything already covered.
2. **Corrections and failures second.** Weight by `sessions` over `count` (friction across sessions beats one bad session). Read the `examples` — the pattern key is normalized and lossy; the examples carry the user's actual words.
3. **Deduplicate across sources.** A CLAUDE.md directive corroborated by correction clusters is one candidate with stronger evidence, not two candidates.
4. **Phrase each candidate as a steer rule** in `detect | suggest` syntax, e.g. `never use python bare | use python3 instead`. Reuse the user's own wording from examples where possible.

Cap the report at the **top 5–7 candidates**. Finding fewer — or none — is a valid result; say so plainly rather than padding with weak candidates.

## Step 4: Present the Report

For each candidate show:

- The proposed rule in `detect | suggest` syntax
- Evidence: source (CLAUDE.md directive / corrections / failures / rejections), counts, session spread, and one representative example quote
- Scope recommendation (project vs global — a rule seen across projects or from global CLAUDE.md suggests global scope)

Then ask which candidates to create (multi-select). Do not create anything unasked.

## Step 5: Hand Off to Steer

For each accepted candidate, follow the steer skill's generation protocol (the root `SKILL.md` of this plugin) with the phrased rule — specialist prompt selection, hook generation, registration via `steer register`, and mandatory self-testing all apply unchanged. Do not duplicate or shortcut that protocol here.

## Headless Mode

When the invocation contains `--headless`: skip the gate, never ask questions, run the scan, analyze, and output ONLY a JSON result as your final message:

```json
{
  "digest_version": 1,
  "stats": { "sessions_scanned": 0, "truncated": false },
  "candidates": [
    {
      "rule": "never use console.log | use logger instead",
      "scope": "project",
      "sources": ["claude_md", "user_corrections"],
      "evidence": "corrected in 4 sessions; directive in CLAUDE.md §Conventions"
    }
  ]
}
```

Headless mode reports candidates only — it never creates hooks.
