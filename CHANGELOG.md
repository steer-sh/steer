# Changelog

## Unreleased

### Features

- **`steer commit-gate`**: a panel of independent reviewers red-teams a change's diff before it lands, blocking while a finding stands and writing an adjudication row for every decision. Three channels share one engine — `commit-gate review` (Claude Code PreToolUse), `commit-gate review-codex` (Codex PreToolUse), and `commit-gate precommit` (git hook) — with agy, Codex and `claude -p` as reviewer providers. `install --harness codex` preserves unrelated `hooks.json` keys and reports the manual trust step; `remove` and `status` cover both harnesses. New ledger rows identify their client and harness without invalidating the existing corpus.

- **`/steer-report` skill**: Mines recent Claude Code transcripts and CLAUDE.md for recurring friction (repeated user corrections, rejected tool calls, lint/test failures, unenforced CLAUDE.md directives) and proposes evidence-backed steer hooks. Recommends a fresh session when invoked mid-conversation; `--headless` emits candidates as JSON without creating hooks.
- **`steer report` CLI command**: Streaming transcript scanner behind the skill. Scans `~/.claude/projects` over a look-back window (`--days`, default 30; all projects by default, `--project-only` to narrow) and emits a versioned, size-capped JSON digest (`--max-digest-bytes`, default 40KB) — lowest-scoring clusters are evicted to fit, and `stats.truncated` reports honesty. Pure JSON on stdout, progress on stderr.
- **Audit log**: Every hook block appends a JSON line to `.claude/hooks/steer-audit.jsonl` (`timestamp`, `rule`, `hook_name`, `enforcement`, `file_path`, `tool_name`, `snippet`) — the artifact a team lead reads to answer "what did steer stop this week"
- **Team policy in git**: `steer team lock` writes a repo-committed lockfile with embedded hook content and integrity hashes; `steer team apply` reconciles a clone against it (floor semantics by default, `--force` for strict match, `--dry-run` to preview); `steer team verify --ci` fails pipelines on drift
- **Shared reconcile engine**: `steer preset sync` and `steer team apply` now run on one drift-detection/repair core

### Fixes

- **The Codex commit gate never fired**: the adapter matched `tool_name: "exec_command"` and read `tool_input.cmd`, while Codex reports `Bash` and `tool_input.command` for every shell path. The registered matcher was `exec_command` too, so the hook was not even invoked. Both now use `Bash`, the legacy names are still accepted on input, and `status` names a legacy registration as inert. A Codex subagent's calls key their chain on `agent_id` — its own rollout thread — so two threads of one session no longer share a firing cap, and rows carry the Codex author model, task and rollout the way Claude Code rows do. Denials use `hookSpecificOutput.permissionDecision: deny`.

- **settings.json round trip dropped keys on the innermost hook object**: registering or removing a steer hook rewrote every PreToolUse hook through a closed struct modeling only `type`, `command` and `timeout`, so any other key on a neighbouring hook — `statusMessage`, or anything Claude Code adds later — was silently lost. `CommandHook` and `HookEntry` now carry unmodeled keys through unchanged, the way the top level and the hook-event level already did. The commit gate's own hook gains a `statusMessage`.

- **Consolidation drops module-level constants**: `steer register` consolidation extracted `main()` and helper functions but silently dropped top-level assignments (e.g. compiled regexes), producing a master hook whose `NameError` was swallowed at runtime — the hook passed individual self-tests but enforced nothing. Constants are now carried into the master with prefixed names; the steer skill additionally verifies the consolidated master after registration.

### Security

- Self-protection hook now guards `steer-audit.jsonl` (a blocked agent can't erase its audit trail) and `steer-team.lock.json` (`steer team lock` is the sanctioned writer); `steer team` commands are classified as trusted reconcile paths
- Self-protection hardening: protected-path scan now runs before steer-CLI classification, and mutating commands are detected anywhere in a compound command — closes `rm steer-hooks.py; steer preset sync` style evasions

## v1.0.0

Initial release.

### Features

- **`/steer` skill**: Generate Claude Code hooks from natural language rules with automatic self-testing
- **Rule syntax**: `detect | suggest` — left side defines what to block, right side provides developer guidance
- **Hard + soft steering**: Hook-level enforcement (hard) paired with `CLAUDE.md` rule persistence (soft)
- **Steer CLI**: Full hook lifecycle management — `register`, `remove`, `list`, `test`, `verify`
- **Factory preset pack**: 12 security hooks shipped as built-in presets (`steer preset enable-bundle security`)
- **Self-protection hook**: Factory hooks guard their own persistence — agent can't disable or overwrite them
- **Git enforcement**: `steer verify --staged` for pre-commit checks, `steer git-hooks install` for automation
- **CI support**: `steer verify --changed --base <ref> --head <ref>` for pull request gates
- **Upsert-by-default registration**: `steer register` always succeeds — no interactive prompts, safe for agent workflows
- **Cross-platform**: Prebuilt binaries for macOS (amd64/arm64), Linux (amd64/arm64), Windows (amd64)
- **Standalone installer**: `install-prebuilt.sh` for CLI use outside the plugin system
