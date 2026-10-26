# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.1.2]: https://github.com/steer-sh/steer/releases/tag/v0.1.2
[0.1.1]: https://github.com/steer-sh/steer/releases/tag/v0.1.1
[0.1.0]: https://github.com/steer-sh/steer/releases/tag/v0.1.0
