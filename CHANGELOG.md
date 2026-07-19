# Changelog

## Unreleased

### Features

- **`/steer-report` skill**: Mines recent Claude Code transcripts and CLAUDE.md for recurring friction (repeated user corrections, rejected tool calls, lint/test failures, unenforced CLAUDE.md directives) and proposes evidence-backed steer hooks. Recommends a fresh session when invoked mid-conversation; `--headless` emits candidates as JSON without creating hooks.
- **`steer report` CLI command**: Streaming transcript scanner behind the skill. Scans `~/.claude/projects` over a look-back window (`--days`, default 30; all projects by default, `--project-only` to narrow) and emits a versioned, size-capped JSON digest (`--max-digest-bytes`, default 40KB) — lowest-scoring clusters are evicted to fit, and `stats.truncated` reports honesty. Pure JSON on stdout, progress on stderr.

### Fixes

- **Consolidation drops module-level constants**: `steer register` consolidation extracted `main()` and helper functions but silently dropped top-level assignments (e.g. compiled regexes), producing a master hook whose `NameError` was swallowed at runtime — the hook passed individual self-tests but enforced nothing. Constants are now carried into the master with prefixed names; the steer skill additionally verifies the consolidated master after registration.

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
