# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.4] - 2026-10-30

### Added

- Commit gate: an `openai-compatible` reviewer provider reviews through an API key,
  with no reviewer CLI installed. It works with an OpenAI-compatible endpoint, such
  as OpenRouter, over https (plain http only on a loopback host). Set
  `reviewer.api.base_url` and a model; `/steer-setup` covers the path.
- `steer reviewer key set` stores the API key in
  `~/.steer/credentials/reviewer-api-key`. It reads from a terminal with no echo and
  refuses a pipe. The key can also come from the variable that
  `reviewer.api.key_env` names, and it never goes in `config.json`.
- `reviewer.api.tools` (`off`, `auto`, `required`; default `off`) lets an API
  reviewer call `read_file`, `grep` and `list_files` over each file as the commit
  lands it. Tools send more code to the endpoint and multiply the input tokens.

### Changed

- The commit-review ledger now has one lock for appends and rewrites, and the
  resolver reads under it. A binary older than 0.1.4 honours neither lock, so run
  `steer bin install` after the plugin update to keep the plugin copy and the stable
  executable on one version.
- The Go toolchain moves to 1.27.3 for GO-2026-6809 in `crypto/x509`.

### Fixed

- `steer git-hooks install` writes where git runs hooks. It reported success for a
  hook git never ran under `core.hooksPath`, and it failed in a linked worktree.
- Fixture runs and `steer test` read a guard's decision from stdout alone. A warning
  on stderr could turn a deny into an allow, and stderr that quoted a block could
  read as one.
- Presets: the postinstall guard checks the raw text of content that decodes to any
  JSON shape. Content that decoded to a non-object allowed the edit silently.
- Presets: the injection guard forgets a shell-safe proof when its name is rebound.
- Presets: self-protection reads every `steer` word in a command past its flags and
  shell punctuation, so those forms no longer hide the removal of a factory hook.
- Repository checks: a Go test unfinished when its test binary exits reads as failed
  and keeps the crash output. It read as unavailable before.

## [0.1.3] - 2026-10-26

### Changed

- A check.sh Stop block now names the other sessions that ran checks in the same
  working tree in the last hour. It tells the agent not to edit their work, and to
  record a contest when the failure does not come from its own task.
- The README's Update section gives the Claude Code commands that start a plugin
  update.

### Fixed

- `steer status` no longer offers the `bin install` of an earlier Claude Code
  plugin copy that the plugin cache keeps after an update.
- Commit gate: a Claude Code subagent keeps its own commit chain, so its first
  commit no longer reads as a retry of its parent's denied commit.
- Commit gate: one large caller file no longer keeps every later caller out of the
  review context.
- Commit gate: a ledger round that names no verdict, such as "see above", no longer
  counts as a round with zero concerns.
- Field counts no longer drop a finding when two chains ledger the same concern
  text in one reply.
- `steer status` and the dashboard read project registrations at the repository
  root when run from a subdirectory.
- `steer team lock`, `apply` and `verify` keep `steer-team.lock.json` at the
  project root, not in the working directory.

## [0.1.2] - 2026-10-25

### Changed

- Commit gate: before the author repairs a review finding, it must name the
  requirement that makes the repair necessary: the user's request, or code, docs or
  tests that existed before the change. Reviewers often construct edge cases that
  the repository never has to support. The author repaired them anyway, which
  added rounds and code without fixing a real failure. A finding with no such
  requirement can stay accepted but unfixed when the code stops safely on that
  input.
- Commit gate: a commit whose `cd` may not have run, or may have failed, is refused
  until it is rerun as `cd /path && git commit` or with `git -C`.

### Fixed

- Commit gate: a commit of a file with a non-ASCII name was reviewed without that
  file, or skipped as empty.
- Commit gate: a backslash escape in a path, a message or the `git commit` words
  could hide the commit or its repository from review.
- Commit gate: a commit whose `git` subcommand comes from a variable or an escape
  is refused as a possible commit.
- Commit gate: an exclusion pathspec in one `git add` no longer hides files that a
  later `git add` stages.
- Syncing the `## Steer Rules` section no longer deletes the headings and text after
  it.
- `steer verify` no longer writes synthetic firings into the live audit log.

## [0.1.1] - 2026-10-19

### Added

- `steer --version` prints the version, commit and build date of the running build.

### Changed

- `steer bin install` is the upgrade step after a plugin update: it also rebuilds a
  global guard runner built by an older Steer, and prints the rebuild command for a
  stale project runner, which the repository commits.
- The session-start message after a plugin update names the updated plugin's
  `bin install` command, and names a rebuild for every stale scope.

### Fixed

- Codex: a hook trust record counts under either spelling of a linked path.
- `/steer-setup`: a reviewer test that fails in Codex's network-off sandbox says so.

## [0.1.0] - 2026-10-17

First public release.

[0.1.4]: https://github.com/steer-sh/steer/releases/tag/v0.1.4
[0.1.3]: https://github.com/steer-sh/steer/releases/tag/v0.1.3
[0.1.2]: https://github.com/steer-sh/steer/releases/tag/v0.1.2
[0.1.1]: https://github.com/steer-sh/steer/releases/tag/v0.1.1
[0.1.0]: https://github.com/steer-sh/steer/releases/tag/v0.1.0
