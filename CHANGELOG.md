# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.1.1]: https://github.com/steer-sh/steer/releases/tag/v0.1.1
[0.1.0]: https://github.com/steer-sh/steer/releases/tag/v0.1.0
